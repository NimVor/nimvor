<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Repositories\DatabaseRepository;
use NimVor\Repositories\UserRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * Account backups: one file per hosting account holding every website
 * backup (files and databases, always encrypted) and the account's
 * description (profile, plan, websites, databases and their users, DNS
 * zones, cron jobs). The agent writes and checks the file; restoring it
 * recreates the account and its websites and then restores each website
 * backup, also after the account was terminated or on another server
 * (which needs the backup key of the server that made it).
 *
 * Not in an account backup: email (mailboxes and messages), SSL
 * certificates (issued again by AutoSSL) and FTP accounts.
 */
final class AccountBackupService
{
    public const FORMAT = 1;

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly WebsiteRepository $sites,
        private readonly UserRepository $users,
        private readonly BackupService $backups,
        private readonly WebsiteService $websites,
        private readonly AccountService $accounts,
        private readonly AccountOptions $options,
        private readonly DatabaseService $databases,
        private readonly DatabaseRepository $dbRepo,
        private readonly DnsService $dns,
        private readonly CronService $cron,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * Hosting accounts that can be backed up (own-scope users).
     *
     * @return list<array<string, mixed>>
     */
    public function accounts(): array
    {
        return $this->db->fetchAll(
            "SELECT u.id, u.username, u.email, (SELECT COUNT(*) FROM websites w WHERE w.owner_id = u.id AND w.server_id = ?) AS websites
             FROM users u JOIN roles r ON r.id = u.role_id WHERE r.scope = 'own' ORDER BY u.username",
            [$this->server->id()]
        );
    }

