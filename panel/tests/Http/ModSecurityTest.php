<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Tests\Support\HttpTestCase;

/** ModSecurity: administrators manage the rules; owners switch their website's mode only. */
final class ModSecurityTest extends HttpTestCase
{
    private int $siteId;
    private int $otherSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('resell', 'reseller');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $db = $this->app->container->get(Connection::class);
        foreach ([['shop.test', $cust], ['blog.test', $other]] as [$d, $o]) {
            $db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, ?, 'static', ?, ?, ?, ?, 'active', 'x', 'x')", [$d, '/home/whacp/' . $d, '/home/whacp/' . $d . '/public_html', explode('.', $d)[0], $o]);
        }
        $this->siteId = (int) $db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->otherSite = (int) $db->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        $this->agent->succeed('modsec.status', [
            'module_loaded' => true, 'active' => true, 'packages_available' => true, 'audit_log_bytes' => 2048, 'custom_rules' => 1,
            'crs' => ['available' => true, 'version' => '3.3.5', 'rules_dir' => '/usr/share/modsecurity-crs/rules'],
            'config' => ['enabled' => true, 'crs' => ['enabled' => true, 'paranoia' => 1, 'inbound_threshold' => 5, 'outbound_threshold' => 4], 'custom' => ['enabled' => true]],
            'vendors' => [
                ['id' => 'crs', 'name' => 'OWASP Core Rule Set', 'description' => 'Generic protection', 'first_id' => 900000, 'last_id' => 999999],
                ['id' => 'custom', 'name' => 'Custom rules', 'description' => 'Yours', 'first_id' => 10000, 'last_id' => 99999],
            ],
            'sites' => ['shop.test' => ['mode' => 'detect', 'exclude' => ['custom'], 'disabled_rules' => [920350]]],
        ]);
        $this->agent->succeed('modsec.events', ['events' => [
            ['time' => '2026-09-28T17:26:00Z', 'host' => 'shop.test', 'client_ip' => '198.51.100.7', 'method' => 'GET', 'uri' => '/?id=1%27%20or%201=1--', 'status' => 403, 'engine' => 'Enabled',
                'rules' => [['id' => '942100', 'message' => 'SQL Injection Attack Detected via libinjection'], ['id' => '949110', 'message' => 'Inbound Anomaly Score Exceeded']]],
        ]]);
        $this->agent->succeed('modsec.custom_get', ['rules' => "SecRule ARGS:probe \"@streq yes\" \"id:10001,deny\"\n"]);
        foreach (['modsec.configure', 'modsec.site', 'modsec.custom_set'] as $a) {
            $this->agent->succeed($a, ['rules' => 1]);
        }
        $this->agent->succeed('modsec.update', ['job_id' => 'abcdef0123456789']);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    public function testAdministratorsManageRulesAndWebsites(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/security/modsec')->body();
        foreach (['Nginx connector working', '3.3.5', 'Rule vendors', 'shop.test', 'Detection only', '920350', 'SQL Injection Attack Detected', 'Blocked (403)', 'id:10001'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('Inbound Anomaly Score Exceeded', $page, 'score summaries are hidden');

        $this->post('/security/modsec', ['enabled' => '1', 'crs_enabled' => '1', 'paranoia' => '2', 'inbound_threshold' => '10', 'outbound_threshold' => '4', 'custom_enabled' => '0']);
        self::assertSame(['enabled' => true, 'crs' => ['enabled' => true, 'paranoia' => 2, 'inbound_threshold' => 10, 'outbound_threshold' => 4], 'custom' => ['enabled' => false], 'detection_only' => false], $this->lastCall('modsec.configure'));
        $this->post('/security/modsec', ['enabled' => '1', 'crs_enabled' => '1', 'detection_only' => '1', 'paranoia' => '1', 'inbound_threshold' => '5', 'outbound_threshold' => '4']);
        self::assertTrue($this->lastCall('modsec.configure')['detection_only'], 'blocking is allowed only when the switch is off');
        $this->post('/security/modsec', ['enabled' => '1', 'paranoia' => '9']);
        self::assertSame(2, $this->calls('modsec.configure'), 'invalid paranoia level refused in the panel');

        $this->post('/security/modsec/sites/' . $this->otherSite, ['mode' => 'on', 'vendors' => ['crs'], 'disabled_rules' => '942100, 941100 942100']);
        self::assertSame(['domain' => 'blog.test', 'mode' => 'on', 'exclude' => ['custom'], 'disabled_rules' => [942100, 941100]], $this->lastCall('modsec.site'));
        $this->post('/security/modsec/sites/' . $this->otherSite, ['mode' => 'on', 'disabled_rules' => '94x']);
        self::assertSame(1, $this->calls('modsec.site'));

        $this->post('/security/modsec/custom', ['rules' => "SecAction \"id:10002,pass\"\r\n"]);
        self::assertSame(['rules' => "SecAction \"id:10002,pass\"\n"], $this->lastCall('modsec.custom_set'));

        $this->post('/security/modsec/update');
        self::assertSame(1, $this->calls('modsec.update'));
        $db = $this->app->container->get(Connection::class);
        self::assertSame(5, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'modsec.%'"), 'settings saves, website setting, custom rules, update');

        // An administrator turns a rule off for a website from its page.
        $page = $this->request('GET', '/sites/' . $this->siteId . '/firewall')->body();
        self::assertStringContainsString('Turn off here', $page);
        $this->post('/sites/' . $this->siteId . '/firewall', ['disable_rule' => '942100']);
        self::assertSame(['domain' => 'shop.test', 'mode' => 'detect', 'exclude' => ['custom'], 'disabled_rules' => [920350, 942100]], $this->lastCall('modsec.site'));
    }

    public function testOwnersSwitchOnlyTheirWebsitesMode(): void
    {
        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->siteId . '/firewall')->body();
        foreach (['Web firewall (ModSecurity)', 'Detection only', '920350', 'SQL Injection Attack Detected', 'ask your hosting provider'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('Turn off here', $page);
        self::assertSame(['limit' => 50, 'hosts' => ['shop.test', 'www.shop.test']], $this->lastCall('modsec.events'), 'events of their own website only');

        // Mode only: vendors and rules stay as the administrators set them.
        $this->post('/sites/' . $this->siteId . '/firewall', ['mode' => 'on', 'disable_rule' => '942100', 'vendors' => []]);
        self::assertSame(['domain' => 'shop.test', 'mode' => 'on', 'exclude' => ['custom'], 'disabled_rules' => [920350]], $this->lastCall('modsec.site'));

        // Not someone else's website, not the server-wide pages.
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite . '/firewall')->status());
        $this->post('/sites/' . $this->otherSite . '/firewall', ['mode' => 'off']);
        self::assertNotSame(200, $this->request('GET', '/security/modsec')->status());
        $this->post('/security/modsec', ['enabled' => '0']);
        $this->post('/security/modsec/custom', ['rules' => '']);
        $this->post('/security/modsec/sites/' . $this->siteId, ['mode' => 'off']);
        self::assertSame(1, $this->calls('modsec.site'));
        self::assertSame(0, $this->calls('modsec.configure') + $this->calls('modsec.custom_set'));
    }

    public function testResellersCannotChangeServerRules(): void
    {
        $this->switchTo('resell');
        self::assertNotSame(200, $this->request('GET', '/security/modsec')->status());
        $this->post('/security/modsec', ['enabled' => '0']);
        $this->post('/security/modsec/custom', ['rules' => '']);
        $this->post('/security/modsec/update');
        self::assertSame(0, $this->calls('modsec.configure') + $this->calls('modsec.custom_set') + $this->calls('modsec.update'));
    }

    public function testInactiveServerExplainsWhatToDo(): void
    {
        $this->agent->succeed('modsec.status', ['module_loaded' => false, 'active' => false, 'packages_available' => true, 'crs' => ['available' => false], 'config' => ['enabled' => false], 'vendors' => [], 'sites' => [],
            'connector' => ['state' => 'installed', 'reason' => 'The module is installed but Nginx cannot load it: not binary compatible', 'nginx_version' => '1.26.2']]);
        $this->agent->succeed('modsec.installs', ['installs' => []]);
        $this->login('cust');
        self::assertStringContainsString('ask your hosting provider', $this->request('GET', '/sites/' . $this->siteId . '/firewall')->body());
        $this->switchTo('admin');
        $page = $this->request('GET', '/security/modsec')->body();
        foreach (['Connector installed, not usable', 'not binary compatible', 'id="ms-install"', 'Install ModSecurity', 'Install Core Rule Set', 'nginx-1.26.2.tar.gz', 'The Nginx program itself is never changed'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
    }

    /** Install, Core Rule Set, records and rollback: administrators only. */
    public function testInstallationsAndRollback(): void
    {
        $this->agent->succeed('modsec.install', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->agent->succeed('modsec.crs_install', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $this->agent->succeed('modsec.rollback', ['job_id' => 'cccccccccccccccc']);
        $this->agent->succeed('modsec.installs', ['installs' => [
            ['id' => '20260929T051322Z-000002-84871d', 'status' => 'committed', 'started_at' => '2026-09-29T05:13:22Z', 'method' => 'source', 'nginx_version' => '1.24.0', 'os' => 'ubuntu 24.04', 'sources' => [['name' => 'ModSecurity-nginx', 'version' => '1.0.4', 'origin' => 'https://github.com/…']], 'module' => '/usr/local/lib/whacp/nginx-modules/ngx_http_modsecurity_module-1.24.0-2-84871d.so', 'can_roll_back' => true, 'folder' => '/var/lib/whacp-agent/transactions/modsec/x'],
            ['id' => '20260929T050000Z-000001-aaaaaa', 'status' => 'rollback_failed', 'started_at' => '2026-09-29T05:00:00Z', 'method' => 'source', 'error' => 'make failed', 'can_roll_back' => false, 'folder' => '/var/lib/whacp-agent/transactions/modsec/y'],
        ]]);
        $this->login('admin');
        $page = $this->request('GET', '/security/modsec')->body();
        foreach (['id="installs"', 'ModSecurity-nginx 1.0.4', 'in effect', 'Roll back', 'Rollback incomplete', '/var/lib/whacp-agent/transactions/modsec/y/snapshot', 'make failed'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertSame(1, substr_count($page, '/security/modsec/rollback/'), 'only the installation in effect can be rolled back');

        $this->post('/security/modsec/install', ['method' => 'source', 'nginx_source_sha256' => str_repeat('AB', 32)]);
        self::assertSame(['method' => 'source', 'upgrade' => false, 'retention_days' => 30, 'nginx_source_sha256' => str_repeat('ab', 32)], $this->lastCall('modsec.install'));
        $this->post('/security/modsec/install', ['nginx_source_sha256' => 'not-a-hash']);
        self::assertSame(1, $this->calls('modsec.install'), 'a malformed checksum is refused');
        $this->post('/security/modsec/crs');
        self::assertSame(1, $this->calls('modsec.crs_install'));
        $this->post('/security/modsec/rollback/20260929T051322Z-000002-84871d');
        self::assertSame(['id' => '20260929T051322Z-000002-84871d'], $this->lastCall('modsec.rollback'));
        $db = $this->app->container->get(Connection::class);
        foreach (['modsec.install', 'modsec.crs_install', 'modsec.rollback'] as $a) {
            self::assertSame(1, (int) $db->scalar('SELECT COUNT(*) FROM audit_logs WHERE action = ?', [$a]), $a);
        }
        self::assertSame(3, (int) $db->scalar('SELECT COUNT(*) FROM agent_jobs'));

        $this->switchTo('resell');
        $this->post('/security/modsec/install', ['method' => 'auto']);
        $this->post('/security/modsec/crs');
        $this->post('/security/modsec/rollback/20260929T051322Z-000002-84871d');
        self::assertSame(1, $this->calls('modsec.install') + $this->calls('modsec.crs_install') + $this->calls('modsec.rollback') - 2, 'resellers cannot');
    }
}
