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

    public function testZonesCreatedByDomainNameBelongToThatWebsite(): void
    {
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->agent->succeed('mail.domain_add', ['domain' => 'x', 'dkim_selector' => 'whacp202609', 'dkim_txt' => 'v=DKIM1; k=rsa; p=MIIB']);
        $this->login('admin');
        // The administrator types the domain on the DNS and Email pages
        // without choosing the website.
        $this->post('/dns/zones', ['name' => 'shop.test', 'template' => '1']);
        $this->post('/mail/domains', ['domain' => 'shop.test']);
        $zone = $this->db->fetch("SELECT id, website_id, owner_id FROM dns_zones WHERE name = 'shop.test'");
        $mail = $this->db->fetch("SELECT id, website_id, owner_id FROM mail_domains WHERE domain = 'shop.test'");
        self::assertSame([$this->siteId, $this->custId], [(int) $zone['website_id'], (int) $zone['owner_id']]);
        self::assertSame([$this->siteId, $this->custId], [(int) $mail['website_id'], (int) $mail['owner_id']]);
        // A zone for another name stays unlinked.
        $this->post('/dns/zones', ['name' => 'unrelated.test', 'template' => '0']);
        self::assertNull($this->db->scalar("SELECT website_id FROM dns_zones WHERE name = 'unrelated.test'"));

        // A zone that exists before its website is linked when the website is created.
        $this->post('/dns/zones', ['name' => 'later.test', 'template' => '0']);
        $users = $this->app->container->get(UserRepository::class);
        $later = $this->app->container->get(WebsiteService::class)->create(['domain' => 'later.test', 'kind' => 'static'], (array) $users->find($this->custId));
        self::assertSame($later, (int) $this->db->scalar("SELECT website_id FROM dns_zones WHERE name = 'later.test'"));

        $this->switchTo('cust');
        self::assertSame('/dns/zones/' . (int) $zone['id'], $this->request('GET', '/sites/' . $this->siteId . '/dns')->header('Location'));
        self::assertSame(200, $this->request('GET', '/dns/zones/' . (int) $zone['id'])->status());
        self::assertSame('/mail/domains/' . (int) $mail['id'], $this->request('GET', '/sites/' . $this->siteId . '/mail')->header('Location'));
        $this->switchTo('mallory');
        self::assertSame(404, $this->request('GET', '/dns/zones/' . (int) $zone['id'])->status());
    }

    public function testMigrationLinksExistingZonesToTheirWebsite(): void
    {
        $now = gmdate('c');
        $mallory = (int) $this->db->scalar("SELECT id FROM users WHERE username = 'mallory'");
        foreach ([['shop.test', null], ['other.test', $mallory], ['nosite.test', null]] as [$name, $owner]) {
            $this->db->run("INSERT INTO dns_zones (server_id, name, website_id, owner_id, primary_ns, hostmaster, created_at, updated_at) VALUES (1, ?, NULL, ?, 'ns1.host.test', 'h@host.test', ?, ?)", [$name, $owner, $now, $now]);
        }
        $this->db->run("INSERT INTO mail_domains (server_id, domain, website_id, owner_id, dkim_selector, dkim_txt, created_at) VALUES (1, 'shop.test', NULL, NULL, 's', 't', ?)", [$now]);
        foreach (require __DIR__ . '/../../database/migrations/0020_link_zones_to_websites.php' as $sql) {
            $this->db->run($sql);
        }
        $row = fn (string $name): array => (array) $this->db->fetch('SELECT website_id, owner_id FROM dns_zones WHERE name = ?', [$name]);
        self::assertSame([$this->siteId, $this->custId], array_map('intval', array_values($row('shop.test'))), 'the whasols.com case: an unowned zone for a website');
        self::assertNull($row('other.test')['website_id'], 'a zone another account owns is not handed over');
        self::assertSame($mallory, (int) $row('other.test')['owner_id']);
        self::assertNull($row('nosite.test')['website_id']);
        self::assertSame($this->siteId, (int) $this->db->scalar("SELECT website_id FROM mail_domains WHERE domain = 'shop.test'"));
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

    public function testAdministratorsCanKeepDatabaseUsersLocal(): void
    {
        $this->app->container->get(SettingsService::class)->update(['db_customer_remote_hosts' => '0'], 1);
        $this->login('cust');
        $page = $this->request('GET', '/databases')->body();
        self::assertStringNotContainsString('value="any"', $page, 'no remote choice offered');
        self::assertStringNotContainsString('data-wcp-remote', $page);
        $before = $this->calls('db.user_create');
        $this->post('/database-users', ['username' => 'far', 'host_choice' => 'any', 'return_to' => '/databases']);
        self::assertSame($before, $this->calls('db.user_create'), 'refused');
        $this->post('/database-users', ['username' => 'near', 'host_choice' => 'localhost', 'return_to' => '/databases']);
        self::assertSame('localhost', $this->lastCall('db.user_create')['host']);
        $user = (int) $this->db->scalar("SELECT id FROM database_users WHERE host = 'localhost'");
        $before = $this->calls('db.user_create');
        $this->post('/database-users/' . $user . '/hosts', ['host_choice' => 'ip', 'host_ip' => '198.51.100.7']);
        self::assertSame($before, $this->calls('db.user_create'), 'no remote login added');

        // Administrators still can.
        $this->switchTo('admin');
        $this->post('/database-users', ['username' => 'ops', 'host_choice' => 'ip', 'host_ip' => '198.51.100.9', 'return_to' => '/databases']);
        self::assertSame('198.51.100.9', $this->lastCall('db.user_create')['host']);
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

    public function testCronRunsAreParsedFromTheLog(): void
    {
        $log = "old output\n### whacp-run start 2026-09-27T10:00:00Z\nhello\n### whacp-run end 2026-09-27T10:00:05Z exit=0\n"
            . "### whacp-run start 2026-09-27T11:00:00Z manual\nboom\nline 2\n### whacp-run end 2026-09-27T11:02:00Z exit=3\n"
            . "### whacp-run start 2026-09-27T12:00:00Z\nstill going\n";
        $runs = \WhaCP\Services\CronService::parseRuns($log);
        self::assertCount(4, $runs);
        self::assertSame(['start' => '2026-09-27T12:00:00Z', 'end' => null, 'exit' => null, 'manual' => false, 'output' => 'still going'], $runs[0], 'newest first, unfinished');
        self::assertSame(['start' => '2026-09-27T11:00:00Z', 'end' => '2026-09-27T11:02:00Z', 'exit' => 3, 'manual' => true, 'output' => "boom\nline 2"], $runs[1]);
        self::assertSame(0, $runs[2]['exit']);
        self::assertSame(['start' => null, 'end' => null, 'exit' => null, 'manual' => false, 'output' => 'old output'], $runs[3], 'output from before the framing');
    }

    public function testCronOutputEmailAndLogPage(): void
    {
        $this->agent->succeed('cron.apply', ['changed' => true]);
        $base = '/sites/' . $this->siteId . '/cron';
        $this->login('cust');
        $this->post($base, ['schedule' => '@daily', 'command' => 'php x.php', 'enabled' => '1', 'email_output' => '1']);
        self::assertSame('cust@example.test', $this->lastCall('cron.apply')['jobs'][0]['email']);
        $job = (int) $this->lastCall('cron.apply')['jobs'][0]['id'];

        // An administrator editing the job keeps the recipient.
        $this->switchTo('admin');
        $this->post($base, ['job_id' => (string) $job, 'schedule' => '@hourly', 'command' => 'php x.php', 'enabled' => '1', 'email_output' => '1']);
        self::assertSame('cust@example.test', $this->lastCall('cron.apply')['jobs'][0]['email']);
        $this->post($base, ['job_id' => (string) $job, 'schedule' => '@hourly', 'command' => 'php x.php', 'enabled' => '1', 'email_output' => '0']);
        self::assertSame('', $this->lastCall('cron.apply')['jobs'][0]['email']);

        $this->agent->succeed('cron.log', ['content' => "### whacp-run start 2026-09-27T10:00:00Z\nDone <b>\n### whacp-run end 2026-09-27T10:01:30Z exit=0\n", 'size' => 90, 'truncated' => false]);
        $page = $this->request('GET', $base . '/' . $job . '/log')->body();
        foreach (['2026-09-27 10:00:00 UTC', '1 min 30 s', 'Success', 'Scheduled', 'Done &lt;b&gt;'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertSame(['domain' => 'shop.test', 'root' => '/home/whacp/shop.test', 'id' => $job], $this->lastCall('cron.log'));
        // Not for another customer's website.
        $this->switchTo('mallory');
        self::assertSame(404, $this->request('GET', $base . '/' . $job . '/log')->status());
    }

    public function testSubdomainsAndAddonDomainsShareTheWebsitesAccount(): void
    {
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('dns.status', ['installed' => true, 'configured' => true, 'running' => true, 'zones' => []]);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->login('cust');
        $this->post('/dns/zones', ['website_id' => (string) $this->siteId]);
        $user = (string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$this->siteId]);
        $sub = '/sites/' . $this->siteId . '/subdomains';
        $addon = '/sites/' . $this->siteId . '/addons';
        self::assertSame(200, $this->request('GET', $sub)->status());
        self::assertSame(200, $this->request('GET', $addon)->status());

        $this->post($sub, ['type' => 'subdomain', 'subdomain' => 'blog', 'dns' => '1']);
        $apply = $this->lastCall('sites.apply');
        self::assertSame(['blog.shop.test', $user], [$apply['domain'], $apply['user']]);
        self::assertArrayNotHasKey('new_user', $apply, "the parent's account is reused");
        $child = $this->db->fetch("SELECT * FROM websites WHERE domain = 'blog.shop.test'");
        self::assertSame([$this->siteId, $this->custId], [(int) $child['parent_id'], (int) $child['owner_id']]);
        self::assertContains(['name' => 'blog', 'type' => 'A', 'ttl' => 0, 'value' => '203.0.113.10', 'priority' => 0, 'weight' => 0, 'port' => 0], $this->lastCall('dns.zone_apply')['records']);

        $this->post($addon, ['type' => 'addon', 'domain' => 'other-shop.test', 'www' => '1']);
        $apply = $this->lastCall('sites.apply');
        self::assertSame(['other-shop.test', $user, ['www.other-shop.test']], [$apply['domain'], $apply['user'], $apply['aliases']]);
        self::assertStringContainsString('blog.shop.test', $this->request('GET', $sub)->body());
        self::assertStringContainsString('other-shop.test', $this->request('GET', $addon)->body());

        // Refused: bad labels, a subdomain as addon, someone else's website.
        $before = $this->calls('sites.apply');
        $this->post($sub, ['type' => 'subdomain', 'subdomain' => 'bad_label!']);
        $this->post($sub, ['type' => 'subdomain', 'subdomain' => 'www']);
        $this->post($addon, ['type' => 'addon', 'domain' => 'x.shop.test']);
        $this->switchTo('mallory');
        self::assertSame(404, $this->request('GET', $sub)->status());
        self::assertSame(404, $this->request('GET', $addon)->status());
        $this->post($sub, ['type' => 'subdomain', 'subdomain' => 'evil']);
        self::assertSame($before, $this->calls('sites.apply'));
    }

    public function testCustomersCannotRunAWebsiteAsAnotherAccountsUser(): void
    {
        $victim = (string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$this->siteId]);
        $admins = (string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$this->otherId]);
        $this->switchTo('mallory');
        $before = $this->calls('sites.apply');
        foreach ([$victim, $admins] as $i => $taken) {
            $this->post('/websites/new', ['domain' => 'steal' . $i . '.test', 'kind' => 'static', 'system_user' => $taken]);
            self::assertStringContainsString('belongs to another account', (string) ((array) $this->flash('errors'))['system_user']);
        }
        self::assertSame($before, $this->calls('sites.apply'));

        // A new name must not exist on the server: the agent is told so.
        $this->post('/websites/new', ['domain' => 'mine.test', 'kind' => 'static', 'system_user' => 'ubuntu']);
        self::assertTrue($this->lastCall('sites.apply')['new_user'] ?? false);

        // Administrators may still attach an existing account.
        $this->switchTo('admin');
        $this->post('/websites/new', ['domain' => 'adm.test', 'kind' => 'static', 'system_user' => $victim]);
        self::assertSame($victim, $this->lastCall('sites.apply')['user']);
        self::assertArrayNotHasKey('new_user', $this->lastCall('sites.apply'));
    }

    public function testDeletingAWebsiteKeepsAnAccountOtherWebsitesUse(): void
    {
        $this->agent->succeed('sites.delete', ['warnings' => []]);
        $this->login('cust');
        $this->post('/sites/' . $this->siteId . '/addons', ['type' => 'addon', 'domain' => 'static-sibling.test']);
        $child = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'static-sibling.test'");
        $this->post('/sites/' . $child . '/delete', ['confirm_domain' => 'static-sibling.test', 'remove_user' => '1']);
        self::assertFalse($this->lastCall('sites.delete')['remove_user'], 'the parent still runs as that account');
        self::assertStringContainsString('was kept', (string) $this->flash('success'));
    }

    private function insertDatabase(string $name, ?int $owner): int
    {
        $this->db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, NULL, ?, ?, '2026-01-01')", [$owner, $name]);
        return (int) $this->db->scalar('SELECT id FROM databases WHERE name = ?', [$name]);
    }
}
