<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Agent\AgentHttpResponse;
use NimVor\Database\Connection;
use NimVor\Security\Crypto;
use NimVor\Tests\Support\HttpTestCase;

/**
 * The migration wizard: connect to a source with a confirmed SSH key, discover,
 * choose and map, check, run, and report. The agent does the work; these tests
 * use its real answers' shape and check what the panel keeps, shows and sends.
 */
final class MigrationsTest extends HttpTestCase
{
    private Connection $db;
    private int $siteId;
    private int $dbId;
    private const PW = 'Source-Server-Pa55word';
    private const KEY = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl';

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->db = $this->app->container->get(Connection::class);
        $now = gmdate('c');
        $this->db->run("INSERT INTO websites (server_id, owner_id, domain, system_user, root, document_root, kind, php_version, created_at, updated_at) VALUES (1, NULL, 'new.example.com', 'newsite', '/home/nimvor/new.example.com', '/home/nimvor/new.example.com/public_html', 'php', '8.3', ?, ?)", [$now, $now]);
        $this->siteId = $this->db->lastInsertId();
        $this->db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, ?, NULL, 'newsite_wp', ?)", [$this->siteId, $now]);
        $this->dbId = $this->db->lastInsertId();
        $this->agent->succeed('migrate.panels', ['panels' => [
            ['panel' => 'cpanel', 'name' => 'cPanel / WHM', 'support' => 'experimental'],
            ['panel' => 'plesk', 'name' => 'Plesk (Linux)', 'support' => 'experimental'],
            ['panel' => 'directadmin', 'name' => 'DirectAdmin', 'support' => 'experimental'],
            ['panel' => 'fastpanel', 'name' => 'FASTPANEL', 'support' => 'experimental'],
            ['panel' => 'vestacp', 'name' => 'VestaCP', 'support' => 'experimental'],
            ['panel' => 'hestiacp', 'name' => 'HestiaCP', 'support' => 'experimental'],
            ['panel' => 'cyberpanel', 'name' => 'CyberPanel', 'support' => 'experimental'],
            ['panel' => 'cloudpanel', 'name' => 'CloudPanel', 'support' => 'experimental'],
            ['panel' => 'aapanel', 'name' => 'aaPanel', 'support' => 'experimental'],
            ['panel' => 'ispconfig', 'name' => 'ISPConfig', 'support' => 'experimental'],
            ['panel' => 'interworx', 'name' => 'InterWorx', 'support' => 'experimental'],
            ['panel' => 'froxlor', 'name' => 'Froxlor', 'support' => 'experimental'],
            ['panel' => 'enhance', 'name' => 'Enhance (limited)', 'support' => 'experimental'],
            ['panel' => 'linux', 'name' => 'Custom Linux server (no panel)', 'support' => 'experimental'],
        ]]);
    }

    private function discovery(): array
    {
        return [
            'panel' => ['panel' => 'linux', 'name' => 'Custom Linux server', 'version' => 'Ubuntu 22.04', 'support' => 'experimental'],
            'server' => ['os' => 'Ubuntu 22.04', 'root' => true, 'rsync' => true, 'mysqldump' => true, 'php' => ['8.1'], 'engine' => 'mysql', 'mysql' => '8.0.33'],
            'accounts' => [['id' => 'alice', 'name' => 'alice', 'home' => '/home/alice',
                'domains' => [['name' => 'shop.example.org', 'kind' => 'main', 'doc_root' => '/home/alice/public_html', 'size_kb' => 2048]],
                'databases' => [['name' => 'alice_wp', 'engine' => 'mysql', 'size_kb' => 512]], 'email' => [['address' => 'info@shop.example.org', 'maildir' => '/home/alice/mail/x']], 'ftp' => [['login' => 'staging@shop.example.org', 'home' => '/home/alice/public_html/staging'], ['login' => 'elsewhere', 'home' => '/home/alice/other'], ['login' => 'Bad Name', 'home' => '/home/alice/public_html']], 'forwarders' => [['source' => 'sales@shop.example.org', 'destinations' => ['boss@gmail.com', 'ops@example.org']], ['source' => 'old@other.example', 'destinations' => ['x@y.com']]], 'cron' => []]],
            'warnings' => [],
            'not_migrated' => ['DNS zones and records', 'SSL certificates'],
        ];
    }

    private function job(string $status, array $result = [], string $error = ''): AgentHttpResponse
    {
        return new AgentHttpResponse(200, (string) json_encode(['success' => true, 'action' => 'jobs.get', 'message' => 'OK', 'data' => ['id' => 'x', 'status' => $status, 'result' => $result, 'error' => $error], 'request_id' => 't']));
    }

    /** A migration brought to a given step through the real requests. */
    private function reach(string $step): int
    {
        $this->login('admin');
        $this->post('/migrations', ['panel' => 'linux']);
        $id = (int) $this->db->scalar('SELECT MAX(id) FROM site_migrations');
        if ($step === 'created') {
            return $id;
        }
        $this->agent->succeed('migrate.host_key', ['host' => 'old.example.com', 'port' => 22, 'host_key' => self::KEY, 'key_type' => 'ssh-ed25519', 'fingerprint' => 'SHA256:abc']);
        $this->post("/migrations/{$id}/probe", ['host' => 'old.example.com', 'port' => '', 'user' => 'root']);
        $this->agent->succeed('migrate.test', ['server' => $this->discovery()['server'], 'panel' => $this->discovery()['panel']]);
        $this->post("/migrations/{$id}/connect", ['password' => self::PW]);
        if ($step === 'connected') {
            return $id;
        }
        $this->agent->succeed('migrate.discover', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->post("/migrations/{$id}/discover");
        $this->agent->responses['/v1/actions/jobs.get'] = $this->job('succeeded', $this->discovery());
        $this->request('GET', "/migrations/{$id}");
        if ($step === 'discovered') {
            return $id;
        }
        $this->post("/migrations/{$id}/select", ['domains' => ['alice|shop.example.org'], 'site' => ['alice|shop.example.org' => (string) $this->siteId], 'dbs' => ['alice|alice_wp'], 'dbmap' => ['alice|alice_wp' => (string) $this->dbId], 'workers' => '3', 'excludes' => "cache/*\n*.log", 'compress' => '1']);
        if ($step === 'selected') {
            return $id;
        }
        $this->agent->succeed('migrate.assess', ['findings' => [['severity' => 'warning', 'code' => 'php_version', 'message' => 'PHP 8.1 is not installed here.', 'fix' => 'Install it.'], ['severity' => 'info', 'code' => 'htaccess', 'message' => 'Rules are not translated.', 'fix' => '']], 'blocked' => false]);
        $this->post("/migrations/{$id}/assess");
        return $id;
    }

    public function testOnlyAdministratorsSeeMigrations(): void
    {
        $this->seedUser('alice', 'customer');
        $this->login('alice');
        foreach (['/migrations', '/migrations/new', '/migrations/1', '/migrations/1/report', '/migrations/1/report/print'] as $path) {
            // The route needs the server.manage permission, so a customer never reaches the controller.
            self::assertSame(403, $this->request('GET', $path)->status(), $path);
        }
        $before = $this->calls('migrate.panels');
        $this->post('/migrations', ['panel' => 'linux']);
        $this->post('/migrations/1/probe', ['host' => 'x.example.com', 'user' => 'root']);
        $this->post('/migrations/1/start');
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_migrations'));
        self::assertSame($before, $this->calls('migrate.panels'));
        foreach (['migrate.host_key', 'migrate.test', 'migrate.discover', 'migrate.create', 'migrate.start'] as $a) {
            self::assertSame(0, $this->calls($a), $a);
        }
    }

    public function testTheSourcePageIsHonestAboutWhatIsSupported(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/migrations/new')->body();
        self::assertStringContainsString('cPanel / WHM', $page);
        self::assertStringContainsString('Custom Linux server', $page);
        self::assertSame(14, substr_count($page, 'Experimental</span>'));
        self::assertStringNotContainsString('Fully supported</span>', $page);
        foreach (['Plesk', 'CyberPanel', 'DirectAdmin', 'HestiaCP', 'aaPanel', 'ISPConfig', 'Virtualmin'] as $p) {
            self::assertStringContainsString($p, $page);
        }
        self::assertStringContainsString('Not available yet', $page);
        // A panel that is only planned cannot be started.
        $this->post('/migrations', ['panel' => 'virtualmin']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_migrations'));
        self::assertStringContainsString('not supported yet', (string) $this->flash('error'));
        $res = $this->post('/migrations', ['panel' => 'cpanel']);
        self::assertMatchesRegularExpression('~^/migrations/\d+$~', (string) $res->header('Location'));
    }

    public function testTheServersKeyMustBeConfirmedBeforeAnyLoginIsSent(): void
    {
        $id = $this->reach('created');
        $res = $this->post("/migrations/{$id}/connect", ['password' => self::PW]);
        self::assertSame("/migrations/{$id}", $res->header('Location'));
        self::assertArrayHasKey('host', (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('migrate.test'), 'a login was sent before the key was confirmed');

        $this->agent->succeed('migrate.host_key', ['host' => 'old.example.com', 'port' => 2222, 'host_key' => self::KEY, 'key_type' => 'ssh-ed25519', 'fingerprint' => 'SHA256:tWdgkiiVDjOIaT1sNx+4LlpTvBcsMUNuMIp7I4ji9C4']);
        $this->post("/migrations/{$id}/probe", ['host' => 'old.example.com', 'port' => '2222', 'user' => 'root']);
        self::assertSame(['host' => 'old.example.com', 'port' => 2222], $this->lastCall('migrate.host_key'));
        $page = $this->request('GET', "/migrations/{$id}")->body();
        foreach (['Confirm the server', 'SHA256:tWdgkiiVDjOIaT1sNx+4LlpTvBcsMUNuMIp7I4ji9C4', 'old.example.com:2222', 'Test connection'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
    }

    /** @return list<array{0: array<string, string>, 1: string}> */
    public static function badAddresses(): array
    {
        return [
            'no host' => [['host' => '', 'user' => 'root'], 'host'],
            'option as host' => [['host' => '-oProxyCommand=x', 'user' => 'root'], 'host'],
            'host with a path' => [['host' => 'a.example.com/x', 'user' => 'root'], 'host'],
            'port 0' => [['host' => 'a.example.com', 'port' => '0', 'user' => 'root'], 'port'],
            'port text' => [['host' => 'a.example.com', 'port' => 'ssh', 'user' => 'root'], 'port'],
            'user with a semicolon' => [['host' => 'a.example.com', 'user' => 'root;id'], 'user'],
            'no user' => [['host' => 'a.example.com', 'user' => ''], 'user'],
        ];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('badAddresses')]
    public function testABadAddressIsRefusedBeforeTheAgentIsAsked(array $in, string $field): void
    {
        $id = $this->reach('created');
        $this->post("/migrations/{$id}/probe", $in);
        self::assertArrayHasKey($field, (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('migrate.host_key'));
    }

    public function testTheLoginIsEncryptedSentToTheAgentAndNeverShownOrAudited(): void
    {
        $id = $this->reach('connected');
        $m = $this->db->fetch('SELECT * FROM site_migrations WHERE id = ?', [$id]);
        self::assertSame('connected', $m['status']);
        self::assertSame(['old.example.com', 22, 'root'], [$m['source_host'], (int) $m['source_port'], $m['source_user']]);
        self::assertStringStartsWith('v1:', $m['sealed']);
        self::assertStringNotContainsString(self::PW, (string) json_encode($m));
        $login = json_decode($this->app->container->get(Crypto::class)->decrypt($m['sealed']), true);
        self::assertSame([self::PW, '', self::KEY], [$login['password'], $login['private_key'], $login['host_key']]);

        $call = $this->lastCall('migrate.test');
        self::assertSame(self::PW, $call['source']['password'], 'the agent gets the login');
        self::assertSame(self::KEY, $call['source']['host_key'], 'and the key that was confirmed');

        foreach (['site_migrations', 'audit_logs', 'agent_jobs'] as $table) {
            self::assertStringNotContainsString(self::PW, (string) json_encode($this->db->fetchAll("SELECT * FROM {$table}")), $table . ($table === 'site_migrations' ? ' (encrypted column excluded)' : ''));
        }
        // Not in the page either, nor in anything flashed.
        $page = $this->request('GET', "/migrations/{$id}")->body();
        self::assertStringNotContainsString(self::PW, $page);
        self::assertStringContainsString('Discover accounts, websites and databases', $page);
        self::assertSame('success', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'migration.connect'"));
    }

    public function testAKeyLoginIsAcceptedAndABadKeyIsNot(): void
    {
        $id = $this->reach('created');
        $this->agent->succeed('migrate.host_key', ['host_key' => self::KEY, 'key_type' => 'ssh-ed25519', 'fingerprint' => 'SHA256:abc']);
        $this->post("/migrations/{$id}/probe", ['host' => 'old.example.com', 'user' => 'root']);
        $this->post("/migrations/{$id}/connect", ['private_key' => 'not a key']);
        self::assertArrayHasKey('private_key', (array) $this->flash('errors'));
        $this->post("/migrations/{$id}/connect", ['private_key' => "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\n-----END RSA PRIVATE KEY-----"]);
        self::assertArrayHasKey('private_key', (array) $this->flash('errors'), 'an encrypted key was accepted');
        $this->post("/migrations/{$id}/connect", []);
        self::assertArrayHasKey('password', (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('migrate.test'));
        $this->agent->succeed('migrate.test', ['server' => [], 'panel' => null]);
        $this->post("/migrations/{$id}/connect", ['private_key' => "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----"]);
        $call = $this->lastCall('migrate.test');
        self::assertSame('', $call['source']['password']);
        self::assertStringContainsString('PRIVATE KEY', $call['source']['private_key']);
        self::assertSame('connected', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]));
    }

    public function testAFailedConnectionKeepsNothingAndIsAudited(): void
    {
        $id = $this->reach('created');
        $this->agent->succeed('migrate.host_key', ['host_key' => self::KEY, 'key_type' => 'ssh-ed25519', 'fingerprint' => 'SHA256:abc']);
        $this->post("/migrations/{$id}/probe", ['host' => 'old.example.com', 'user' => 'root']);
        $this->agent->fail('migrate.test', 422, 'failed', 'Permission denied (password).');
        $this->post("/migrations/{$id}/connect", ['password' => self::PW]);
        $m = $this->db->fetch('SELECT * FROM site_migrations WHERE id = ?', [$id]);
        self::assertSame('draft', $m['status']);
        self::assertSame('', $m['sealed'], 'a login that did not work was stored');
        self::assertSame('failure', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'migration.connect'"));
    }

    public function testDiscoveryRunsAsAJobAndItsResultListsWhatWasFound(): void
    {
        $id = $this->reach('connected');
        $this->agent->succeed('migrate.discover', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->post("/migrations/{$id}/discover");
        self::assertSame('aaaaaaaaaaaaaaaa', $this->flash('job'));
        self::assertSame('discovering', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]));
        self::assertSame(self::PW, $this->lastCall('migrate.discover')['source']['password']);

        // Still running: the page says so and refreshes itself.
        $this->agent->responses['/v1/actions/jobs.get'] = $this->job('running');
        $page = $this->request('GET', "/migrations/{$id}")->body();
        self::assertStringContainsString('Discovering…', $page);
        self::assertStringContainsString('migrations.js', $page);

        $this->agent->responses['/v1/actions/jobs.get'] = $this->job('succeeded', $this->discovery());
        $page = $this->request('GET', "/migrations/{$id}")->body();
        self::assertSame('discovered', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]));
        foreach (['shop.example.org', '/home/alice/public_html', '2 MiB', 'alice_wp', 'NimVor websites that already exist', 'Select all websites', 'data-nim-select-all="domains[]"', 'new.example.com', 'newsite_wp', 'Allow overwriting'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }

        // A failed discovery goes back to "connected" with the reason.
        $id2 = $this->reach('connected');
        $this->agent->succeed('migrate.discover', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $this->post("/migrations/{$id2}/discover");
        $this->agent->responses['/v1/actions/jobs.get'] = $this->job('failed', [], 'Permission denied (publickey).');
        $page = $this->request('GET', "/migrations/{$id2}")->body();
        self::assertSame('connected', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id2]));
        self::assertStringContainsString('Permission denied', $page);
    }

    public function testChoosingBuildsAPlanFromExistingWebsitesAndDatabasesOnly(): void
    {
        $id = $this->reach('selected');
        $m = $this->db->fetch('SELECT * FROM site_migrations WHERE id = ?', [$id]);
        self::assertSame('selected', $m['status']);
        $plan = json_decode($m['plan'], true);
        self::assertSame([[
            'domain' => 'new.example.com', 'source_root' => '/home/alice/public_html', 'dest_root' => '/home/nimvor/new.example.com/public_html', 'dest_user' => 'newsite', 'size_kb' => 2048,
        ]], $plan['files']);
        self::assertSame([['source' => 'alice_wp', 'dest' => 'newsite_wp', 'size_kb' => 512]], $plan['databases']);
        self::assertSame(['compress' => true, 'workers' => 3, 'excludes' => ['cache/*', '*.log'], 'allow_overwrite' => false], $plan['config']);
    }

    /** @return list<array{0: array<string, mixed>, 1: string}> */
    public static function badChoices(): array
    {
        return [
            'nothing' => [[], 'domains'],
            'no destination' => [['domains' => ['alice|shop.example.org'], 'site' => []], 'site[alice|shop.example.org]'],
            'unknown destination' => [['domains' => ['alice|shop.example.org'], 'site' => ['alice|shop.example.org' => '99999']], 'site[alice|shop.example.org]'],
            'not in the discovery' => [['domains' => ['alice|other.example.org'], 'site' => ['alice|other.example.org' => '1']], 'domains'],
            'no database destination' => [['dbs' => ['alice|alice_wp'], 'dbmap' => []], 'dbmap[alice|alice_wp]'],
            'workers' => [['domains' => ['alice|shop.example.org'], 'site' => ['alice|shop.example.org' => '1'], 'workers' => '50'], 'workers'],
            'bad exclude' => [['domains' => ['alice|shop.example.org'], 'site' => ['alice|shop.example.org' => '1'], 'excludes' => '$(reboot)'], 'excludes'],
            'exclude with dot dot' => [['domains' => ['alice|shop.example.org'], 'site' => ['alice|shop.example.org' => '1'], 'excludes' => '../etc'], 'excludes'],
        ];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('badChoices')]
    public function testBadChoicesAreRefusedAndKeepNothing(array $in, string $field): void
    {
        $id = $this->reach('discovered');
        if (isset($in['site']) || isset($in['dbmap'])) {
            // The destination ids of the data set are replaced by the real ones where it says 1.
            foreach (['site' => $this->siteId, 'dbmap' => $this->dbId] as $k => $real) {
                foreach ((array) ($in[$k] ?? []) as $key => $v) {
                    if ($v === '1') {
                        $in[$k][$key] = (string) $real;
                    }
                }
            }
        }
        $this->post("/migrations/{$id}/select", $in);
        self::assertArrayHasKey($field, (array) $this->flash('errors'));
        self::assertSame('discovered', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]));
    }

    public function testTwoSourceWebsitesCannotFillTheSameNimVorWebsite(): void
    {
        $id = $this->reach('discovered');
        $disc = $this->discovery();
        $disc['accounts'][0]['domains'][] = ['name' => 'blog.example.org', 'kind' => 'sub', 'doc_root' => '/home/alice/blog', 'size_kb' => 1];
        $this->db->run('UPDATE site_migrations SET discovery = ? WHERE id = ?', [json_encode($disc), $id]);
        $this->post("/migrations/{$id}/select", ['domains' => ['alice|shop.example.org', 'alice|blog.example.org'], 'site' => ['alice|shop.example.org' => (string) $this->siteId, 'alice|blog.example.org' => (string) $this->siteId]]);
        self::assertArrayHasKey('site[alice|blog.example.org]', (array) $this->flash('errors'));
    }

    public function testTheCompatibilityCheckIsShownAndCriticalProblemsBlockTheStart(): void
    {
        $id = $this->reach('assessed');
        $call = $this->lastCall('migrate.assess');
        self::assertSame('new.example.com', $call['plan']['files'][0]['domain']);
        self::assertSame('alice', $call['discovery']['accounts'][0]['name']);
        $page = $this->request('GET', "/migrations/{$id}")->body();
        foreach (['Readiness report', 'PHP 8.1 is not installed here.', 'Install it.', 'Warning', 'Start the migration', 'data-nim-confirm="Start the migration?'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        self::assertStringNotContainsString('MAY BE OVERWRITTEN', $page);

        // A blocking finding: the button is off and the start is refused even when posted.
        $this->agent->succeed('migrate.assess', ['findings' => [['severity' => 'critical', 'code' => 'disk_space', 'message' => 'Not enough disk space.', 'fix' => 'Free some.']], 'blocked' => true]);
        $this->post("/migrations/{$id}/assess");
        $page = $this->request('GET', "/migrations/{$id}")->body();
        self::assertStringContainsString('Critical', $page);
        self::assertMatchesRegularExpression('~<button[^>]*disabled[^>]*>Start the migration~', $page);
        $this->post("/migrations/{$id}/start");
        self::assertSame(0, $this->calls('migrate.create'));
        self::assertSame(0, $this->calls('migrate.start'));
        self::assertStringContainsString('stop the migration', (string) $this->flash('error'));
    }

    public function testStartingNeedsACheckAndWarnsWhenOverwritingIsAllowed(): void
    {
        $id = $this->reach('selected');
        $this->post("/migrations/{$id}/start");
        self::assertSame(0, $this->calls('migrate.create'));
        self::assertStringContainsString('compatibility check', (string) $this->flash('error'));

        $this->post("/migrations/{$id}/select", ['domains' => ['alice|shop.example.org'], 'site' => ['alice|shop.example.org' => (string) $this->siteId], 'allow_overwrite' => '1']);
        $this->agent->succeed('migrate.assess', ['findings' => [], 'blocked' => false]);
        $this->post("/migrations/{$id}/assess");
        $page = $this->request('GET', "/migrations/{$id}")->body();
        self::assertStringContainsString('existing files and tables MAY BE OVERWRITTEN', $page);
    }

    public function testStartStoresThePlanInTheAgentAndRunsItWithTheLoginForThisRunOnly(): void
    {
        $id = $this->reach('assessed');
        $this->agent->succeed('migrate.create', ['id' => '0123456789abcdef', 'status' => 'created']);
        $this->agent->succeed('migrate.start', ['job_id' => 'cccccccccccccccc', 'id' => '0123456789abcdef']);
        $res = $this->post("/migrations/{$id}/start");
        self::assertSame("/migrations/{$id}", $res->header('Location'), (string) $this->flash('error'));
        self::assertSame('cccccccccccccccc', $this->flash('job'));
        $create = $this->lastCall('migrate.create');
        self::assertSame('/home/nimvor/new.example.com/public_html', $create['plan']['files'][0]['dest_root']);
        self::assertSame('ssh://root@old.example.com:22', $create['source']);
        self::assertStringNotContainsString(self::PW, (string) json_encode($create), 'the plan carries no login');
        $start = $this->lastCall('migrate.start');
        self::assertSame('0123456789abcdef', $start['id']);
        self::assertSame(self::PW, $start['source']['password']);
        $m = $this->db->fetch('SELECT * FROM site_migrations WHERE id = ?', [$id]);
        self::assertSame(['running', '0123456789abcdef'], [$m['status'], $m['agent_id']]);
        $audit = $this->db->fetch("SELECT * FROM audit_logs WHERE action = 'migration.start'");
        self::assertSame('success', $audit['result']);
        self::assertStringNotContainsString(self::PW, (string) $audit['details']);
        self::assertStringContainsString('"websites":1', (string) $audit['details']);
    }

    private function started(): int
    {
        $id = $this->reach('assessed');
        $this->agent->succeed('migrate.create', ['id' => '0123456789abcdef', 'status' => 'created']);
        $this->agent->succeed('migrate.start', ['job_id' => 'cccccccccccccccc', 'id' => '0123456789abcdef']);
        $this->post("/migrations/{$id}/start");
        return $id;
    }

    private function agentState(string $status, array $tasks = [], array $extra = []): void
    {
        $this->agent->succeed('migrate.get', $extra + ['id' => '0123456789abcdef', 'status' => $status, 'tasks' => $tasks, 'error' => '']);
    }

    public function testProgressShowsEachTaskAndItsOutcome(): void
    {
        $id = $this->started();
        $this->agentState('running', [
            ['id' => 'files:/x', 'kind' => 'files', 'label' => 'Files of new.example.com', 'status' => 'done', 'attempts' => 1, 'detail' => '12 files, 2048 bytes copied; 3 differ after verification', 'differences' => 3],
            ['id' => 'db:newsite_wp', 'kind' => 'db', 'label' => 'Database alice_wp → newsite_wp', 'status' => 'running', 'attempts' => 1],
        ]);
        $page = $this->request('GET', "/migrations/{$id}")->body();
        foreach (['Files of new.example.com', 'Done', 'Running…', '3 file(s) changed while they were copied', 'Pause', 'Cancel', 'migrations.js', 'Report (JSON)'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        self::assertSame('running', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]));
    }

    public function testAFailureSaysWhatIsLeftAndCanBeContinued(): void
    {
        $id = $this->started();
        $this->agentState('failed', [['id' => 'db:newsite_wp', 'kind' => 'db', 'label' => 'Database', 'status' => 'failed', 'attempts' => 3, 'error' => 'the database newsite_wp already has 3 table(s).']], [
            'error' => '1 task(s) failed.', 'remaining' => ['The database newsite_wp was not imported.'],
        ]);
        $page = $this->request('GET', "/migrations/{$id}")->body();
        foreach (['Failed', 'already has 3 table', 'What is left to do', 'The database newsite_wp was not imported.', 'Try the failed tasks again', 'Cancel'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        self::assertSame('failed', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]));
        // Continue: the login is sent again from its encrypted copy.
        $this->agent->succeed('migrate.resume', ['job_id' => 'dddddddddddddddd', 'id' => '0123456789abcdef']);
        $this->post("/migrations/{$id}/resume");
        self::assertSame(self::PW, $this->lastCall('migrate.resume')['source']['password']);
        self::assertSame('running', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]));
    }

    public function testCompletionErasesTheStoredLoginAndTheReportHoldsNoSecret(): void
    {
        $id = $this->started();
        self::assertNotSame('', $this->db->scalar('SELECT sealed FROM site_migrations WHERE id = ?', [$id]));
        $this->agentState('completed', [['id' => 'files:/x', 'kind' => 'files', 'label' => 'Files of new.example.com', 'status' => 'done', 'attempts' => 1, 'detail' => '12 files copied', 'differences' => 0]]);
        $page = $this->request('GET', "/migrations/{$id}")->body();
        $m = $this->db->fetch('SELECT * FROM site_migrations WHERE id = ?', [$id]);
        self::assertSame(['completed', ''], [$m['status'], $m['sealed']], 'the login must be erased when the migration completes');
        self::assertNotNull($m['finished_at']);
        self::assertStringContainsString('After the migration', $page);
        self::assertStringContainsString('Nothing was deleted on the source', $page);

        $res = $this->request('GET', "/migrations/{$id}/report");
        self::assertStringContainsString('attachment; filename="migration-' . $id . '-report.json"', (string) $res->header('Content-Disposition'));
        $report = json_decode($res->body(), true);
        self::assertSame('completed', $report['status']);
        self::assertSame('old.example.com', $report['source']['host']);
        self::assertSame('Files of new.example.com', $report['tasks'][0]['label']);
        self::assertContains('DNS zones and records', $report['not_migrated']);
        self::assertNotEmpty($report['recommendations']);
        self::assertStringNotContainsString(self::PW, $res->body());
        self::assertStringNotContainsString('host_key', $res->body());
        self::assertStringNotContainsString(self::KEY, $res->body());
        $print = $this->request('GET', "/migrations/{$id}/report/print");
        self::assertSame(200, $print->status());
        self::assertStringContainsString('Migration report #' . $id, $print->body());
        self::assertStringContainsString('Files of new.example.com', $print->body());
        self::assertStringContainsString('Not moved by this version', $print->body());
        self::assertStringNotContainsString('<style', $print->body());
        self::assertStringNotContainsString('onclick=', $print->body());
        self::assertStringNotContainsString(self::PW, $print->body());
        self::assertStringNotContainsString(self::KEY, $print->body());
        // It cannot be continued without connecting again.
        $this->post("/migrations/{$id}/resume");
        self::assertStringContainsString('cannot be continued', (string) $this->flash('error'));
    }

    public function testCancelErasesTheLoginKeepsWhatWasCopiedAndSaysSo(): void
    {
        $id = $this->started();
        $this->agent->succeed('migrate.cancel', ['id' => '0123456789abcdef', 'stopping' => 'cancelled']);
        $this->post("/migrations/{$id}/cancel");
        self::assertSame('0123456789abcdef', $this->lastCall('migrate.cancel')['id']);
        $m = $this->db->fetch('SELECT * FROM site_migrations WHERE id = ?', [$id]);
        self::assertSame(['cancelled', ''], [$m['status'], $m['sealed']]);
        self::assertStringContainsString('nothing was removed from the source', (string) $this->flash('success'));
        // The page asks before cancelling.
        $id2 = $this->started();
        $this->agentState('running');
        self::assertStringContainsString('data-nim-confirm="Cancel this migration?', $this->request('GET', "/migrations/{$id2}")->body());
    }

    public function testThePageAsksBeforeErasingTheLoginAndTheLoginCanBeErasedOnRequest(): void
    {
        $id = $this->reach('connected');
        self::assertStringContainsString('data-nim-confirm="Erase the stored login?', $this->request('GET', "/migrations/{$id}")->body());
        $this->post("/migrations/{$id}/forget");
        self::assertSame('', $this->db->scalar('SELECT sealed FROM site_migrations WHERE id = ?', [$id]));
        $this->agent->succeed('migrate.discover', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->post("/migrations/{$id}/discover");
        self::assertSame(0, $this->calls('migrate.discover'), 'discovery ran without a login');
        self::assertStringContainsString('Connect again', (string) $this->flash('error'));
    }

    public function testARunningMigrationCannotBeRemovedAndAFinishedOneCan(): void
    {
        $id = $this->started();
        $this->post("/migrations/{$id}/delete");
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM site_migrations WHERE id = ?', [$id]));
        self::assertStringContainsString('Cancel it first', (string) $this->flash('error'));
        $this->agent->succeed('migrate.cancel', ['id' => '0123456789abcdef']);
        $this->post("/migrations/{$id}/cancel");
        $this->post("/migrations/{$id}/delete");
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_migrations WHERE id = ?', [$id]));
    }

    public function testTheDashboardListsMigrationsWithTheirStatus(): void
    {
        $this->login('admin');
        self::assertStringContainsString('No migration yet', $this->request('GET', '/migrations')->body());
        $id = $this->reach('connected');
        $page = $this->request('GET', '/migrations')->body();
        foreach (['root@old.example.com:22', 'Connected', 'Continue', 'New migration'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        self::assertStringContainsString('Migrations', $this->request('GET', '/')->body(), 'the menu has an entry');
        self::assertGreaterThan(0, $id);
    }

    public function testRollbackNeedsConfirmationAndNeverRunsWhileTheMigrationRuns(): void
    {
        $id = $this->started();
        $this->agentState('running');
        $page = $this->request('GET', "/migrations/{$id}")->body();
        self::assertStringNotContainsString('Roll back this migration', $page, 'offered while it runs');
        $this->post("/migrations/{$id}/rollback", ['confirm' => '1']);
        self::assertSame(0, $this->calls('migrate.rollback'));
        self::assertStringContainsString('cannot be rolled back', (string) $this->flash('error'));

        $this->agentState('failed', [['id' => 'files:/x', 'kind' => 'files', 'label' => 'Files', 'status' => 'failed', 'attempts' => 3, 'error' => 'x']]);
        $page = $this->request('GET', "/migrations/{$id}")->body();
        foreach (['Roll back this migration', 'data-nim-confirm="Roll back this migration?', 'Also empty the databases it filled', 'name="empty_databases"', 'cannot be restored'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        // Posting without the confirmation does nothing.
        $this->post("/migrations/{$id}/rollback", []);
        self::assertSame(0, $this->calls('migrate.rollback'));
        self::assertStringContainsString('Confirm the rollback', (string) $this->flash('error'));
        self::assertSame('failed', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]));
    }

    public function testARollbackIsSentToTheAgentRecordedAndShown(): void
    {
        $id = $this->started();
        $this->agentState('failed');
        $this->agent->succeed('migrate.rollback', ['at' => gmdate('c'), 'files_removed' => 12, 'files_gone' => 0, 'dirs_removed' => 2, 'databases_emptied' => ['newsite_wp'], 'kept' => ['/home/nimvor/new.example.com/public_html/index.php (changed since the migration: newer data is never removed)'], 'notes' => ['x: 1 existing file(s) were overwritten by the copy and cannot be restored (no copy of them was kept).']]);
        $res = $this->post("/migrations/{$id}/rollback", ['confirm' => '1', 'empty_databases' => '1']);
        self::assertSame("/migrations/{$id}", $res->header('Location'), (string) $this->flash('error'));
        $call = $this->lastCall('migrate.rollback');
        self::assertSame(['0123456789abcdef', true, true], [$call['id'], $call['confirm'], $call['empty_databases']]);
        self::assertStringNotContainsString(self::PW, (string) json_encode($call), 'a rollback needs no login');
        $m = $this->db->fetch('SELECT * FROM site_migrations WHERE id = ?', [$id]);
        self::assertSame(['rolled_back', ''], [$m['status'], $m['sealed']]);
        $audit = $this->db->fetch("SELECT * FROM audit_logs WHERE action = 'migration.rollback'");
        self::assertSame('success', $audit['result']);
        self::assertStringContainsString('"files_removed":12', (string) $audit['details']);

        $this->agentState('rolled_back', [], ['rollback' => json_decode('{"files_removed":12,"dirs_removed":2,"databases_emptied":["newsite_wp"],"kept":["/home/nimvor/new.example.com/public_html/index.php (changed since the migration: newer data is never removed)"],"notes":["cannot be restored"]}', true)]);
        $page = $this->request('GET', "/migrations/{$id}")->body();
        foreach (['Rollback', '12 file(s) and 2 folder(s)', 'newsite_wp', 'Kept, because removing it could lose newer data', 'newer data is never removed', 'Nothing on the source was touched'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        self::assertStringNotContainsString('Roll back this migration', $page, 'offered again after a rollback');
        // It cannot be continued or rolled back again, but its record can be removed.
        $this->post("/migrations/{$id}/rollback", ['confirm' => '1']);
        self::assertSame(1, $this->calls('migrate.rollback'));
        $this->post("/migrations/{$id}/delete");
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_migrations WHERE id = ?', [$id]));
    }

    public function testARefusedRollbackKeepsTheMigrationAsItWasAndIsAudited(): void
    {
        $id = $this->started();
        $this->agentState('completed');
        $this->agent->fail('migrate.rollback', 422, 'invalid_params', 'the migration is running: pause or cancel it first');
        $this->post("/migrations/{$id}/rollback", ['confirm' => '1']);
        self::assertSame('completed', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]), 'the agent refused, so the migration must not be recorded as rolled back');
        self::assertSame('failure', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'migration.rollback'"));
    }

    private function validationReport(int $failed = 0): array
    {
        return ['at' => gmdate('c'), 'passed' => 5, 'warned' => 1, 'failed' => $failed, 'limits' => ['PHP execution is not checked.'], 'checks' => [
            ['item' => 'new.example.com', 'name' => 'Document root', 'status' => 'pass', 'detail' => '/home/nimvor/new.example.com/public_html exists'],
            ['item' => 'new.example.com', 'name' => 'HTTP answer', 'status' => 'warn', 'detail' => 'the site answers with HTTP 500'],
            ['item' => 'newsite_wp', 'name' => 'Table count', 'status' => $failed > 0 ? 'fail' : 'pass', 'detail' => '1 table(s), as imported'],
        ]];
    }

    public function testValidationIsOfferedAfterARunAndShowsWhatItDoesNotCheck(): void
    {
        $id = $this->started();
        $this->agentState('running');
        self::assertStringNotContainsString('Validate', $this->request('GET', "/migrations/{$id}")->body(), 'offered while running');
        $this->post("/migrations/{$id}/validate");
        self::assertSame(0, $this->calls('migrate.validate'));
        self::assertStringContainsString('nothing to validate', (string) $this->flash('error'));

        $this->agentState('completed');
        self::assertStringContainsString('Validate</button>', $this->request('GET', "/migrations/{$id}")->body());
        $this->agent->succeed('migrate.validate', $this->validationReport(1));
        $res = $this->post("/migrations/{$id}/validate");
        self::assertSame("/migrations/{$id}", $res->header('Location'), (string) $this->flash('error'));
        self::assertSame('0123456789abcdef', $this->lastCall('migrate.validate')['id']);
        self::assertSame('failure', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'migration.validate'"), 'a failed check is audited as a failure');

        $this->agentState('completed', [], ['validation' => $this->validationReport(1)]);
        $page = $this->request('GET', "/migrations/{$id}")->body();
        foreach (['Validation', '5</strong> passed', 'Document root', 'the site answers with HTTP 500', 'Look', 'Fail', 'What this does not check', 'PHP execution is not checked.', 'Validate again'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        $report = json_decode($this->request('GET', "/migrations/{$id}/report")->body(), true);
        self::assertSame(1, $report['validation']['failed']);
        self::assertStringContainsString('PHP, SSL and DNS checks are not run', $report['validation_note']);
    }

    public function testARolledBackMigrationIsNotValidated(): void
    {
        $id = $this->started();
        $this->agentState('failed');
        $this->agent->succeed('migrate.rollback', ['files_removed' => 0, 'dirs_removed' => 0, 'databases_emptied' => [], 'kept' => [], 'notes' => []]);
        $this->post("/migrations/{$id}/rollback", ['confirm' => '1']);
        $this->post("/migrations/{$id}/validate");
        self::assertSame(0, $this->calls('migrate.validate'));
    }

    private function dnsReport(string $status = 'mixed'): array
    {
        return ['at' => gmdate('c'), 'server_ips' => ['203.0.113.10'], 'notes' => ['The TTL cannot be read here: look at it at your DNS provider.'], 'domains' => [[
            'domain' => 'new.example.com', 'status' => $status, 'advice' => 'The change has reached some resolvers and not others.',
            'resolvers' => [['name' => "This server's resolver", 'addrs' => ['203.0.113.10'], 'points_here' => true], ['name' => 'Google (8.8.8.8)', 'addrs' => ['198.51.100.7'], 'points_here' => false], ['name' => 'Quad9 (9.9.9.9)', 'addrs' => [], 'points_here' => false, 'error' => 'no answer']],
        ]]];
    }

    public function testTheDnsCheckReadsOnlyAndSaysThatNimVorNeverChangesDns(): void
    {
        $id = $this->started();
        $this->agentState('running');
        $this->post("/migrations/{$id}/dns");
        self::assertSame(0, $this->calls('migrate.cutover_check'), 'checked while it runs');

        $this->agentState('completed');
        $page = $this->request('GET', "/migrations/{$id}")->body();
        foreach (['DNS cut-over', 'NimVor never changes DNS records or nameservers', 'Check DNS', 'Cloudflare, Google and Quad9'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        $this->agent->succeed('net.addresses', ['addresses' => [['address' => '203.0.113.10', 'scope' => 'global'], ['address' => '10.0.0.5', 'scope' => 'global'], ['address' => 'fe80::1', 'scope' => 'link']]]);
        $this->agent->succeed('migrate.cutover_check', $this->dnsReport());
        $res = $this->post("/migrations/{$id}/dns");
        self::assertSame("/migrations/{$id}", $res->header('Location'), (string) $this->flash('error'));
        $call = $this->lastCall('migrate.cutover_check');
        self::assertSame('0123456789abcdef', $call['id']);
        self::assertSame(['203.0.113.10'], $call['server_ips'], 'only this server\'s public addresses are compared');
        self::assertStringContainsString('NimVor changed nothing', (string) $this->flash('success'));
        self::assertSame('success', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'migration.dns_check'"));
        // The panel has no route that changes DNS for a migration.
        foreach (['/dns/update', '/migrations/' . $id . '/cutover', '/migrations/' . $id . '/nameservers'] as $path) {
            self::assertSame(404, $this->post($path)->status(), $path);
        }

        $this->agentState('completed', [], ['cutover' => $this->dnsReport()]);
        $page = $this->request('GET', "/migrations/{$id}")->body();
        foreach (['Still spreading', 'new.example.com', '198.51.100.7', 'no answer', 'This server\'s addresses', '203.0.113.10', 'The TTL cannot be read here', 'Check again'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        $this->agentState('completed', [], ['cutover' => $this->dnsReport('here')]);
        self::assertStringContainsString('Points to this server', $this->request('GET', "/migrations/{$id}")->body());
    }

    public function testTheDnsCheckNeedsTheServersPublicAddresses(): void
    {
        $id = $this->started();
        $this->agentState('completed');
        $this->agent->succeed('net.addresses', ['addresses' => [['address' => '10.0.0.5', 'scope' => 'global']]]); // private only
        $this->post("/migrations/{$id}/dns");
        self::assertSame(0, $this->calls('migrate.cutover_check'));
        self::assertStringContainsString('public addresses are not known', (string) $this->flash('error'));
    }

    public function testSyncAsksForTheLoginAgainKeepsThePinnedKeyAndSaysDatabasesAreNotIncluded(): void
    {
        $id = $this->started();
        $this->agentState('running');
        self::assertStringNotContainsString('Synchronise files and mail', $this->request('GET', "/migrations/{$id}")->body(), 'offered while it runs');
        $this->post("/migrations/{$id}/sync", ['password' => self::PW]);
        self::assertSame(0, $this->calls('migrate.sync'));
        self::assertStringContainsString('cannot be synchronised now', (string) $this->flash('error'));

        $this->agentState('completed');
        $page = $this->request('GET', "/migrations/{$id}")->body();
        self::assertSame('', $this->db->scalar('SELECT sealed FROM site_migrations WHERE id = ?', [$id]), 'the login was erased at completion');
        foreach (['Synchronise before switching DNS', 'only the website files and mail messages that changed on the source', 'newer here', 'Databases are not synchronised', 'Synchronise files and mail', 'stays pinned'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        // No login: refused before the agent is asked.
        $this->post("/migrations/{$id}/sync", []);
        self::assertArrayHasKey('password', (array) $this->flash('errors'));
        $this->post("/migrations/{$id}/sync", ['private_key' => 'not a key']);
        self::assertArrayHasKey('private_key', (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('migrate.sync'));

        $this->agent->succeed('migrate.sync', ['job_id' => 'eeeeeeeeeeeeeeee', 'id' => '0123456789abcdef']);
        $res = $this->post("/migrations/{$id}/sync", ['password' => 'Another-Pa55word']);
        self::assertSame("/migrations/{$id}", $res->header('Location'), (string) $this->flash('error'));
        self::assertSame('eeeeeeeeeeeeeeee', $this->flash('job'));
        $call = $this->lastCall('migrate.sync');
        self::assertSame('0123456789abcdef', $call['id']);
        self::assertSame(['Another-Pa55word', self::KEY, 'old.example.com', 'root'], [$call['source']['password'], $call['source']['host_key'], $call['source']['host'], $call['source']['user']], 'the new login and the pinned key');
        $m = $this->db->fetch('SELECT * FROM site_migrations WHERE id = ?', [$id]);
        self::assertSame('running', $m['status']);
        self::assertStringStartsWith('v1:', $m['sealed']);
        foreach (['site_migrations', 'audit_logs'] as $table) {
            self::assertStringNotContainsString('Another-Pa55word', (string) json_encode(array_map(static fn (array $r): array => array_diff_key($r, ['sealed' => 1]), $this->db->fetchAll("SELECT * FROM {$table}"))), $table);
        }
        self::assertSame('success', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'migration.sync'"));
        // When the agent says it is completed again, the login is erased again.
        $this->agentState('completed');
        $this->request('GET', "/migrations/{$id}");
        self::assertSame('', $this->db->scalar('SELECT sealed FROM site_migrations WHERE id = ?', [$id]));
    }

    public function testSyncIsNotOfferedForARolledBackOrDatabaseOnlyMigration(): void
    {
        $id = $this->started();
        $this->agentState('completed');
        $this->db->run('UPDATE site_migrations SET plan = ? WHERE id = ?', [json_encode(['files' => [], 'databases' => [['source' => 'a', 'dest' => 'b']], 'config' => []]), $id]);
        self::assertStringNotContainsString('Synchronise files and mail', $this->request('GET', "/migrations/{$id}")->body());
        $this->post("/migrations/{$id}/sync", ['password' => 'x']);
        self::assertSame(0, $this->calls('migrate.sync'));
        self::assertStringContainsString('no website files', (string) $this->flash('error'));
    }

    private function makeMailbox(string $local, string $domain): void
    {
        $now = gmdate('c');
        $d = $this->db->fetch('SELECT id FROM mail_domains WHERE domain = ?', [$domain]);
        if ($d === null) {
            $this->db->run('INSERT INTO mail_domains (server_id, domain, created_at) VALUES (1, ?, ?)', [$domain, $now]);
            $d = ['id' => $this->db->lastInsertId()];
        }
        $this->db->run('INSERT INTO mail_mailboxes (domain_id, local, quota_mb, created_at) VALUES (?, ?, 0, ?)', [$d['id'], $local, $now]);
    }

    public function testAMailboxIsCopiedIntoTheNimVorMailboxWithTheSameAddress(): void
    {
        $this->makeMailbox('info', 'shop.example.org');
        $id = $this->reach('discovered');
        self::assertStringContainsString('Mailboxes to copy', $this->request('GET', "/migrations/{$id}")->body());
        $this->post("/migrations/{$id}/select", ['mail' => ['alice|info@shop.example.org'], 'workers' => '2']);
        $plan = json_decode((string) $this->db->scalar('SELECT plan FROM site_migrations WHERE id = ?', [$id]), true);
        self::assertSame([['address' => 'info@shop.example.org', 'source_maildir' => '/home/alice/mail/x', 'size_kb' => -1]], $plan['mail']);
        self::assertSame([], $plan['files']);
    }

    public function testAMailboxThatDoesNotExistInNimVorOrInTheDiscoveryIsRefused(): void
    {
        $id = $this->reach('discovered');
        $this->post("/migrations/{$id}/select", ['mail' => ['alice|info@shop.example.org']]);
        self::assertStringContainsString('does not exist in NimVor yet', (string) ((array) $this->flash('errors'))['mail']);
        $this->makeMailbox('info', 'shop.example.org');
        $this->post("/migrations/{$id}/select", ['mail' => ['alice|ghost@shop.example.org']]);
        self::assertStringContainsString('not in the discovery', (string) ((array) $this->flash('errors'))['mail']);
        self::assertSame('discovered', $this->db->scalar('SELECT status FROM site_migrations WHERE id = ?', [$id]));
    }

    public function testForwardersBecomeNimVorAliasesAndExistingOrUnknownDomainsAreLeftAlone(): void
    {
        $this->makeMailbox('info', 'shop.example.org');
        $domainId = (int) $this->db->scalar("SELECT id FROM mail_domains WHERE domain = 'shop.example.org'");
        $id = $this->reach('discovered');
        self::assertStringContainsString('Forwarders to create', $this->request('GET', "/migrations/{$id}")->body());
        $this->agent->succeed('mail.alias_save', []);
        $this->post("/migrations/{$id}/forwarders", ['forwarders' => ['alice|sales@shop.example.org', 'alice|old@other.example', 'alice|ghost@shop.example.org']]);
        self::assertSame('boss@gmail.com, ops@example.org', $this->db->scalar('SELECT destinations FROM mail_aliases WHERE domain_id = ? AND local = ?', [$domainId, 'sales']));
        self::assertSame(1, $this->calls('mail.alias_save'));
        $msg = (string) $this->flash('success');
        self::assertStringContainsString('1 forwarder(s) created', $msg);
        self::assertStringContainsString('not set up for email', $msg);
        self::assertStringContainsString('not in the discovery', $msg);
        // Run again: the alias exists now, so it is not overwritten.
        $this->db->run('UPDATE mail_aliases SET destinations = ? WHERE domain_id = ?', ['mine@example.org', $domainId]);
        $this->post("/migrations/{$id}/forwarders", ['forwarders' => ['alice|sales@shop.example.org']]);
        self::assertSame('mine@example.org', $this->db->scalar('SELECT destinations FROM mail_aliases WHERE domain_id = ?', [$domainId]));
        self::assertSame(1, $this->calls('mail.alias_save'));
        $this->post("/migrations/{$id}/forwarders", []);
        self::assertArrayHasKey('forwarders', (array) $this->flash('errors'));
    }

    public function testDnsRecordsOfACpanelSourceGoIntoExistingNimVorZonesWithoutAddressRecords(): void
    {
        $now = gmdate('c');
        $this->db->run("INSERT INTO dns_zones (server_id, name, primary_ns, hostmaster, serial, created_at, updated_at) VALUES (1, 'shop.example.org', 'ns1.nimvor.test.', 'hostmaster.shop.example.org.', 1, ?, ?)", [$now, $now]);
        $zoneId = $this->db->lastInsertId();
        $id = $this->reach('discovered');
        $this->db->run("UPDATE site_migrations SET panel = 'cpanel' WHERE id = ?", [$id]);
        $this->agent->succeedAny();
        self::assertStringContainsString('DNS records to add', $this->request('GET', "/migrations/{$id}")->body());
        $this->agent->succeed('migrate.dns_zone', ['domain' => 'shop.example.org', 'text' => "\$TTL 14400\n@ IN SOA ns1. h. ( 1 2 3 4 5 )\n@ IN A 192.0.2.10\nwww IN A 192.0.2.10\n@ IN MX 10 mail.shop.example.org.\n@ IN TXT \"v=spf1 a mx ~all\"\n"]);
        $this->agent->succeed('dns.zone_parse', ['records' => [
            ['name' => '@', 'type' => 'MX', 'ttl' => 0, 'value' => 'mail.shop.example.org.', 'priority' => 10, 'weight' => 0, 'port' => 0],
            ['name' => '@', 'type' => 'TXT', 'ttl' => 0, 'value' => 'v=spf1 a mx ~all', 'priority' => 0, 'weight' => 0, 'port' => 0],
        ], 'skipped' => []]);
        $this->post("/migrations/{$id}/dns-import", ['dns' => ['shop.example.org', 'other.example', 'ghost.example.org']]);
        // The agent reads the zone file with the stored login; the parsed text has no address records.
        self::assertSame('shop.example.org', $this->lastCall('migrate.dns_zone')['domain']);
        self::assertArrayHasKey('source', $this->lastCall('migrate.dns_zone'));
        $sent = (string) $this->lastCall('dns.zone_parse')['text'];
        self::assertStringNotContainsString('192.0.2.10', $sent);
        self::assertStringContainsString('MX 10', $sent);
        self::assertStringContainsString('v=spf1 a mx', $sent, 'a TXT record is kept even though it mentions "a"');
        self::assertSame(2, (int) $this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND type IN (\'MX\', \'TXT\')', [$zoneId]));
        $msg = (string) $this->flash('success');
        self::assertStringContainsString('2 DNS record(s) added', $msg);
        self::assertStringContainsString('other.example (not in the discovery)', $msg);
        $this->post("/migrations/{$id}/dns-import", []);
        self::assertArrayHasKey('dns', (array) $this->flash('errors'));
    }

    public function testDnsImportIsOfferedForDirectAdminButRefusedForASourceWithoutZoneFiles(): void
    {
        $id = $this->reach('discovered');
        $this->db->run("UPDATE site_migrations SET panel = 'directadmin' WHERE id = ?", [$id]);
        self::assertStringContainsString('DNS records to add', $this->request('GET', "/migrations/{$id}")->body());
        $this->db->run("UPDATE site_migrations SET panel = 'froxlor' WHERE id = ?", [$id]);
        self::assertStringNotContainsString('DNS records to add', $this->request('GET', "/migrations/{$id}")->body());
        $this->post("/migrations/{$id}/dns-import", ['dns' => ['shop.example.org']]);
        self::assertSame(0, $this->calls('migrate.dns_zone'));
        self::assertStringContainsString('DNS zones can only be read from', (string) $this->flash('error'));
    }

    public function testFtpAccountsAreCreatedInsideTheMatchingWebsiteWithNewPasswordsShownOnce(): void
    {
        $id = $this->reach('selected');
        $this->agent->succeed('ftp.status', ['accounts' => [['name' => 'old@new.example.com']]]);
        $this->agent->succeed('ftp.account_save', []);
        $this->post("/migrations/{$id}/ftp", ['ftp' => ['alice|staging@shop.example.org', 'alice|elsewhere', 'alice|Bad Name', 'alice|ghost']]);
        $call = $this->lastCall('ftp.account_save');
        self::assertSame('staging@new.example.com', $call['name']);
        self::assertSame('newsite', $call['user']);
        self::assertSame('staging', $call['dir']);
        self::assertGreaterThanOrEqual(10, strlen((string) $call['password']));
        self::assertSame(1, $this->calls('ftp.account_save'));
        $msg = (string) $this->flash('success');
        self::assertStringContainsString('1 FTP account(s) created', $msg);
        self::assertStringContainsString('elsewhere (its folder is not inside a website you are copying)', $msg);
        self::assertStringContainsString('Bad Name', $msg);
        self::assertStringContainsString('ghost (not in the discovery)', $msg);
        self::assertStringNotContainsString((string) $call['password'], $msg, 'the password must not be in the flash message');
        // The password is shown once, on the next page, and is not kept in the database.
        $page = $this->request('GET', "/migrations/{$id}")->body();
        self::assertStringContainsString($call['password'], $page);
        self::assertStringNotContainsString($call['password'], $this->request('GET', "/migrations/{$id}")->body());
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM site_migrations WHERE plan LIKE '%' || ? || '%' OR discovery LIKE '%' || ? || '%'", [$call['password'], $call['password']]));
        // An account that exists already is left alone.
        $this->agent->succeed('ftp.status', ['accounts' => [['name' => 'staging@new.example.com']]]);
        $this->post("/migrations/{$id}/ftp", ['ftp' => ['alice|staging@shop.example.org']]);
        self::assertSame(1, $this->calls('ftp.account_save'));
        self::assertStringContainsString('already exists', (string) $this->flash('success'));
        $this->post("/migrations/{$id}/ftp", []);
        self::assertArrayHasKey('ftp', (array) $this->flash('errors'));
    }
}
