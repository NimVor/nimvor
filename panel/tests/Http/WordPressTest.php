<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Tests\Support\HttpTestCase;

/** WordPress toolkit: scans, overview and checks, operations, sign-in links, who may do what. */
final class WordPressTest extends HttpTestCase
{
    private int $siteId;
    private int $blogId;
    private int $subId;
    private int $custId;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->custId = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $db = $this->app->container->get(Connection::class);
        $add = static function (string $domain, string $root, string $doc, string $user, int $owner, ?int $parent = null) use ($db): int {
            $db->run("INSERT INTO websites (server_id, domain, kind, php_version, https, root, document_root, system_user, owner_id, parent_id, status, created_at, updated_at) VALUES (1, ?, 'php', '8.3', 1, ?, ?, ?, ?, ?, 'active', 'x', 'x')", [$domain, $root, $doc, $user, $owner, $parent]);
            return (int) $db->lastInsertId();
        };
        $this->siteId = $add('shop.test', '/home/whacp/shop.test', '/home/whacp/shop.test/public_html', 'shop', $this->custId);
        $this->subId = $add('blog.shop.test', '/home/whacp/shop.test/blog.shop.test', '/home/whacp/shop.test/blog.shop.test', 'shop', $this->custId, $this->siteId);
        $this->blogId = $add('other.test', '/home/whacp/other.test', '/home/whacp/other.test/public_html', 'other', $other);
        $this->agent->succeed('wordpress.scan', ['latest' => '6.9', 'wp_cli' => true, 'errors' => [], 'installs' => [
            ['domain' => 'shop.test', 'path' => 'public_html', 'version' => '6.8', 'db_name' => 'shop_wp', 'db_host' => 'localhost', 'table_prefix' => 'wp_', 'debug' => true, 'file_edit_disabled' => false, 'config_mode' => '0644', 'maintenance' => false, 'configured' => true],
            ['domain' => 'shop.test', 'path' => 'blog.shop.test', 'version' => '6.9', 'db_name' => 'blog_wp', 'db_host' => 'localhost', 'table_prefix' => 'wp_', 'debug' => false, 'file_edit_disabled' => true, 'config_mode' => '0600', 'maintenance' => false, 'configured' => true],
            ['domain' => 'other.test', 'path' => 'public_html', 'version' => '6.1', 'db_name' => 'x', 'db_host' => 'localhost', 'table_prefix' => 'wp_', 'debug' => false, 'file_edit_disabled' => true, 'config_mode' => '0600', 'maintenance' => false, 'configured' => true],
        ]]);
        $this->agent->succeed('wordpress.info', [
            'version' => '6.8', 'php' => '8.3.6', 'mysql' => '10.11', 'multisite' => false, 'siteurl' => 'http://shop.test', 'home' => 'http://shop.test', 'title' => 'Shop <script>x</script>',
            'search_engines' => true, 'core' => ['latest' => '6.9', 'php_needed' => '7.2.24', 'checked_at' => 1790000000],
            'plugins' => [['name' => 'akismet', 'title' => 'Akismet Anti-spam', 'version' => '5.3', 'active' => false, 'update' => '5.4', 'auto_update' => false]],
            'themes' => [['name' => 'twentytwentyfour', 'title' => 'Twenty Twenty-Four', 'version' => '1.0', 'active' => true, 'update' => '', 'auto_update' => false]],
            'admins' => [['id' => 1, 'login' => 'owner'], ['id' => 4, 'login' => 'editor-in-chief']], 'admin_login_exists' => true,
            'constants' => ['WP_DEBUG' => true, 'DISALLOW_FILE_EDIT' => false, 'WP_AUTO_UPDATE_CORE' => null],
            'maintenance' => false, 'db' => ['name' => 'shop_wp', 'host' => 'localhost', 'prefix' => 'wp_', 'tables' => 12, 'bytes' => 2097152], 'config_mode' => '0644',
        ]);
        $this->agent->succeed('wordpress.manage', ['op' => 'plugin_activate']);
        $this->agent->succeed('wordpress.login', ['file' => 'whacp-login-' . str_repeat('ab', 16) . '.php', 'expires_at' => '2026-09-28T12:01:00Z']);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    private function installId(string $path, int $site): int
    {
        return (int) $this->app->container->get(Connection::class)->scalar('SELECT id FROM wordpress_installs WHERE website_id = ? AND path = ?', [$site, $path]);
    }

