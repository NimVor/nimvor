<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
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
        'Virtualmin / Webmin',
    ];

    /** @var list<array{database: string, user: string, password: string}> credentials of databases created by the last begin() */
    private array $newDatabaseCredentials = [];

    /** Source panels whose BIND zone files the agent can read. */
    public const ZONE_PANELS = ['cpanel', 'directadmin', 'vestacp', 'hestiacp', 'ispconfig', 'nimvor'];

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
        private readonly Connection $db,
        private readonly MailService $mail,
        private readonly DnsService $dns,
        private readonly DatabaseService $databases,
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
     * The login typed in a form, checked: a password or an unencrypted private key.
     *
     * @param array<string, mixed> $in
     * @return array{password: string, private_key: string}
     */
    private function loginFrom(array $in): array
    {
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
        return ['password' => $key !== '' ? '' : $password, 'private_key' => $key];
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
        $parsed = $this->loginFrom($in);
        $password = $parsed['password'];
        $key = $parsed['private_key'];
        $login = $parsed + ['host_key' => (string) $pending['host_key']];
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
        $selection = ['domains' => [], 'databases' => [], 'mail' => []];
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
        $usedNames = [];
        // The NimVor website that receives each source account's files, when there is exactly one: a new database is linked to it.
        $siteOfAccount = [];
        foreach ($selection['domains'] as $sel) {
            [$sa] = array_pad(explode('|', (string) $sel['key'], 2), 2, '');
            $siteOfAccount[$sa][(int) $sel['site_id']] = true;
        }
        foreach ((array) ($in['dbs'] ?? []) as $key) {
            [$acct, $name] = array_pad(explode('|', (string) $key, 2), 2, '');
            $found = null;
            foreach ((array) ($accounts[$acct]['databases'] ?? []) as $d) {
                if (($d['name'] ?? '') === $name) {
                    $found = (array) $d;
                }
            }
            $choice = (string) (((array) ($in['dbmap'] ?? []))[$key] ?? '');
            $did = (int) $choice;
            if ($found === null) {
                $errors['dbs'] = 'A database you selected is not in the discovery. Discover again.';
                continue;
            }
            if ($choice === 'new') {
                // A new NimVor database and user are made when the migration starts; only the name is chosen now.
                $base = trim((string) preg_replace('/[^a-z0-9_]/', '_', strtolower((string) $found['name'])), '_');
                $base = substr($base === '' ? 'db' : $base, 0, 32);
                $dest = $base;
                for ($n = 2; $n < 100 && (isset($usedNames[$dest]) || $this->dbs->nameTaken($this->server->id(), $dest) || in_array($dest, ['mysql', 'sys', 'information_schema', 'performance_schema'], true)); $n++) {
                    $dest = substr($base, 0, 29) . '_' . $n;
                }
                if (isset($usedNames[$dest]) || $this->dbs->nameTaken($this->server->id(), $dest)) {
                    $errors['dbmap[' . $key . ']'] = 'No free NimVor database name was found for ' . $name . '. Create one under Databases and choose it.';
                    continue;
                }
                $usedNames[$dest] = true;
                $sites = array_keys((array) ($siteOfAccount[$acct] ?? []));
                $databases[] = ['source' => (string) $found['name'], 'dest' => $dest, 'size_kb' => (int) ($found['size_kb'] ?? 0)];
                $selection['databases'][] = ['key' => (string) $key, 'db_id' => 0, 'new' => ['name' => $dest, 'site_id' => count($sites) === 1 ? (int) $sites[0] : 0, 'created' => false]];
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
        // A mailbox is copied into the NimVor mailbox with the same address, which must exist already.
        $mail = [];
        $have = [];
        foreach ($this->db->fetchAll('SELECT m.local, d.domain FROM mail_mailboxes m JOIN mail_domains d ON d.id = m.domain_id WHERE d.server_id = ?', [$this->server->id()]) as $r) {
            $have[strtolower($r['local'] . '@' . $r['domain'])] = true;
        }
        foreach ((array) ($in['mail'] ?? []) as $key) {
            [$acct, $addr] = array_pad(explode('|', (string) $key, 2), 2, '');
            $found = null;
            foreach ((array) ($accounts[$acct]['email'] ?? []) as $e) {
                if (($e['address'] ?? '') === $addr) {
                    $found = (array) $e;
                }
            }
            if ($found === null) {
                $errors['mail'] = 'A mailbox you selected is not in the discovery. Discover again.';
            } elseif (!isset($have[strtolower($addr)])) {
                $errors['mail'] = $addr . ' does not exist in NimVor yet. Create the mailbox under Email first, then select it again.';
            } else {
                $mail[] = ['address' => $addr, 'source_maildir' => (string) $found['maildir'], 'size_kb' => (int) ($found['size_kb'] ?? -1)];
                $selection['mail'][] = (string) $key;
            }
        }
        if ($files === [] && $databases === [] && $mail === [] && $errors === []) {
            $errors['domains'] = 'Select at least one website, database or mailbox.';
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
            'mail' => $mail,
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
        $this->createNewDatabases($id, $m);
        $created = (array) $this->agent->call('migrate.create', ['plan' => (array) $m['plan'], 'panel' => $m['panel'], 'source' => 'ssh://' . $m['source_user'] . '@' . $m['source_host'] . ':' . $m['source_port']]);
        $agentId = (string) ($created['id'] ?? '');
        $res = (array) $this->agent->call('migrate.start', ['id' => $agentId, 'source' => $this->source($m)]);
        $job = (string) ($res['job_id'] ?? '');
        $this->repo->update($id, ['agent_id' => $agentId, 'job_id' => $job, 'status' => 'running', 'error' => '']);
        $this->jobs->track($job, 'Migration from ' . $m['source_host'], 'migration', (string) $id, null);
        $this->audit->record('migration.start', 'migration', (string) $id, AuditLogger::SUCCESS, [
            'host' => $m['source_host'], 'websites' => count((array) ($m['plan']['files'] ?? [])), 'databases' => count((array) ($m['plan']['databases'] ?? [])), 'mailboxes' => count((array) ($m['plan']['mail'] ?? [])),
            'allow_overwrite' => !empty($m['plan']['config']['allow_overwrite']),
        ]);
        return $job;
    }

    /**
     * Create the NimVor databases (with a user that has full access, linked to the website) the person asked
     * for with "create a new database". It runs once, just before the migration starts; one that was created
     * on an earlier try is not created again. The new credentials are available from newDatabaseCredentials().
     *
     * @param array<string, mixed> $m
     */
    private function createNewDatabases(int $id, array $m): void
    {
        $this->newDatabaseCredentials = [];
        $selection = (array) $m['selection'];
        $changed = false;
        foreach ((array) ($selection['databases'] ?? []) as $i => $row) {
            $new = $row['new'] ?? null;
            if (!is_array($new) || !empty($new['created'])) {
                continue;
            }
            try {
                $out = $this->databases->create([
                    'name' => (string) $new['name'], 'create_user' => true, 'username' => substr((string) $new['name'], 0, 32),
                    'website_id' => (int) $new['site_id'] ?: null, 'store_password' => false,
                ], ['id' => 0, 'username' => '', 'role_scope' => 'all']);
            } catch (ValidationException $e) {
                throw new UserFacingException('The database ' . $new['name'] . ' could not be created: ' . implode(' ', array_map('strval', $e->errors())) . ' Go back one step and choose another name or an existing database.');
            }
            $selection['databases'][$i]['new']['created'] = true;
            $selection['databases'][$i]['db_id'] = (int) ($out['database_id'] ?? 0);
            $changed = true;
            $this->repo->update($id, ['selection' => $selection]);
            $this->newDatabaseCredentials[] = ['database' => (string) $new['name'], 'user' => (string) ($out['user'] ?? ''), 'password' => (string) ($out['password'] ?? '')];
        }
        if ($changed) {
            $this->audit->record('migration.databases_created', 'migration', (string) $id, AuditLogger::SUCCESS, ['count' => count($this->newDatabaseCredentials)]);
        }
    }

    /** @return list<array{database: string, user: string, password: string}> */
    public function newDatabaseCredentials(): array
    {
        return $this->newDatabaseCredentials;
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
     * Copy again only the website files that changed on the source (for use right before switching DNS).
     * A finished migration no longer holds the login, so it is given again; the SSH key that was confirmed
     * stays pinned. Databases are not synchronised.
     *
     * @param array<string, mixed> $in
     */
    public function sync(int $id, array $in): string
    {
        $this->refresh($id);
        $m = $this->mustFind($id);
        if ($m['agent_id'] === '' || !in_array($m['status'], ['completed', 'failed', 'paused', 'interrupted'], true)) {
            throw new UserFacingException('This migration cannot be synchronised now (it never started, is running, or was cancelled or rolled back).');
        }
        if (empty((array) ($m['plan']['files'] ?? [])) && empty((array) ($m['plan']['mail'] ?? []))) {
            throw new UserFacingException('This migration has no website files or mailboxes to synchronise.');
        }
        $pending = (array) $m['pending_key'];
        if (empty($pending['host_key'])) {
            throw new UserFacingException('The source server\'s SSH key is not on record. Start a new migration.');
        }
        $login = $this->loginFrom($in) + ['host_key' => (string) $pending['host_key']];
        $source = ['panel' => (string) $m['panel'], 'host' => $m['source_host'], 'port' => $m['source_port'], 'user' => $m['source_user']] + $login;
        $res = (array) $this->agent->call('migrate.sync', ['id' => $m['agent_id'], 'source' => $source]);
        $job = (string) ($res['job_id'] ?? '');
        $this->repo->update($id, ['sealed' => $this->crypto->encrypt((string) json_encode($login, JSON_UNESCAPED_SLASHES)), 'job_id' => $job, 'status' => 'running', 'error' => '', 'finished_at' => null]);
        $this->jobs->track($job, 'Synchronise ' . $m['source_host'], 'migration', (string) $id, null);
        $this->audit->record('migration.sync', 'migration', (string) $id, AuditLogger::SUCCESS, ['host' => $m['source_host'], 'websites' => count((array) ($m['plan']['files'] ?? [])), 'mailboxes' => count((array) ($m['plan']['mail'] ?? []))]);
        return $job;
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
                'Mailbox passwords and quotas, address (A/AAAA) records, SSL certificates, FTP accounts and cron jobs are not moved by this version; email messages, forwarders and other DNS records are only moved for the items you selected.',
            ],
            'validation_note' => 'Files were compared by checksum after the copy; databases were compared by table count. The validation pass (Validate) adds document root, created files, ownership, database and HTTP checks; PHP, SSL and DNS checks are not run by this version.',
        ];
    }

    /**
     * Create NimVor aliases for the forwarders the user ticked. An address that already has an
     * alias or a mailbox is left alone, and so is one whose domain is not set up for mail here.
     *
     * @param list<string> $keys "account|source" from the discovery
     * @return array{created: int, skipped: list<string>}
     */
    public function importForwarders(int $id, array $keys): array
    {
        $m = $this->mustFind($id);
        if ($keys === []) {
            throw new ValidationException(['forwarders' => 'Select at least one forwarder.']);
        }
        $found = [];
        foreach ((array) (((array) $m['discovery'])['accounts'] ?? []) as $a) {
            foreach ((array) ($a['forwarders'] ?? []) as $f) {
                $found[$a['id'] . '|' . $f['source']] = (array) $f;
            }
        }
        $created = 0;
        $skipped = [];
        foreach ($keys as $key) {
            $f = $found[(string) $key] ?? null;
            if ($f === null) {
                $skipped[] = (string) $key . ' (not in the discovery)';
                continue;
            }
            [$local, $domainName] = explode('@', (string) $f['source'], 2);
            $domain = $this->db->fetch('SELECT * FROM mail_domains WHERE server_id = ? AND domain = ?', [$this->server->id(), $domainName]);
            if ($domain === null) {
                $skipped[] = $f['source'] . ' (' . $domainName . ' is not set up for email in NimVor yet)';
                continue;
            }
            if ((int) $this->db->scalar('SELECT COUNT(*) FROM mail_aliases WHERE domain_id = ? AND local = ?', [$domain['id'], $local === '*' ? '' : $local]) > 0) {
                $skipped[] = $f['source'] . ' (an alias with this address already exists)';
                continue;
            }
            try {
                $this->mail->saveAlias($domain, ['local' => $local, 'destinations' => implode(',', (array) $f['destinations'])]);
                $created++;
            } catch (ValidationException $e) {
                $skipped[] = $f['source'] . ' (' . implode(' ', array_map('strval', $e->errors())) . ')';
            } catch (AgentException $e) {
                $skipped[] = $f['source'] . ' (the mail server refused it)';
            }
        }
        $this->audit->record('migration.forwarders', 'migration', (string) $id, AuditLogger::SUCCESS, ['created' => $created, 'skipped' => count($skipped)]);
        return ['created' => $created, 'skipped' => $skipped];
    }

    /**
     * Add the DNS records of a cPanel source's zones to the NimVor zones of the same names. Address
     * records (A, AAAA) are left out: they point at the old server and the NimVor zone has its own.
     * SOA and the zone's own name servers are skipped, and records already there are not duplicated.
     * Nothing is changed at the source or at any registrar.
     *
     * @param list<string> $domains
     * @return array{added: int, notes: list<string>}
     */
    public function importDns(int $id, array $domains): array
    {
        $m = $this->mustFind($id);
        if ($domains === []) {
            throw new ValidationException(['dns' => 'Select at least one domain.']);
        }
        if (!in_array($m['panel'], self::ZONE_PANELS, true)) {
            throw new UserFacingException('DNS zones can only be read from a cPanel, DirectAdmin, VestaCP, HestiaCP or ISPConfig source in this version.');
        }
        $known = [];
        foreach ((array) (((array) $m['discovery'])['accounts'] ?? []) as $a) {
            foreach ((array) ($a['domains'] ?? []) as $d) {
                $known[strtolower((string) $d['name'])] = true;
            }
        }
        $source = $this->source($m);
        $added = 0;
        $notes = [];
        foreach ($domains as $name) {
            $name = strtolower((string) $name);
            if (!isset($known[$name])) {
                $notes[] = $name . ' (not in the discovery)';
                continue;
            }
            $zone = $this->db->fetch('SELECT * FROM dns_zones WHERE server_id = ? AND name = ?', [$this->server->id(), $name]);
            if ($zone === null) {
                $notes[] = $name . ' (there is no DNS zone for it in NimVor yet; create the zone first)';
                continue;
            }
            try {
                $res = (array) $this->agent->call('migrate.dns_zone', ['source' => $source, 'domain' => $name], 90);
            } catch (AgentException $e) {
                $notes[] = $name . ' (the zone file could not be read from the source)';
                continue;
            }
            $text = implode("\n", array_filter(explode("\n", (string) ($res['text'] ?? '')), static fn (string $l): bool => preg_match('/\s(A|AAAA)\s+\S/i', $l) !== 1 || preg_match('/\s(MX|TXT|CNAME|SRV|CAA|NS|SOA)\s/i', $l) === 1));
            try {
                $r = $this->dns->import($zone, $text);
            } catch (ValidationException|UserFacingException $e) {
                $notes[] = $name . ' (' . $e->getMessage() . ')';
                continue;
            }
            $added += $r['added'];
            $notes[] = $name . ': ' . $r['added'] . ' added, ' . $r['duplicates'] . ' already there, ' . count($r['skipped']) . ' skipped';
        }
        $this->audit->record('migration.dns_import', 'migration', (string) $id, AuditLogger::SUCCESS, ['records' => $added, 'zones' => count($domains)]);
        return ['added' => $added, 'notes' => $notes];
    }

    /** @return array<string, mixed> */
    /**
     * Create NimVor FTP accounts for the ticked FTP accounts of the source. An account is created only
     * for a source folder that lies inside a website the migration copies (the longest match decides the
     * website), gets a new random password (the old one cannot be read) and is shown once. An account
     * that already exists in NimVor is left alone.
     *
     * @param list<string> $keys "account|login" from the discovery
     * @return array{created: list<array{login: string, password: string}>, skipped: list<string>}
     */
    public function importFtp(int $id, array $keys): array
    {
        $m = $this->mustFind($id);
        if ($keys === []) {
            throw new ValidationException(['ftp' => 'Select at least one FTP account.']);
        }
        $found = [];
        foreach ((array) (((array) $m['discovery'])['accounts'] ?? []) as $a) {
            foreach ((array) ($a['ftp'] ?? []) as $f) {
                $found[$a['id'] . '|' . $f['login']] = (array) $f;
            }
        }
        $files = (array) (((array) $m['plan'])['files'] ?? []);
        if ($files === []) {
            throw new UserFacingException('Choose the websites to copy and save the selection first: an FTP account is created for the website that holds its folder.');
        }
        $created = [];
        $skipped = [];
        foreach ($keys as $key) {
            $f = $found[(string) $key] ?? null;
            if ($f === null) {
                $skipped[] = (string) $key . ' (not in the discovery)';
                continue;
            }
            $home = rtrim((string) $f['home'], '/');
            $best = null;
            foreach ($files as $file) {
                $root = rtrim((string) $file['source_root'], '/');
                if (($home === $root || str_starts_with($home, $root . '/')) && ($best === null || strlen($root) > strlen(rtrim((string) $best['source_root'], '/')))) {
                    $best = (array) $file;
                }
            }
            if ($best === null) {
                $skipped[] = $f['login'] . ' (its folder is not inside a website you are copying)';
                continue;
            }
            $local = strtolower((string) strstr($f['login'] . '@', '@', true));
            if (!preg_match('/^[a-z0-9][a-z0-9._-]{0,30}$/', $local)) {
                $skipped[] = $f['login'] . ' (the name cannot be used in NimVor)';
                continue;
            }
            $login = \NimVor\Http\Controllers\FtpController::login($local, (string) $best['domain']);
            $dir = trim(substr($home, strlen(rtrim((string) $best['source_root'], '/'))), '/');
            try {
                $existing = array_map(static fn ($a): string => (string) ($a['name'] ?? ''), (array) (((array) $this->agent->call('ftp.status', ['user' => (string) $best['dest_user']]))['accounts'] ?? []));
                if (in_array($login, $existing, true)) {
                    $skipped[] = $login . ' (an account with this name already exists)';
                    continue;
                }
                if (count($existing) >= \NimVor\Http\Controllers\FtpController::MAX_ACCOUNTS) {
                    $skipped[] = $login . ' (the website already has the maximum number of FTP accounts)';
                    continue;
                }
                $password = rtrim(strtr(base64_encode(random_bytes(15)), '+/', 'Aa'), '=');
                $this->agent->call('ftp.account_save', ['name' => $login, 'user' => (string) $best['dest_user'], 'dir' => $dir, 'quota_mb' => 0, 'password' => $password]);
            } catch (AgentException $e) {
                $skipped[] = $login . ' (the FTP server refused it: is FTP set up under Server → FTP?)';
                continue;
            }
            $created[] = ['login' => $login, 'password' => $password];
        }
        $this->audit->record('migration.ftp', 'migration', (string) $id, AuditLogger::SUCCESS, ['created' => count($created), 'skipped' => count($skipped)]);
        return ['created' => $created, 'skipped' => $skipped];
    }

    private function mustFind(int $id): array
    {
        return $this->find($id) ?? throw new UserFacingException('Migration not found.');
    }
}
