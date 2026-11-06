<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Repositories\DatabaseRepository;
use NimVor\Repositories\MigrationRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Crypto;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * The migration wizard: move websites and databases from another server into
 * existing NimVor websites and databases.
 *
 * The agent does everything that needs privileges (it connects to the source
 * over SSH with a pinned host key, discovers, copies, imports and verifies);
 * the panel keeps the wizard's choices, shows them, and asks. The source
 * login is encrypted here while the migration is unfinished, is sent to the
 * agent for each run only, and is erased when the migration completes or is
 * cancelled (or on request).
 */
final class MigrationService
{
    /** Panels that are not supported yet: listed so nobody assumes they are. */
    public const PLANNED = [
        'Plesk', 'CyberPanel', 'DirectAdmin', 'Virtualmin / Webmin', 'HestiaCP', 'aaPanel', 'ISPConfig',
        'Enhance', 'InterWorx', 'CloudPanel', 'VestaCP', 'Froxlor',
    ];

    /** Statuses in which the agent holds the migration's state. */
    private const AGENT_STATES = ['running', 'paused', 'failed', 'interrupted', 'completed', 'cancelled'];

    public function __construct(
        private readonly MigrationRepository $repo,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly Crypto $crypto,
        private readonly JobTracker $jobs,
        private readonly WebsiteRepository $sites,
        private readonly DatabaseRepository $dbs,
        private readonly IpService $ips,
    ) {
    }

    /** @return array{available: list<array<string, mixed>>, planned: list<string>} */
    public function sources(): array
    {
        $res = (array) $this->agent->call('migrate.panels');
        return ['available' => array_values((array) ($res['panels'] ?? [])), 'planned' => self::PLANNED];
    }

    /** @return list<array<string, mixed>> */
    public function list(): array
    {
        return $this->repo->list($this->server->id());
    }

    /** @return array<string, mixed>|null */
    public function find(int $id): ?array
    {
        return $this->repo->find($this->server->id(), $id);
    }

    public function create(string $panel, ?int $userId): int
    {
        $known = array_column($this->sources()['available'], 'panel');
        if (!in_array($panel, $known, true)) {
            throw new UserFacingException('This source is not supported yet.');
        }
        $id = $this->repo->insert($this->server->id(), $panel, $userId);
        $this->audit->record('migration.create', 'migration', (string) $id, AuditLogger::SUCCESS, ['panel' => $panel]);
        return $id;
    }