    public function testOwnerScansManagesAndSignsIn(): void
    {
        $this->login('cust');
        $this->post('/sites/' . $this->siteId . '/wordpress/scan');
        $sent = $this->lastCall('wordpress.scan')['sites'];
        self::assertSame([['domain' => 'shop.test', 'user' => 'shop', 'root' => '/home/whacp/shop.test', 'php_version' => '8.3']], $sent, 'own folder once (the subdomain is inside it); never other accounts');
        $db = $this->app->container->get(Connection::class);
        self::assertSame(0, (int) $db->scalar('SELECT COUNT(*) FROM wordpress_installs WHERE website_id = ?', [$this->blogId]), 'another account\'s website is never recorded from this scan');
        $main = $this->installId('public_html', $this->siteId);
        $sub = $this->installId('', $this->subId);
        self::assertGreaterThan(0, $main);
        self::assertGreaterThan(0, $sub, 'the subdomain\'s WordPress belongs to the subdomain website');

        // One installation: the website's page opens it; ?list=1 lists it.
        self::assertSame('/sites/' . $this->siteId . '/wordpress/' . $main, $this->request('GET', '/sites/' . $this->siteId . '/wordpress')->header('Location'));
        self::assertStringContainsString('Scan for WordPress', $this->request('GET', '/sites/' . $this->siteId . '/wordpress', [], [], '', ['list' => '1'])->body());

        $page = $this->request('GET', '/sites/' . $this->siteId . '/wordpress/' . $main)->body();
        foreach (['WordPress 6.9 is available', 'Debugging is on', 'The theme and plugin file editor is on', 'wp-config.php is readable by others (0644)', 'A user is called', 'WordPress uses http://', 'Akismet Anti-spam', '5.4 available', 'Twenty Twenty-Four', 'Log in to WordPress', 'editor-in-chief', 'shop_wp'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('<script>x</script>', $page, 'what WordPress reports is escaped');
        $row = $db->fetch('SELECT * FROM wordpress_installs WHERE id = ?', [$main]);
        self::assertSame(1, (int) $row['plugin_updates']);
        self::assertGreaterThanOrEqual(5, (int) $row['issues']);

        $this->post('/sites/' . $this->siteId . '/wordpress/' . $main . '/manage', ['op' => 'plugin_activate', 'items' => ['akismet', '--require=/tmp/x.php']]);
        self::assertSame(['domain' => 'shop.test', 'user' => 'shop', 'root' => '/home/whacp/shop.test', 'php_version' => '8.3', 'path' => 'public_html', 'op' => 'plugin_activate', 'items' => ['akismet']], $this->lastCall('wordpress.manage'));
        $this->post('/sites/' . $this->siteId . '/wordpress/' . $main . '/manage', ['op' => 'eval', 'items' => ['phpinfo()']]);
        $this->post('/sites/' . $this->siteId . '/wordpress/' . $main . '/manage', ['op' => 'core_auto_update', 'value' => 'always']);
        self::assertSame(1, $this->calls('wordpress.manage'), 'unknown operations and values never reach the agent');
        $this->agent->succeed('wordpress.manage', ['job_id' => 'abcdef0123456789']);
        $this->post('/sites/' . $this->siteId . '/wordpress/' . $main . '/manage', ['op' => 'core_update']);
        self::assertSame('abcdef0123456789', $this->flash('job'));
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM agent_jobs WHERE job_id = 'abcdef0123456789' AND target_type = 'website'"));

        // One-time sign-in: built from the website's own domain, audited
        // without the secret file name.
        $res = $this->post('/sites/' . $this->siteId . '/wordpress/' . $main . '/login', ['admin_id' => '4']);
        self::assertSame('https://shop.test/whacp-login-' . str_repeat('ab', 16) . '.php', $res->header('Location'));
        self::assertSame(4, $this->lastCall('wordpress.login')['admin_id']);
        $audit = (string) json_encode($db->fetchAll("SELECT * FROM audit_logs WHERE action LIKE 'wordpress.%'"));
        self::assertStringContainsString('wordpress.login', $audit);
        self::assertStringNotContainsString(str_repeat('ab', 16), $audit);
    }

    public function testOwnersCannotReachOtherAccounts(): void
    {
        $this->switchTo('admin');
        $this->post('/wordpress/scan');
        self::assertCount(2, $this->lastCall('wordpress.scan')['sites'], 'administrators scan every top-level folder');
        $theirs = $this->installId('public_html', $this->blogId);
        $mine = $this->installId('public_html', $this->siteId);
        self::assertGreaterThan(0, $theirs);

        $this->switchTo('cust');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->blogId . '/wordpress/' . $theirs)->status());
        // Their installation through my website's address: not found.
        $this->request('GET', '/sites/' . $this->siteId . '/wordpress/' . $theirs);
        $this->post('/sites/' . $this->siteId . '/wordpress/' . $theirs . '/manage', ['op' => 'maintenance_on']);
        $this->post('/sites/' . $this->blogId . '/wordpress/' . $theirs . '/manage', ['op' => 'maintenance_on']);
        $this->post('/sites/' . $this->siteId . '/wordpress/' . $theirs . '/login', ['admin_id' => '1']);
        self::assertSame(0, $this->calls('wordpress.manage') + $this->calls('wordpress.login'));
        $list = $this->request('GET', '/wordpress')->body();
        self::assertStringContainsString('shop.test', $list);
        self::assertStringNotContainsString('other.test', $list);
        self::assertGreaterThan(0, $mine);
    }