    /**
     * The description of an account written into its backup.
     *
     * @return array<string, mixed>
     */
    public function describe(int $userId): array
    {
        $u = $this->db->fetch(
            'SELECT u.*, r.name AS role_name, r.scope AS role_scope, p.name AS plan_name, par.username AS parent_username
             FROM users u JOIN roles r ON r.id = u.role_id LEFT JOIN plans p ON p.id = u.plan_id LEFT JOIN users par ON par.id = u.parent_id WHERE u.id = ?',
            [$userId]
        ) ?? throw new UserFacingException('The account no longer exists.');
        if ($u['role_scope'] !== 'own') {
            throw new UserFacingException('Only hosting accounts (customers and resellers) have account backups.');
        }
        $serverId = $this->server->id();
        $websites = [];
        $byId = [];
        foreach ($this->db->fetchAll('SELECT id, parent_id FROM websites WHERE server_id = ? AND owner_id = ? ORDER BY parent_id IS NOT NULL, id', [$serverId, $userId]) as $row) {
            $site = $this->sites->find($serverId, (int) $row['id'], null);
            if ($site === null) {
                continue;
            }
            $byId[(int) $site['id']] = $site;
        }
        foreach ($byId as $site) {
            $parent = $site['parent_id'] !== null ? ($byId[(int) $site['parent_id']] ?? null) : null;
            $type = 'main';
            if ($parent !== null) {
                $type = str_ends_with((string) $site['domain'], '.' . $parent['domain']) ? 'subdomain' : 'addon';
            }
            $websites[] = [
                'domain' => (string) $site['domain'],
                'type' => $type,
                'parent' => $parent !== null ? (string) $parent['domain'] : null,
                'kind' => (string) $site['kind'],
                'php_version' => (string) $site['php_version'],
                'system_user' => (string) $site['system_user'],
                'aliases' => array_values((array) $site['aliases']),
                'proxy_pass' => (string) ($site['proxy_pass'] ?? ''),
                'databases' => $this->backups->siteDatabases($site),
            ];
        }
        $dbs = [];
        foreach ($this->db->fetchAll('SELECT d.id, d.name, w.domain FROM databases d LEFT JOIN websites w ON w.id = d.website_id WHERE d.server_id = ? AND (d.owner_id = ? OR w.owner_id = ?) ORDER BY d.name', [$serverId, $userId, $userId]) as $d) {
            $dbs[(int) $d['id']] = ['name' => (string) $d['name'], 'website' => $d['domain'] !== null ? (string) $d['domain'] : null];
        }
        $dbUsers = [];
        foreach ($this->db->fetchAll('SELECT id, username, host FROM database_users WHERE server_id = ? AND owner_id = ? ORDER BY username', [$serverId, $userId]) as $du) {
            $grants = [];
            foreach ($this->dbRepo->grantsOf((int) $du['id']) as $g) {
                $name = $dbs[(int) ($g['database_id'] ?? 0)]['name'] ?? null;
                if ($name !== null) {
                    $grants[] = ['database' => $name, 'privileges' => (string) ($g['privileges'] ?? 'all')];
                }
            }
            $dbUsers[] = ['name' => (string) $du['username'], 'host' => (string) $du['host'], 'grants' => $grants];
        }
        $zones = [];
        foreach ($this->db->fetchAll('SELECT z.*, w.domain AS website FROM dns_zones z LEFT JOIN websites w ON w.id = z.website_id WHERE z.server_id = ? AND z.owner_id = ? ORDER BY z.name', [$serverId, $userId]) as $z) {
            try {
                $text = $this->dns->export($z);
            } catch (\Throwable) {
                $text = '';
            }
            $zones[] = ['name' => (string) $z['name'], 'website' => $z['website'] !== null ? (string) $z['website'] : null, 'zonefile' => $text];
        }
        $cron = [];
        foreach ($byId as $site) {
            foreach ($this->cron->jobs($site) as $j) {
                $cron[] = ['website' => (string) $site['domain'], 'schedule' => (string) $j['schedule'], 'command' => (string) $j['command'], 'description' => (string) ($j['description'] ?? ''), 'enabled' => (int) ($j['enabled'] ?? 1) === 1];
            }
        }
        return [
            'format' => self::FORMAT,
            'made_at' => gmdate('c'),
            'user' => [
                'username' => (string) $u['username'], 'email' => (string) $u['email'], 'display_name' => (string) ($u['display_name'] ?? ''),
                'role' => (string) $u['role_name'], 'plan' => $u['plan_name'] !== null ? (string) $u['plan_name'] : null,
                'parent' => $u['parent_username'] !== null ? (string) $u['parent_username'] : null, 'options' => $this->options->get($userId),
            ],
            'websites' => $websites,
            'databases' => array_values($dbs),
            'database_users' => $dbUsers,
            'dns_zones' => $zones,
            'cron_jobs' => $cron,
        ];
    }

    /** Start an account backup; returns the agent job id. */
    public function create(int $userId, string $label, ?int $actorId): string
    {
        $meta = $this->describe($userId);
        $sites = [];
        foreach ($meta['websites'] as $w) {
            $site = $this->sites->findByDomain($this->server->id(), $w['domain']);
            if ($site === null) {
                continue;
            }
            $sites[] = [
                'domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'files' => true,
                'databases' => $w['databases'], 'label' => 'Account backup ' . $meta['user']['username'],
                'meta' => ['website' => WebsiteService::spec($site, false), 'panel_version' => 1],
            ];
        }
        $res = (array) $this->agent->call('backup.account', [
            'username' => $meta['user']['username'], 'label' => mb_substr(trim($label), 0, 100), 'meta' => $meta, 'sites' => $sites,
        ]);
        $this->audit->record('backup.account', 'user', (string) $userId, AuditLogger::SUCCESS, ['username' => $meta['user']['username'], 'websites' => array_column($meta['websites'], 'domain')], $actorId);
        return (string) $res['job_id'];
    }

    /**
     * Account backup files on the server.
     *
     * @return list<array<string, mixed>>
     */
    public function files(): array
    {
        try {
            $res = (array) $this->agent->call('backup.account_files', [], 60);
        } catch (AgentException) {
            return [];
        }
        return array_map(static fn ($f): array => (array) json_decode((string) json_encode($f), true), (array) ($res['files'] ?? []));
    }

