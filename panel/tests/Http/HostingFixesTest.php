<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\SettingsService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * Hosting-customer fixes: automatic backups are the administrator's, DNS
 * and email an administrator set up for a customer's website belong to
 * that website's owner, Let's Encrypt notices go to the account's email,
 * database users can get remote access and be assigned to databases.
 */
final class HostingFixesTest extends HttpTestCase
{
    private int $siteId;
    private int $otherId;
    private int $custId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->custId = $this->seedUser('cust', 'customer');
        $this->seedUser('mallory', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $users->find($this->custId));
        $this->otherId = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('db.user_create', ['ok' => true]);
        $this->agent->succeed('db.grant', ['ok' => true]);
    }

    private function switchTo(string $user): void
    {
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testOnlyAdministratorsSetUpAutomaticBackups(): void
    {
        $base = '/sites/' . $this->siteId;
        $schedule = ['frequency' => 'daily', 'hour' => '3', 'retention' => '2', 'include_files' => '1', 'include_databases' => '0'];
        $this->login('cust');
        $page = $this->request('GET', $base . '/backups')->body();
        self::assertStringContainsString('set up by your administrator', $page);
        self::assertStringNotContainsString('/backup-schedules"', $page, 'no schedule form');
        $this->post($base . '/backup-schedules', $schedule);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM backup_schedules'));

        $this->switchTo('admin');
        $this->post($base . '/backup-schedules', $schedule);
        $id = (int) $this->db->scalar('SELECT id FROM backup_schedules');
        self::assertGreaterThan(0, $id);

        // The customer sees it, but cannot remove it.
        $this->switchTo('cust');
        $page = $this->request('GET', $base . '/backups')->body();
        self::assertStringContainsString('Every day at 03:00', $page);
        self::assertStringNotContainsString('/backup-schedules/' . $id . '/delete', $page);
        $this->post($base . '/backup-schedules/' . $id . '/delete');
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM backup_schedules'));
    }

    public function testDnsAndEmailSetUpByTheAdministratorBelongToTheWebsiteOwner(): void
    {
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('dns.status', ['installed' => true, 'configured' => true, 'running' => true, 'zones' => []]);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->agent->succeed('mail.status', ['installed' => ['postfix' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => ['postfix' => true], 'mailboxes' => []]);
        $this->agent->succeed('mail.domain_add', ['domain' => 'shop.test', 'dkim_selector' => 'whacp202609', 'dkim_txt' => 'v=DKIM1; k=rsa; p=MIIB']);

        $this->login('admin');
        $this->post('/dns/zones', ['website_id' => (string) $this->siteId, 'template' => '1']);
        $this->post('/mail/domains', ['website_id' => (string) $this->siteId]);
        $zone = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = 'shop.test'");
        $mail = (int) $this->db->scalar("SELECT id FROM mail_domains WHERE domain = 'shop.test'");
        self::assertGreaterThan(0, $zone);
        self::assertGreaterThan(0, $mail);

        $this->switchTo('cust');
        self::assertSame('/dns/zones/' . $zone, $this->request('GET', '/sites/' . $this->siteId . '/dns')->header('Location'));
        self::assertSame('/mail/domains/' . $mail, $this->request('GET', '/sites/' . $this->siteId . '/mail')->header('Location'));
        self::assertSame(200, $this->request('GET', '/dns/zones/' . $zone)->status());
        self::assertSame(200, $this->request('GET', '/mail/domains/' . $mail)->status());
        self::assertStringContainsString('shop.test', $this->request('GET', '/dns')->body());
        // ...and may manage the records.
        $this->post('/dns/zones/' . $zone . '/records', ['name' => 'blog', 'type' => 'A', 'value' => '203.0.113.20']);
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND name = 'blog'", [$zone]));

        // Another customer still sees neither.
        $this->switchTo('mallory');
        self::assertSame(404, $this->request('GET', '/dns/zones/' . $zone)->status());
        self::assertSame(404, $this->request('GET', '/mail/domains/' . $mail)->status());
        self::assertStringNotContainsString('shop.test', $this->request('GET', '/dns')->body());
        $this->post('/dns/zones/' . $zone . '/records', ['name' => 'evil', 'type' => 'A', 'value' => '203.0.113.66']);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM dns_records WHERE name = 'evil'"));
    }

    public function testLetsEncryptNoticesGoToTheAccountEmail(): void
    {
        $this->agent->succeed('ssl.issue', ['cert_file' => '/etc/letsencrypt/live/shop.test/fullchain.pem', 'key_file' => '/etc/letsencrypt/live/shop.test/privkey.pem']);
        $this->agent->succeed('ssl.renewal_status', ['certbot_installed' => true, 'timer_active' => true, 'method' => 'systemd']);
        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->siteId . '/ssl')->body();
        self::assertStringContainsString('cust@example.test', $page);
        self::assertStringNotContainsString('name="email"', $page, 'no email field');
        $this->post('/sites/' . $this->siteId . '/ssl/issue', ['email' => 'someone-else@example.test']);
        self::assertSame('cust@example.test', $this->lastCall('ssl.issue')['email']);
    }

    public function testDatabaseUsersGetRemoteAccess(): void
    {
        $this->login('cust');
        foreach (['ip' => '203.0.113.%', 'any' => '%'] as $choice => $want) {
            $this->post('/database-users', ['username' => 'app' . $choice, 'host_choice' => $choice, 'host_ip' => '203.0.113.%', 'return_to' => '/databases']);
            self::assertSame($want, $this->lastCall('db.user_create')['host'], $choice);
        }
        $this->post('/database-users', ['username' => 'local', 'host_choice' => 'localhost', 'host_ip' => '203.0.113.%']);
        self::assertSame('localhost', $this->lastCall('db.user_create')['host']);

        // Customers may not use host names or other patterns.
        $before = $this->calls('db.user_create');
        foreach (['evil.example.com', '%.example.com', '10.0.0.0/8', "1.2.3.4'--", '1.2.%.4'] as $bad) {
            $this->post('/database-users', ['username' => 'bad', 'host_choice' => 'ip', 'host_ip' => $bad]);
        }
        self::assertSame($before, $this->calls('db.user_create'));

        // Remote access for an existing user: another login with the same database access.
        $db = $this->insertDatabase('cust_shop', $this->custId);
        $user = (int) $this->db->scalar("SELECT id FROM database_users WHERE host = 'localhost'");
        $this->db->run("INSERT INTO database_grants (database_id, user_id, privileges) VALUES (?, ?, 'all')", [$db, $user]);
        $this->post('/database-users/' . $user . '/hosts', ['host_choice' => 'ip', 'host_ip' => '198.51.100.7']);
        self::assertSame('198.51.100.7', $this->lastCall('db.user_create')['host']);
        self::assertSame(['user' => $this->lastCall('db.user_create')['user'], 'host' => '198.51.100.7', 'database' => 'cust_shop', 'privileges' => 'all', 'revoke' => false], $this->lastCall('db.grant'));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM database_grants g JOIN database_users u ON u.id = g.user_id WHERE u.host = '198.51.100.7'"));
        self::assertNotNull($this->flash('secret'), 'the new password is shown once');
    }

    public function testDatabaseUsersAreAssignedToTheAccountsDatabases(): void
    {
        $mine = $this->insertDatabase('cust_blog', $this->custId);
        $theirs = $this->insertDatabase('admin_db', null);
        $this->login('cust');
        $this->post('/database-users', ['username' => 'blog', 'host_choice' => 'localhost']);
        $user = (int) $this->db->scalar("SELECT id FROM database_users WHERE username LIKE '%blog'");

        $page = $this->request('GET', '/databases')->body();
        self::assertStringContainsString('Assign to database', $page);
        $this->post('/database-users/' . $user . '/assign', ['database_id' => (string) $mine, 'privileges' => 'readonly', 'return_to' => '/databases']);
        self::assertSame(['database' => 'cust_blog', 'privileges' => 'readonly'], array_intersect_key($this->lastCall('db.grant'), ['database' => 1, 'privileges' => 1]));

        // Not to a database of another account.
        $before = $this->calls('db.grant');
        $this->post('/database-users/' . $user . '/assign', ['database_id' => (string) $theirs, 'privileges' => 'all']);
        self::assertSame($before, $this->calls('db.grant'));
    }

    public function testSqlFilesAreImportedFromTheWebsite(): void
    {
        $this->agent->succeed('db.import_site', ['job_id' => 'dddddddddddddddd']);
        $this->db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, ?, ?, 'cust_shop', '2026-01-01')", [$this->siteId, $this->custId]);
        $mine = (int) $this->db->scalar("SELECT id FROM databases WHERE name = 'cust_shop'");
        $loose = $this->insertDatabase('cust_loose', $this->custId);
        $this->db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, ?, NULL, 'admin_db', '2026-01-01')", [$this->otherId]);
        $theirs = (int) $this->db->scalar("SELECT id FROM databases WHERE name = 'admin_db'");
        $this->login('cust');
        self::assertStringContainsString('/databases/' . $mine . '/import', $this->request('GET', '/sites/' . $this->siteId . '/databases')->body());

        // An upload the panel named is removed afterwards; any other file is not.
        $this->post('/databases/' . $mine . '/import', ['path' => 'tmp/whacp-import-abcdefghij012345.sql', 'uploaded' => '1']);
        self::assertSame(['name' => 'cust_shop', 'domain' => 'shop.test', 'path' => 'tmp/whacp-import-abcdefghij012345.sql', 'remove_after' => true], $this->lastCall('db.import_site'));
        self::assertSame('dddddddddddddddd', $this->flash('job'));
        $this->post('/databases/' . $mine . '/import', ['path' => 'backups/site.sql.gz', 'uploaded' => '1']);
        self::assertFalse($this->lastCall('db.import_site')['remove_after'], 'only the panel\'s own upload is removed');

        $before = $this->calls('db.import_site');
        foreach (['../other.test/x.sql', 'public_html/index.php', 'tmp/x.sql.zip', ''] as $bad) {
            $this->post('/databases/' . $mine . '/import', ['path' => $bad]);
        }
        $this->post('/databases/' . $loose . '/import', ['path' => 'x.sql']);
        $this->post('/databases/' . $theirs . '/import', ['path' => 'x.sql']);
        self::assertSame($before, $this->calls('db.import_site'), 'refused paths, a database without website, another account\'s database');
    }

    private function insertDatabase(string $name, ?int $owner): int
    {
        $this->db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, NULL, ?, ?, '2026-01-01')", [$owner, $name]);
        return (int) $this->db->scalar('SELECT id FROM databases WHERE name = ?', [$name]);
    }
}