    /**
     * @param array<string, mixed> $in
     * @return array{host: string, port: int, user: string}
     */
    private function address(array $in): array
    {
        $errors = [];
        $host = trim(is_scalar($in['host'] ?? null) ? (string) $in['host'] : '', " \t[]");
        $port = trim(is_scalar($in['port'] ?? null) ? (string) $in['port'] : '');
        $user = trim(is_scalar($in['user'] ?? null) ? (string) $in['user'] : '');
        if ($host === '' || (!preg_match('/^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/', $host) && filter_var($host, FILTER_VALIDATE_IP) === false)) {
            $errors['host'] = 'Enter the source server\'s host name or IP address.';
        }
        $portNumber = $port === '' ? 22 : (ctype_digit($port) ? (int) $port : 0);
        if ($portNumber < 1 || $portNumber > 65535) {
            $errors['port'] = 'The SSH port is a number from 1 to 65535.';
        }
        if (!preg_match('/^[A-Za-z0-9._@+-]{1,100}$/', $user)) {
            $errors['user'] = 'Enter the SSH user name (root, or a user that can read everything you will move).';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['host' => $host, 'port' => $portNumber, 'user' => $user];
    }

    /**
     * Ask the source for its SSH host key, to be confirmed before any login is sent.
     *
     * @param array<string, mixed> $in
     * @return array<string, mixed>
     */
    public function probe(int $id, array $in): array
    {
        $m = $this->mustFind($id);
        $a = $this->address($in);
        $res = (array) $this->agent->call('migrate.host_key', ['host' => $a['host'], 'port' => $a['port']], 60);
        $pending = $a + [
            'host_key' => (string) ($res['host_key'] ?? ''),
            'key_type' => (string) ($res['key_type'] ?? ''),
            'fingerprint' => (string) ($res['fingerprint'] ?? ''),
        ];
        $this->repo->update($id, ['pending_key' => $pending, 'source_host' => $a['host'], 'source_port' => $a['port'], 'source_user' => $a['user'], 'status' => $m['status'] === 'draft' ? 'draft' : $m['status']]);
        return $pending;
    }

    /**
     * Log in with the confirmed host key and read the source's facts. The login
     * is encrypted and kept for this migration only.
     *
     * @param array<string, mixed> $in
     * @return array<string, mixed>
     */
    public function connect(int $id, array $in): array
    {
        $m = $this->mustFind($id);
        $pending = (array) $m['pending_key'];
        if (empty($pending['host_key'])) {
            throw new ValidationException(['host' => 'Check the server and confirm its SSH key first.'], 'The source server\'s key has not been confirmed.');
        }
        $password = is_scalar($in['password'] ?? null) ? (string) $in['password'] : '';
        $key = is_scalar($in['private_key'] ?? null) ? trim((string) $in['private_key']) : '';
        $errors = [];
        if ($password === '' && $key === '') {
            $errors['password'] = 'Enter the password, or paste a private key.';
        }
        if (strlen($password) > 500 || preg_match('/[\r\n\0]/', $password) === 1) {
            $errors['password'] = 'This password cannot be used.';
        }
        if ($key !== '' && (!str_contains($key, 'PRIVATE KEY-----') || strlen($key) > 16384 || str_contains($key, 'ENCRYPTED'))) {
            $errors['private_key'] = 'Paste an unencrypted private key in OpenSSH or PEM format (it starts with -----BEGIN).';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $login = ['password' => $key !== '' ? '' : $password, 'private_key' => $key, 'host_key' => (string) $pending['host_key']];
        $source = ['panel' => '', 'host' => $pending['host'], 'port' => $pending['port'], 'user' => $pending['user']] + $login;
        $details = ['panel' => $m['panel'], 'host' => $pending['host'], 'port' => $pending['port'], 'user' => $pending['user'], 'auth' => $key !== '' ? 'key' : 'password'];
        try {
            $res = (array) $this->agent->call('migrate.test', ['source' => $source], 120);
        } catch (AgentException $e) {
            $this->audit->record('migration.connect', 'migration', (string) $id, AuditLogger::FAILURE, $details);
            throw $e;
        }
        $this->repo->update($id, [
            'sealed' => $this->crypto->encrypt((string) json_encode($login, JSON_UNESCAPED_SLASHES)),
            'info' => $res,
            'status' => 'connected',
            'error' => '',
        ]);
        $this->audit->record('migration.connect', 'migration', (string) $id, AuditLogger::SUCCESS, $details);
        return $res;
    }

    /** @return array<string, mixed> the source as the agent takes it */
    private function source(array $m): array
    {
        $sealed = $this->repo->sealed((int) $m['id']);
        if ($sealed === '') {
            throw new UserFacingException('The source login is not kept (it is erased when a migration ends). Connect again.');
        }
        $login = (array) json_decode($this->crypto->decrypt($sealed), true);
        return ['panel' => (string) $m['panel'], 'host' => $m['source_host'], 'port' => $m['source_port'], 'user' => $m['source_user']] + $login;
    }

    public function discover(int $id): string
    {
        $m = $this->mustFind($id);
        $res = (array) $this->agent->call('migrate.discover', ['source' => $this->source($m)]);
        $job = (string) ($res['job_id'] ?? '');
        $this->repo->update($id, ['status' => 'discovering', 'job_id' => $job, 'error' => '']);
        $this->jobs->track($job, 'Discover ' . $m['source_host'], 'migration', (string) $id, null);
        $this->audit->record('migration.discover', 'migration', (string) $id, AuditLogger::SUCCESS, ['host' => $m['source_host']]);
        return $job;
    }

    /** Bring the stored state up to date with the agent (discovery job, running migration). */
    public function refresh(int $id): void
    {
        $m = $this->find($id);
        if ($m === null) {
            return;
        }
        try {
            if ($m['status'] === 'discovering' && $m['job_id'] !== '') {
                $job = (array) $this->agent->call('jobs.get', ['id' => $m['job_id']]);
                $st = (string) ($job['status'] ?? '');
                if ($st === 'succeeded') {
                    $this->repo->update($id, ['discovery' => (array) ($job['result'] ?? []), 'status' => 'discovered']);
                } elseif ($st === 'failed') {
                    $this->repo->update($id, ['status' => 'connected', 'error' => mb_substr((string) ($job['error'] ?? 'The discovery failed.'), 0, 500)]);
                }
            } elseif (in_array($m['status'], ['running', 'paused', 'interrupted', 'failed'], true) && $m['agent_id'] !== '') {
                $state = (array) $this->agent->call('migrate.get', ['id' => $m['agent_id']]);
                $new = (string) ($state['status'] ?? $m['status']);
                $fields = ['status' => $new, 'error' => mb_substr((string) ($state['error'] ?? ''), 0, 500)];
                if (in_array($new, ['completed', 'cancelled'], true)) {
                    $fields['sealed'] = ''; // the login is no longer needed
                    $fields['finished_at'] = gmdate('c');
                }
                $this->repo->update($id, $fields);
            }
        } catch (AgentException $e) {
            if ($e->errorCode() === 'not_found' && $m['status'] === 'discovering') {
                $this->repo->update($id, ['status' => 'connected', 'error' => 'The discovery job was lost (the agent restarted). Discover again.']);
            }
        }
    }

    /** @return array<string, mixed> the agent's state of a started migration */
    public function state(array $m): array
    {
        if ($m['agent_id'] === '') {
            return [];
        }
        try {
            return (array) $this->agent->call('migrate.get', ['id' => $m['agent_id']]);
        } catch (AgentException) {
            return [];
        }
    }

    /**
     * Build the plan from the person's choices: which source websites and
     * databases go into which NimVor websites and databases.
     *
     * @param array<string, mixed> $in
     * @return array<string, mixed> the plan
     */
    public function select(int $id, array $in): array
    {
        $m = $this->mustFind($id);
        $disc = (array) $m['discovery'];
        $errors = [];
        $files = [];
        $databases = [];
        $selection = ['domains' => [], 'databases' => []];
        $siteRows = [];
        foreach ($this->sites->all($this->server->id(), ['role_scope' => 'all', 'id' => 0]) as $s) {
            $siteRows[(int) $s['id']] = $s;
        }
        $dbRows = [];
        foreach ($this->dbs->databases($this->server->id(), ['role_scope' => 'all', 'id' => 0]) as $d) {
            $dbRows[(int) $d['id']] = $d;
        }
        $accounts = [];
        foreach ((array) ($disc['accounts'] ?? []) as $a) {
            $accounts[(string) $a['id']] = (array) $a;
        }
        $usedSites = [];
        foreach ((array) ($in['domains'] ?? []) as $key) {
            [$acct, $domain] = array_pad(explode('|', (string) $key, 2), 2, '');
            $found = null;
            foreach ((array) ($accounts[$acct]['domains'] ?? []) as $d) {
                if (($d['name'] ?? '') === $domain) {
                    $found = (array) $d;
                }
            }
            $sid = (int) (((array) ($in['site'] ?? []))[$key] ?? 0);
            if ($found === null) {
                $errors['domains'] = 'A website you selected is not in the discovery. Discover again.';
                continue;
            }
            if (!isset($siteRows[$sid])) {
                $errors['site[' . $key . ']'] = 'Choose the NimVor website that receives ' . $domain . '. Create it first under Websites if it does not exist.';
                continue;
            }
            if (isset($usedSites[$sid])) {
                $errors['site[' . $key . ']'] = $siteRows[$sid]['domain'] . ' is already the destination of another website you selected.';
                continue;
            }
            $usedSites[$sid] = true;
            $s = $siteRows[$sid];
            $files[] = ['domain' => (string) $s['domain'], 'source_root' => (string) $found['doc_root'], 'dest_root' => (string) $s['document_root'], 'dest_user' => (string) $s['system_user'], 'size_kb' => (int) ($found['size_kb'] ?? -1)];
            $selection['domains'][] = ['key' => (string) $key, 'site_id' => $sid];
        }
        $usedDbs = [];
        foreach ((array) ($in['dbs'] ?? []) as $key) {
            [$acct, $name] = array_pad(explode('|', (string) $key, 2), 2, '');
            $found = null;
            foreach ((array) ($accounts[$acct]['databases'] ?? []) as $d) {
                if (($d['name'] ?? '') === $name) {
                    $found = (array) $d;
                }
            }
            $did = (int) (((array) ($in['dbmap'] ?? []))[$key] ?? 0);
            if ($found === null) {
                $errors['dbs'] = 'A database you selected is not in the discovery. Discover again.';
                continue;
            }
            if (!isset($dbRows[$did])) {
                $errors['dbmap[' . $key . ']'] = 'Choose the NimVor database that receives ' . $name . '. Create it first under Databases.';
                continue;
            }
            if (isset($usedDbs[$did])) {
                $errors['dbmap[' . $key . ']'] = $dbRows[$did]['name'] . ' is already the destination of another database you selected.';
                continue;
            }
            $usedDbs[$did] = true;
            $databases[] = ['source' => (string) $found['name'], 'dest' => (string) $dbRows[$did]['name'], 'size_kb' => (int) ($found['size_kb'] ?? 0)];
            $selection['databases'][] = ['key' => (string) $key, 'db_id' => $did];
        }
        if ($files === [] && $databases === [] && $errors === []) {
            $errors['domains'] = 'Select at least one website or database.';
        }
        $workers = (int) ($in['workers'] ?? 2);
        if ($workers < 1 || $workers > 8) {
            $errors['workers'] = 'Use 1 to 8 websites at the same time.';
        }
        $excludes = array_values(array_filter(array_map('trim', preg_split('/\R/', (string) ($in['excludes'] ?? '')) ?: [])));
        foreach ($excludes as $x) {
            if (!preg_match('#^[A-Za-z0-9._*?/\[\]-]{1,200}$#', $x) || str_contains($x, '..')) {
                $errors['excludes'] = 'Excluded paths use letters, digits and . _ * ? / [ ] - only, one per line.';
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $plan = [
            'files' => $files,
            'databases' => $databases,
            'config' => ['compress' => !empty($in['compress']), 'workers' => $workers, 'excludes' => $excludes, 'allow_overwrite' => !empty($in['allow_overwrite'])],
        ];
        $selection['config'] = $plan['config'];
        $this->repo->update($id, ['selection' => $selection, 'plan' => $plan, 'status' => 'selected', 'assessment' => []]);
        return $plan;
    }

    /** @return array<string, mixed> findings and whether they block */
    public function assess(int $id): array
    {
        $m = $this->mustFind($id);
        if (((array) $m['plan']) === []) {
            throw new UserFacingException('Choose what to migrate first.');
        }
        $res = (array) $this->agent->call('migrate.assess', ['discovery' => (array) $m['discovery'], 'plan' => (array) $m['plan']], 120);
        $this->repo->update($id, ['assessment' => $res, 'status' => 'assessed']);
        return $res;
    }

    /** Store the plan in the agent and start it. */
    public function begin(int $id): string
    {
        $m = $this->mustFind($id);
        $a = (array) $m['assessment'];
        if ($a === [] || $m['status'] !== 'assessed') {
            throw new UserFacingException('Run the compatibility check first.');
        }
        if (!empty($a['blocked'])) {
            throw new UserFacingException('The compatibility check found problems that stop the migration. Solve them and check again.');
        }
        $created = (array) $this->agent->call('migrate.create', ['plan' => (array) $m['plan'], 'panel' => $m['panel'], 'source' => 'ssh://' . $m['source_user'] . '@' . $m['source_host'] . ':' . $m['source_port']]);
        $agentId = (string) ($created['id'] ?? '');
        $res = (array) $this->agent->call('migrate.start', ['id' => $agentId, 'source' => $this->source($m)]);
        $job = (string) ($res['job_id'] ?? '');
        $this->repo->update($id, ['agent_id' => $agentId, 'job_id' => $job, 'status' => 'running', 'error' => '']);
        $this->jobs->track($job, 'Migration from ' . $m['source_host'], 'migration', (string) $id, null);
        $this->audit->record('migration.start', 'migration', (string) $id, AuditLogger::SUCCESS, [
            'host' => $m['source_host'], 'websites' => count((array) ($m['plan']['files'] ?? [])), 'databases' => count((array) ($m['plan']['databases'] ?? [])),
            'allow_overwrite' => !empty($m['plan']['config']['allow_overwrite']),
        ]);
        return $job;
    }

    public function resume(int $id): string
    {
        $this->refresh($id);
        $m = $this->mustFind($id);
        if ($m['agent_id'] === '' || !in_array($m['status'], ['paused', 'failed', 'interrupted'], true)) {
            throw new UserFacingException('This migration cannot be continued.');
        }
        $res = (array) $this->agent->call('migrate.resume', ['id' => $m['agent_id'], 'source' => $this->source($m)]);
        $this->repo->update($id, ['job_id' => (string) ($res['job_id'] ?? ''), 'status' => 'running', 'error' => '']);
        $this->audit->record('migration.resume', 'migration', (string) $id);
        return (string) ($res['job_id'] ?? '');
    }

    public function pause(int $id): void
    {
        $m = $this->mustFind($id);
        $this->agent->call('migrate.pause', ['id' => $m['agent_id']]);
        $this->audit->record('migration.pause', 'migration', (string) $id);
    }

    public function cancel(int $id): void
    {
        $m = $this->mustFind($id);
        if ($m['agent_id'] !== '') {
            $this->agent->call('migrate.cancel', ['id' => $m['agent_id']]);
        }
        $this->repo->update($id, ['status' => 'cancelled', 'sealed' => '', 'finished_at' => gmdate('c')]);
        $this->audit->record('migration.cancel', 'migration', (string) $id);
    }

    /**
     * Check what the migration left in this server (reads only; the agent keeps the result).
     *
     * @return array<string, mixed>
     */
    public function validate(int $id): array
    {
        $this->refresh($id);
        $m = $this->mustFind($id);
        if ($m['agent_id'] === '' || in_array($m['status'], ['running', 'rolled_back', 'discovering'], true) || in_array($m['status'], ['draft', 'connected', 'discovered', 'selected', 'assessed'], true)) {
            throw new UserFacingException('There is nothing to validate yet: validate a migration that has run and is not running or rolled back.');
        }
        $rep = (array) $this->agent->call('migrate.validate', ['id' => $m['agent_id']], 330);
        $this->audit->record('migration.validate', 'migration', (string) $id, ((int) ($rep['failed'] ?? 0)) > 0 ? AuditLogger::FAILURE : AuditLogger::SUCCESS, [
            'passed' => (int) ($rep['passed'] ?? 0), 'warned' => (int) ($rep['warned'] ?? 0), 'failed' => (int) ($rep['failed'] ?? 0),
        ]);
        return $rep;
    }

    /**
     * Read where each migrated website's name points now and whether that is this server. It reads
     * DNS only: it never changes a record or a nameserver.
     *
     * @return array<string, mixed>
     */
    public function cutoverCheck(int $id): array
    {
        $this->refresh($id);
        $m = $this->mustFind($id);
        if ($m['agent_id'] === '' || in_array($m['status'], ['rolled_back', 'running', 'discovering'], true) || in_array($m['status'], ['draft', 'connected', 'discovered', 'selected', 'assessed'], true)) {
            throw new UserFacingException('There is nothing to switch yet: check DNS for a migration that has run and is not running or rolled back.');
        }
        // Only public addresses can be what a name on the internet points to (not 10.x, 192.168.x, fc00::/7…).
        $addresses = array_values(array_filter($this->ips->onServer(), static fn (string $ip): bool => filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE) !== false));
        if ($addresses === []) {
            throw new UserFacingException('This server\'s public addresses are not known (a server behind NAT only sees its private address), so DNS cannot be compared.');
        }
        $rep = (array) $this->agent->call('migrate.cutover_check', ['id' => $m['agent_id'], 'server_ips' => array_values($addresses)], 150);
        $this->audit->record('migration.dns_check', 'migration', (string) $id, AuditLogger::SUCCESS, ['domains' => count((array) ($rep['domains'] ?? []))]);
        return $rep;
    }

    /**
     * Undo the migration's changes in this server: remove the files it created while they are
     * unchanged, and (only when asked) empty the databases it filled. Newer data is kept.
     *
     * @return array<string, mixed> what was removed and what was kept
     */
    public function rollback(int $id, bool $confirm, bool $emptyDatabases): array
    {
        $this->refresh($id); // judge it by what the agent says now, not by an old page
        $m = $this->mustFind($id);
        if (!$confirm) {
            throw new UserFacingException('Confirm the rollback: it removes files from this server.');
        }
        if ($m['agent_id'] === '' || !in_array($m['status'], ['completed', 'failed', 'cancelled', 'paused', 'interrupted'], true)) {
            throw new UserFacingException('This migration cannot be rolled back (it never started, is still running, or was rolled back already).');
        }
        try {
            $rep = (array) $this->agent->call('migrate.rollback', ['id' => $m['agent_id'], 'confirm' => true, 'empty_databases' => $emptyDatabases], 660);
        } catch (AgentException $e) {
            $this->audit->record('migration.rollback', 'migration', (string) $id, AuditLogger::FAILURE, ['empty_databases' => $emptyDatabases]);
            throw $e;
        }
        $this->repo->update($id, ['status' => 'rolled_back', 'sealed' => '', 'finished_at' => gmdate('c'), 'error' => '']);
        $this->audit->record('migration.rollback', 'migration', (string) $id, AuditLogger::SUCCESS, [
            'empty_databases' => $emptyDatabases, 'files_removed' => (int) ($rep['files_removed'] ?? 0),
            'kept' => count((array) ($rep['kept'] ?? [])), 'databases_emptied' => count((array) ($rep['databases_emptied'] ?? [])),
        ]);
        return $rep;
    }

    /** Erase the stored login (a migration that needs it cannot continue until connected again). */
    public function forgetLogin(int $id): void
    {
        $this->mustFind($id);
        $this->repo->update($id, ['sealed' => '']);
        $this->audit->record('migration.forget_login', 'migration', (string) $id);
    }

    /** Remove a migration that did not start, or that is finished. */
    public function delete(int $id): void
    {
        $m = $this->mustFind($id);
        if (in_array($m['status'], ['running'], true)) {
            throw new UserFacingException('A running migration cannot be removed. Cancel it first.');
        }
        $this->repo->delete($id);
        $this->audit->record('migration.delete', 'migration', (string) $id);
    }

    /**
     * The report: what was moved, what failed, what to check. No credential is in it.
     *
     * @return array<string, mixed>
     */
    public function report(int $id): array
    {
        $m = $this->mustFind($id);
        $state = $this->state($m);
        $tasks = (array) ($state['tasks'] ?? []);
        $disc = (array) $m['discovery'];
        return [
            'migration' => $id,
            'status' => $m['status'],
            'generated_at' => gmdate('c'),
            'source' => ['panel' => $m['panel'], 'host' => $m['source_host'], 'port' => $m['source_port'], 'user' => $m['source_user'], 'server' => (array) ($disc['server'] ?? $m['info']['server'] ?? [])],
            'created_at' => $m['created_at'],
            'finished_at' => $m['finished_at'],
            'plan' => (array) $m['plan'],
            'assessment' => (array) ($m['assessment']['findings'] ?? []),
            'tasks' => $tasks,
            'failed' => array_values(array_filter($tasks, static fn (array $t): bool => ($t['status'] ?? '') === 'failed')),
            'differences' => array_values(array_filter($tasks, static fn (array $t): bool => (int) ($t['differences'] ?? 0) > 0)),
            'remaining' => (array) ($state['remaining'] ?? []),
            'rollback' => $state['rollback'] ?? null,
            'validation' => $state['validation'] ?? null,
            'dns' => $state['cutover'] ?? null,
            'not_migrated' => (array) ($disc['not_migrated'] ?? []),
            'recommendations' => [
                'A rollback is available while the migration has not been rolled back: it removes only files the migration created and that are unchanged, and empties databases only when you confirm and nothing was written to them since.',
                'Open each migrated website and check pages, forms, redirects and logins: .htaccess rules are not translated to Nginx.',
                'Files that differ from the source (see "differences") changed while they were copied: run the migration again to synchronise them before switching DNS.',
                'Nothing was deleted on the source. Keep it until you have checked the new site, and change DNS yourself when you are ready.',
                'Email, DNS records, SSL certificates, FTP accounts and cron jobs are not moved by this version.',
            ],
            'validation_note' => 'Files were compared by checksum after the copy; databases were compared by table count. The validation pass (Validate) adds document root, created files, ownership, database and HTTP checks; PHP, SSL and DNS checks are not run by this version.',
        ];
    }

    /** @return array<string, mixed> */
    private function mustFind(int $id): array
    {
        return $this->find($id) ?? throw new UserFacingException('Migration not found.');
    }
}