    /**
     * The description of an account backup file and what restoring it does
     * here (accounts, websites and databases that exist).
     *
     * @return array{manifest: array<string, mixed>, plan: array<string, mixed>}
     */
    public function inspect(string $path): array
    {
        $res = (array) json_decode((string) json_encode($this->agent->call('backup.account_inspect', ['path' => $path], 60)), true);
        $m = (array) ($res['manifest'] ?? []);
        $meta = (array) ($m['meta'] ?? []);
        if ((int) ($meta['format'] ?? 0) !== self::FORMAT || !is_array($meta['user'] ?? null)) {
            throw new UserFacingException('This account backup was made by a NimVor version this panel cannot read.');
        }
        return ['manifest' => $m, 'plan' => $this->plan($meta)];
    }

    /**
     * What a restore would do with an account description.
     *
     * @param array<string, mixed> $meta
     * @return array<string, mixed>
     */
    public function plan(array $meta): array
    {
        $serverId = $this->server->id();
        $username = (string) ($meta['user']['username'] ?? '');
        $existing = $this->db->fetch('SELECT u.id, r.scope FROM users u JOIN roles r ON r.id = u.role_id WHERE u.username = ?', [$username]);
        $account = $existing === null ? 'create' : ($existing['scope'] === 'own' ? 'existing' : 'conflict');
        $sites = [];
        foreach ((array) ($meta['websites'] ?? []) as $w) {
            $row = $this->db->fetch('SELECT id, owner_id FROM websites WHERE server_id = ? AND domain = ?', [$serverId, (string) $w['domain']]);
            $state = 'create';
            if ($row !== null) {
                $state = $existing !== null && (int) ($row['owner_id'] ?? 0) === (int) $existing['id'] ? 'existing' : 'conflict';
            }
            $sites[(string) $w['domain']] = $state;
        }
        $dbs = [];
        foreach ((array) ($meta['databases'] ?? []) as $d) {
            $dbs[(string) $d['name']] = $this->dbRepo->nameTaken($serverId, (string) $d['name']) ? 'existing' : 'create';
        }
        $plan = (string) ($meta['user']['plan'] ?? '');
        return [
            'account' => $account,
            'websites' => $sites,
            'databases' => $dbs,
            'plan_found' => $plan === '' || $this->db->scalar('SELECT COUNT(*) FROM plans WHERE name = ? AND owner_id IS NULL', [$plan]) > 0,
        ];
    }

    /**
     * Start restoring an account backup file: the agent checks it and puts
     * its website backups in place (a job); returns the restore's id.
     */
    public function start(string $path, ?int $actorId): int
    {
        $insp = $this->inspect($path);
        if ($insp['plan']['account'] === 'conflict') {
            throw new UserFacingException('A staff user with the account\'s name exists.', 'The account cannot be restored under that name.', 'Rename or remove that user first.');
        }
        $res = (array) $this->agent->call('backup.account_import', ['path' => $path]);
        $now = gmdate('c');
        $m = $insp['manifest'];
        $this->db->run(
            'INSERT INTO account_restores (server_id, path, username, manifest, status, job_id, user_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), $path, (string) $m['username'], json_encode($m, JSON_UNESCAPED_SLASHES), 'importing', (string) $res['job_id'], $actorId, $now, $now]
        );
        $id = $this->db->lastInsertId();
        $this->audit->record('backup.account_restore_start', 'account_restore', (string) $id, AuditLogger::SUCCESS, ['username' => $m['username'], 'path' => $path], $actorId);
        return $id;
    }

    /** @return array<string, mixed>|null */
    public function restoreRecord(int $id): ?array
    {
        $r = $this->db->fetch('SELECT * FROM account_restores WHERE id = ? AND server_id = ?', [$id, $this->server->id()]);
        if ($r === null) {
            return null;
        }
        $r['manifest'] = (array) json_decode((string) $r['manifest'], true);
        $r['report'] = (array) json_decode((string) $r['report'], true);
        return $r;
    }