    public function testFeatureManagerSuspensionAndHttpsRules(): void
    {
        $this->login('cust');
        $this->post('/sites/' . $this->siteId . '/wordpress/scan');
        $main = $this->installId('public_html', $this->siteId);
        $db = $this->app->container->get(Connection::class);

        // No one-click sign-in without HTTPS.
        $db->run('UPDATE websites SET https = 0 WHERE id = ?', [$this->siteId]);
        $this->post('/sites/' . $this->siteId . '/wordpress/' . $main . '/login', ['admin_id' => '1']);
        self::assertSame(0, $this->calls('wordpress.login'));

        // A suspended account can look, not change.
        $db->run("UPDATE users SET suspended_at = 'x' WHERE id = ?", [$this->custId]);
        $this->switchTo('admin');
        $this->post('/sites/' . $this->siteId . '/wordpress/' . $main . '/manage', ['op' => 'maintenance_on']);
        self::assertSame(0, $this->calls('wordpress.manage'));
        $db->run('UPDATE users SET suspended_at = NULL WHERE id = ?', [$this->custId]);

        // A plan without the toolkit: no page, no actions, no menu entry.
        $list = $this->app->container->get(\WhaCP\Services\FeatureService::class)->save(null, ['name' => 'No WP', 'features' => array_values(array_diff(array_keys(\WhaCP\Services\FeatureService::FEATURES), ['wordpress']))], 1);
        $db->run("INSERT INTO plans (name, feature_list_id, created_at, updated_at) VALUES ('Basic', ?, 'x', 'x')", [$list]);
        $db->run('UPDATE users SET plan_id = ? WHERE id = ?', [(int) $db->lastInsertId(), $this->custId]);
        $this->switchTo('cust');
        self::assertNotSame(200, $this->request('GET', '/sites/' . $this->siteId . '/wordpress/' . $main)->status());
        self::assertNotSame(200, $this->request('GET', '/wordpress')->status());
        $this->post('/sites/' . $this->siteId . '/wordpress/' . $main . '/manage', ['op' => 'maintenance_on']);
        $calls = $this->calls('wordpress.scan');
        $this->post('/sites/' . $this->siteId . '/wordpress/scan');
        self::assertSame(0, $this->calls('wordpress.manage'));
        self::assertSame($calls, $this->calls('wordpress.scan'));
        self::assertStringNotContainsString('/wordpress"', $this->request('GET', '/sites/' . $this->siteId)->body());
    }

    public function testUnreachableWordPressOrgIsSaid(): void
    {
        $this->login('admin');
        $this->post('/wordpress/scan');
        $info = (array) json_decode((string) json_encode(json_decode((string) $this->agent->responses['/v1/actions/wordpress.info']->body, true)['data']), true);
        $info['core'] = ['latest' => '', 'php_needed' => '', 'checked_at' => 1790000000];
        $this->agent->succeed('wordpress.info', $info);
        $page = $this->request('GET', '/sites/' . $this->siteId . '/wordpress/' . $this->installId('public_html', $this->siteId))->body();
        self::assertStringContainsString('Updates could not be checked', $page);
        self::assertStringContainsString('WordPress.org not reachable', $page);
    }

    public function testAdministratorsSeeOutdatedInstallations(): void
    {
        $this->login('admin');
        $this->post('/wordpress/scan');
        $page = $this->request('GET', '/wordpress', [], [], '', ['filter' => 'outdated'])->body();
        self::assertStringContainsString('shop.test', $page);
        self::assertStringContainsString('6.9 available', $page);
        self::assertStringContainsString('other.test', $page, '6.1 is older than 6.9');
        self::assertStringNotContainsString('blog.shop.test', $page, 'up to date');
        // A rescan that no longer finds an installation forgets it.
        $this->agent->succeed('wordpress.scan', ['latest' => '6.9', 'errors' => [], 'installs' => []]);
        $this->post('/wordpress/scan');
        self::assertSame(0, (int) $this->app->container->get(Connection::class)->scalar('SELECT COUNT(*) FROM wordpress_installs'));
    }
}