    /**
     * The state of the import job of a restore ("running", "succeeded",
     * "failed"), recorded on the restore.
     *
     * @param array<string, mixed> $r
     */
    public function importState(array $r): string
    {
        if ($r['status'] !== 'importing') {
            return $r['status'] === 'failed' ? 'failed' : 'succeeded';
        }
        try {
            $job = (array) $this->agent->call('jobs.get', ['id' => $r['job_id']]);
        } catch (AgentException $e) {
            if ($e->errorCode() !== 'not_found') {
                return 'running';
            }
            $job = ['status' => 'failed', 'error' => 'The import job was lost (agent restarted). Start the restore again.'];
        }
        $status = (string) ($job['status'] ?? 'running');
        if ($status === 'failed') {
            $this->finishRecord((int) $r['id'], 'failed', [['level' => 'error', 'text' => 'The file could not be imported: ' . mb_substr((string) ($job['error'] ?? ''), 0, 500)]]);
            return 'failed';
        }
        if ($status === 'succeeded') {
            $this->db->run("UPDATE account_restores SET status = 'imported', updated_at = ? WHERE id = ?", [gmdate('c'), (int) $r['id']]);
            return 'succeeded';
        }
        return 'running';
    }

    /** @param list<array{level: string, text: string}> $report */
    private function finishRecord(int $id, string $status, array $report): void
    {
        $this->db->run('UPDATE account_restores SET status = ?, report = ?, updated_at = ? WHERE id = ?', [$status, json_encode($report, JSON_UNESCAPED_SLASHES), gmdate('c'), $id]);
    }

    /**
     * Recreate the account from an imported account backup and restore
     * its websites. Returns the report, the restore jobs started and the
     * passwords made (shown once, never stored).
     *
     * @param array<string, mixed> $actor an administrator
     * @return array{report: list<array{level: string, text: string}>, jobs: list<array{job: string, domain: string, website_id: int}>, secrets: list<array{what: string, name: string, password: string}>}
     */
    public function recreate(int $restoreId, array $actor): array
    {
        $r = $this->restoreRecord($restoreId) ?? throw new UserFacingException('The restore no longer exists.');
        if ($r['status'] !== 'imported') {
            throw new UserFacingException($r['status'] === 'done' ? 'This account was already restored.' : 'The file is not imported yet.');
        }
        $this->db->run("UPDATE account_restores SET status = 'restoring', updated_at = ? WHERE id = ?", [gmdate('c'), $restoreId]);
        $meta = (array) ($r['manifest']['meta'] ?? []);
        $sitesInFile = [];
        foreach ((array) ($r['manifest']['sites'] ?? []) as $s) {
            $sitesInFile[(string) $s['domain']] = (string) $s['id'];
        }
        $report = [];
        $jobs = [];
        $secrets = [];
        $note = static function (string $level, string $text) use (&$report): void {
            $report[] = ['level' => $level, 'text' => $text];
        };
        $serverId = $this->server->id();
        $u = (array) ($meta['user'] ?? []);
        $username = (string) ($u['username'] ?? '');

        // 1. The account.
        $existing = $this->db->fetch('SELECT u.id, r.scope FROM users u JOIN roles r ON r.id = u.role_id WHERE u.username = ?', [$username]);
        if ($existing !== null && $existing['scope'] !== 'own') {
            $this->finishRecord($restoreId, 'failed', [['level' => 'error', 'text' => 'A staff user named ' . $username . ' exists; the account was not restored.']]);
            throw new UserFacingException('A staff user named ' . $username . ' exists.');
        }
        $planName = (string) ($u['plan'] ?? '');
        $planId = $planName !== '' ? $this->db->scalar('SELECT id FROM plans WHERE name = ? AND owner_id IS NULL', [$planName]) : null;
        if ($planName !== '' && $planId === null) {
            $note('warning', 'The plan "' . $planName . '" does not exist here: the account has no plan. Assign one under Users.');
        }
        if ($existing === null) {
            $email = (string) ($u['email'] ?? '');
            if ($email === '' || $this->users->emailExists($email)) {
                // An address is required and unique: a placeholder to change.
                $placeholder = $username . '@restored.invalid';
                $note('warning', ($email === '' ? 'The account had no email address' : 'The email address ' . $email . ' belongs to another user here') . ': the account has ' . $placeholder . ' until you change it.');
                $email = $placeholder;
            }
            try {
                $made = $this->accounts->create([
                    'username' => $username, 'email' => $email, 'display_name' => (string) ($u['display_name'] ?? ''), 'generate' => true,
                    'reseller' => ($u['role'] ?? '') === 'reseller', 'plan_id' => $planId !== null ? (string) $planId : '',
                ] + array_map(static fn ($v) => is_bool($v) ? ($v ? '1' : '') : $v, (array) ($u['options'] ?? [])), $actor);
            } catch (ValidationException $e) {
                $this->finishRecord($restoreId, 'failed', [['level' => 'error', 'text' => 'The account could not be created: ' . implode(' ', $e->errors())]]);
                throw $e;
            }
            $accountId = (int) $made['id'];
            $secrets[] = ['what' => 'Panel password of the account', 'name' => $username, 'password' => (string) $made['password']];
            $note('ok', 'Account ' . $username . ' created' . ($planId !== null ? ' with the plan ' . $planName : '') . '.');
        } else {
            $accountId = (int) $existing['id'];
            $note('ok', 'Account ' . $username . ' exists: its websites are restored into it.');
        }
        if (!empty($u['parent'])) {
            $parent = $this->db->scalar("SELECT u.id FROM users u JOIN roles r ON r.id = u.role_id WHERE u.username = ? AND r.name = 'reseller'", [(string) $u['parent']]);
            if ($parent !== null && $existing === null) {
                $this->users->setOwnership($accountId, (int) $parent, $planId !== null ? (int) $planId : null);
                $note('ok', 'Placed under the reseller ' . $u['parent'] . '.');
            } elseif ($parent === null) {
                $note('warning', 'The reseller ' . $u['parent'] . ' does not exist here: the account is managed by administrators.');
            }
        }
        $account = (array) $this->users->find($accountId);

        // 2. Websites: main websites first, then subdomains and addon domains.
        $siteIds = [];
        $websites = (array) ($meta['websites'] ?? []);
        usort($websites, static fn (array $a, array $b): int => (($a['type'] ?? 'main') !== 'main') <=> (($b['type'] ?? 'main') !== 'main'));
        foreach ($websites as $w) {
            $domain = (string) $w['domain'];
            $row = $this->db->fetch('SELECT id, owner_id FROM websites WHERE server_id = ? AND domain = ?', [$serverId, $domain]);
            if ($row !== null) {
                if ((int) ($row['owner_id'] ?? 0) !== $accountId) {
                    $note('error', $domain . ' exists here and belongs to another account: not restored.');
                    continue;
                }
                $siteIds[$domain] = (int) $row['id'];
                $note('ok', $domain . ' exists in the account: its files and databases are restored into it.');
                continue;
            }
            try {
                if (($w['type'] ?? 'main') !== 'main' && isset($siteIds[(string) $w['parent']])) {
                    $parent = $this->sites->find($serverId, $siteIds[(string) $w['parent']], null) ?? throw new UserFacingException('parent missing');
                    $input = ($w['type'] === 'subdomain')
                        ? ['subdomain' => substr($domain, 0, -strlen('.' . $parent['domain']))]
                        : ['domain' => $domain, 'www' => in_array('www.' . $domain, (array) $w['aliases'], true)];
                    $siteIds[$domain] = $this->websites->createChild($parent, (string) $w['type'], $input, $account)['id'];
                } else {
                    $systemUser = (string) ($w['system_user'] ?? $username);
                    $siteIds[$domain] = $this->websites->create([
                        'domain' => $domain,
                        'kind' => (string) ($w['kind'] ?? 'php'),
                        'php_version' => (string) ($w['php_version'] ?? ''),
                        'proxy_pass' => (string) ($w['proxy_pass'] ?? ''),
                        'system_user' => $systemUser,
                        'new_system_user' => $this->sites->usingSystemUser($serverId, $systemUser) === [],
                        'aliases' => implode(' ', array_map('strval', (array) ($w['aliases'] ?? []))),
                    ], $account);
                }
                $note('ok', 'Website ' . $domain . ' created.');
            } catch (ValidationException $e) {
                $note('error', 'Website ' . $domain . ' could not be created: ' . implode(' ', $e->errors()));
            } catch (UserFacingException | AgentException $e) {
                $note('error', 'Website ' . $domain . ' could not be created: ' . $e->getMessage());
            }
        }

        // 3. Databases and their users.
        $dbIds = [];
        foreach ((array) ($meta['databases'] ?? []) as $d) {
            $name = (string) $d['name'];
            $existingDb = $this->db->fetch('SELECT id, owner_id FROM databases WHERE server_id = ? AND name = ?', [$serverId, $name]);
            if ($existingDb !== null) {
                if ((int) ($existingDb['owner_id'] ?? 0) === $accountId) {
                    $dbIds[$name] = (int) $existingDb['id'];
                    $note('ok', 'Database ' . $name . ' exists in the account: its contents are restored into it.');
                } else {
                    $note('error', 'Database ' . $name . ' exists here and belongs to another account: not restored.');
                }
                continue;
            }
            $websiteId = $d['website'] !== null ? ($siteIds[(string) $d['website']] ?? null) : null;
            try {
                $res = $this->databases->create(['name' => $name, 'website_id' => $websiteId], $actor);
                $dbIds[$name] = (int) $res['database_id'];
                if ($websiteId === null) {
                    $this->db->run('UPDATE databases SET owner_id = ? WHERE id = ?', [$accountId, $dbIds[$name]]);
                }
                $note('ok', 'Database ' . $name . ' created.');
            } catch (ValidationException $e) {
                $note('error', 'Database ' . $name . ' could not be created: ' . implode(' ', $e->errors()));
            } catch (UserFacingException | AgentException $e) {
                $note('error', 'Database ' . $name . ' could not be created: ' . $e->getMessage());
            }
        }
        foreach ((array) ($meta['database_users'] ?? []) as $du) {
            $name = (string) $du['name'];
            $host = (string) ($du['host'] ?? 'localhost');
            if ($this->dbRepo->userTaken($serverId, $name, $host)) {
                $note('warning', 'Database user ' . $name . ' exists here: kept as it is.');
                continue;
            }
            try {
                $made = $this->databases->createUser(['username' => $name, 'host' => $host], $actor);
                $uid = (int) $this->db->scalar('SELECT id FROM database_users WHERE server_id = ? AND username = ? AND host = ?', [$serverId, $made['user'], $host]);
                $this->db->run('UPDATE database_users SET owner_id = ? WHERE id = ?', [$accountId, $uid]);
                $secrets[] = ['what' => 'Database user (new password: update the website\'s configuration)', 'name' => $made['user'], 'password' => $made['password']];
                $dbUser = $this->dbRepo->user($serverId, $uid, null);
                foreach ((array) ($du['grants'] ?? []) as $g) {
                    if (isset($dbIds[(string) $g['database']]) && $dbUser !== null) {
                        $database = $this->dbRepo->database($serverId, $dbIds[(string) $g['database']], null);
                        if ($database !== null) {
                            $this->databases->grant($database, $dbUser, isset(DatabaseService::PRIVILEGES[(string) $g['privileges']]) ? (string) $g['privileges'] : 'all');
                        }
                    }
                }
                $note('ok', 'Database user ' . $made['user'] . ' created with a new password.');
            } catch (ValidationException $e) {
                $note('error', 'Database user ' . $name . ' could not be created: ' . implode(' ', $e->errors()));
            } catch (UserFacingException | AgentException $e) {
                $note('error', 'Database user ' . $name . ' could not be created: ' . $e->getMessage());
            }
        }

        // 4. Restore each website backup (files and databases).
        $this->backups->refresh();
        foreach ($websites as $w) {
            $domain = (string) $w['domain'];
            if (!isset($siteIds[$domain], $sitesInFile[$domain])) {
                continue;
            }
            $site = $this->sites->find($serverId, $siteIds[$domain], null);
            if ($site === null) {
                continue;
            }
            try {
                $b = $this->recordImported($site, $sitesInFile[$domain], $actor);
                $dbs = [];
                foreach ($b['parts'] as $p) {
                    if (($p['kind'] ?? '') === 'database' && isset($dbIds[(string) $p['database']])) {
                        $dbs[] = (string) $p['database'];
                    }
                }
                $job = $this->backups->restore($site, $b, true, $dbs, (string) $site['domain'], (int) $actor['id']);
                $jobs[] = ['job' => $job, 'domain' => $domain, 'website_id' => (int) $site['id']];
                $note('ok', 'Restoring the files' . ($dbs !== [] ? ' and ' . count($dbs) . ' database(s)' : '') . ' of ' . $domain . '.');
            } catch (UserFacingException | AgentException $e) {
                $note('error', 'The backup of ' . $domain . ' could not be restored: ' . $e->getMessage());
            }
        }

        // 5. DNS zones (records not in the zone yet are added) and cron jobs.
        foreach ((array) ($meta['dns_zones'] ?? []) as $z) {
            $name = (string) $z['name'];
            try {
                $zone = $this->db->fetch('SELECT * FROM dns_zones WHERE server_id = ? AND name = ?', [$serverId, $name]);
                if ($zone !== null && (int) ($zone['owner_id'] ?? 0) !== $accountId) {
                    $note('error', 'DNS zone ' . $name . ' exists here and belongs to another account: not restored.');
                    continue;
                }
                if ($zone === null) {
                    $websiteId = $z['website'] !== null ? ($siteIds[(string) $z['website']] ?? null) : null;
                    $zid = $this->dns->create(['name' => $name, 'website_id' => $websiteId], $actor);
                    $this->db->run('UPDATE dns_zones SET owner_id = ? WHERE id = ?', [$accountId, $zid]);
                    $zone = $this->db->fetch('SELECT * FROM dns_zones WHERE id = ?', [$zid]);
                }
                if ($zone !== null && trim((string) ($z['zonefile'] ?? '')) !== '') {
                    $res = $this->dns->import($zone, (string) $z['zonefile']);
                    $note('ok', 'DNS zone ' . $name . ': ' . $res['added'] . ' record(s) added.');
                }
            } catch (ValidationException $e) {
                $note('error', 'DNS zone ' . $name . ': ' . implode(' ', $e->errors()));
            } catch (UserFacingException | AgentException $e) {
                $note('error', 'DNS zone ' . $name . ': ' . $e->getMessage());
            }
        }
        $cronAdded = 0;
        foreach ((array) ($meta['cron_jobs'] ?? []) as $c) {
            $domain = (string) $c['website'];
            $site = isset($siteIds[$domain]) ? $this->sites->find($serverId, $siteIds[$domain], null) : null;
            if ($site === null) {
                continue;
            }
            $same = array_filter($this->cron->jobs($site), static fn (array $j): bool => $j['schedule'] === $c['schedule'] && $j['command'] === $c['command']);
            if ($same !== []) {
                continue;
            }
            try {
                $this->cron->save($site, null, ['schedule' => $c['schedule'], 'command' => $c['command'], 'description' => $c['description'] ?? '', 'enabled' => !empty($c['enabled']) ? '1' : '0']);
                $cronAdded++;
            } catch (ValidationException $e) {
                $note('error', 'Cron job "' . $c['command'] . '" on ' . $domain . ': ' . implode(' ', $e->errors()));
            } catch (UserFacingException | AgentException $e) {
                $note('error', 'Cron job "' . $c['command'] . '" on ' . $domain . ': ' . $e->getMessage());
            }
        }
        if ($cronAdded > 0) {
            $note('ok', $cronAdded . ' cron job(s) added.');
        }
        $note('info', 'Not in account backups: email (mailboxes and messages), SSL certificates (AutoSSL issues them again once the domains point here) and FTP accounts.');

        $failed = count(array_filter($report, static fn (array $l): bool => $l['level'] === 'error'));
        $this->finishRecord($restoreId, 'done', $report);
        $this->audit->record('backup.account_restore', 'user', (string) $accountId, $failed === 0 ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['username' => $username, 'websites' => array_keys($siteIds), 'problems' => $failed], (int) $actor['id']);
        return ['report' => $report, 'jobs' => $jobs, 'secrets' => $secrets];
    }

    /**
     * The backups row of a website backup imported from an account backup
     * (created when the agent's list has it but the panel does not yet).
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $actor
     * @return array<string, mixed>
     */
    private function recordImported(array $site, string $backupId, array $actor): array
    {
        $row = $this->db->fetch('SELECT id FROM backups WHERE server_id = ? AND backup_id = ?', [$this->server->id(), $backupId]);
        if ($row === null) {
            $m = (array) json_decode((string) json_encode($this->agent->call('backup.inspect', ['domain' => $site['domain'], 'id' => $backupId])), true);
            unset($m['meta']);
            $created = (string) ($m['created_at'] ?? gmdate('c'));
            $this->db->run(
                "INSERT INTO backups (server_id, website_id, kind, domain, backup_id, label, manifest, size, encrypted, local, status, user_id, created_at, finished_at) VALUES (?, ?, 'site', ?, ?, ?, ?, ?, 1, 1, 'completed', ?, ?, ?)",
                [$this->server->id(), (int) $site['id'], (string) $site['domain'], $backupId, mb_substr((string) ($m['label'] ?? 'Account backup'), 0, 100), json_encode($m, JSON_UNESCAPED_SLASHES), (int) ($m['size'] ?? 0), (int) $actor['id'], $created, $created]
            );
            $id = $this->db->lastInsertId();
        } else {
            $id = (int) $row['id'];
            // A backup kept from before the account was terminated.
            $this->db->run('UPDATE backups SET website_id = ? WHERE id = ? AND website_id IS NULL', [(int) $site['id'], $id]);
        }
        return $this->backups->find($site, $id) ?? throw new UserFacingException('The imported backup of ' . $site['domain'] . ' was not found.');
    }

    /** Delete an account backup file made or uploaded here. */
    public function delete(string $path, ?int $actorId): void
    {
        $this->agent->call('backup.account_delete', ['path' => $path]);
        $this->audit->record('backup.account_delete', 'backup', basename($path), AuditLogger::SUCCESS, ['path' => $path], $actorId);
    }

    /**
     * A chunk of an account backup file.
     *
     * @return array{data: string, size: int, eof: bool}
     */
    public function chunk(string $path, int $offset): array
    {
        $r = (array) $this->agent->call('backup.account_download', ['path' => $path, 'offset' => $offset], 120);
        return ['data' => (string) ($r['data'] ?? ''), 'size' => (int) ($r['size'] ?? 0), 'eof' => !empty($r['eof'])];
    }

    /**
     * Store a chunk of an uploaded account backup file.
     *
     * @return array{size: int, path: string}
     */
    public function upload(string $name, int $offset, string $bytes, bool $final): array
    {
        $r = (array) $this->agent->call('backup.account_upload', ['name' => $name, 'offset' => $offset, 'data' => base64_encode($bytes), 'final' => $final], 120);
        return ['size' => (int) ($r['size'] ?? 0), 'path' => (string) ($r['path'] ?? '')];
    }
}
