<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Services\MonitoringService;
use WhaCP\Tests\Support\HttpTestCase;

final class SecurityAndSystemTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->login('admin');
    }

    public function testFirewallChangeMustBeConfirmed(): void
    {
        $pending = ['id' => 'abc123abc123', 'kind' => 'firewall', 'summary' => 'firewall: allow proto tcp port 8080', 'deadline' => gmdate('c', time() + 120)];
        $this->agent->succeed('firewall.status', ['installed' => true, 'active' => true, 'default_incoming' => 'deny', 'default_outgoing' => 'allow', 'rules' => [['spec' => 'allow 22/tcp', 'action' => 'allow', 'ssh' => true]], 'ssh_port' => 22, 'ssh_allowed' => true]);
        $this->agent->succeed('security.pending', ['pending' => null]);
        $this->agent->succeed('firewall.rule_add', ['changed' => true, 'pending' => $pending]);
        $page = $this->request('GET', '/security/firewall')->body();
        self::assertStringContainsString('allow 22/tcp', $page);
        self::assertStringNotContainsString('Unconfirmed change', $page);

        $this->post('/security/firewall/rules', ['action' => 'allow', 'port' => '8080', 'proto' => 'tcp', 'comment' => 'app']);
        self::assertSame(['action' => 'allow', 'port' => '8080', 'proto' => 'tcp', 'from' => '', 'comment' => 'app'], $this->lastCall('firewall.rule_add'));
        self::assertStringContainsString('confirm within 2 minutes', (string) $this->flash('success'));

        $this->agent->succeed('security.pending', ['pending' => $pending]);
        $page = $this->request('GET', '/security/firewall')->body();
        self::assertStringContainsString('Unconfirmed change', $page);
        self::assertStringContainsString('data-wcp-countdown', $page);

        $this->agent->succeed('security.confirm', ['confirmed' => 'abc123abc123']);
        $res = $this->post('/security/confirm', ['id' => 'abc123abc123', 'return_to' => '/security/firewall']);
        self::assertSame('/security/firewall', $res->header('Location'));
        self::assertSame('abc123abc123', $this->lastCall('security.confirm')['id']);
        $res = $this->post('/security/confirm', ['id' => 'abc123abc123', 'return_to' => 'https://evil.example']);
        self::assertSame('/security', $res->header('Location'), 'open redirects are not followed');

        // Agent refusals (e.g. lockout protection) surface as friendly errors.
        $this->agent->fail('firewall.set_enabled', 409, 'conflict', 'No rule allows SSH (port 22). Add one first, or you would be locked out.');
        $this->post('/security/firewall/toggle', ['enabled' => '1']);
        self::assertStringContainsString('locked out', (string) $this->flash('error'));
    }

    public function testTurningTheFirewallOnKeepsAccess(): void
    {
        $this->agent->succeed('security.pending', ['pending' => null]);
        $this->agent->succeed('firewall.status', ['installed' => true, 'active' => false, 'rules' => [], 'ssh_port' => 22, 'ssh_allowed' => false, 'required' => [
            ['port' => '22', 'proto' => 'tcp', 'service' => 'SSH', 'critical' => true, 'open' => false],
            ['port' => '8443', 'proto' => 'tcp', 'service' => 'WhaCP panel (HTTPS)', 'critical' => true, 'open' => false],
            ['port' => '443', 'proto' => 'tcp', 'service' => 'Websites and webmail (HTTPS)', 'critical' => false, 'open' => false],
        ]]);
        $page = $this->request('GET', '/security/firewall')->body();
        foreach (['Ports WhaCP keeps open', 'WhaCP panel (HTTPS)', '8443', 'Opened when turned on', 'Turn on firewall', 'Always allow my address <code>192.0.2.10</code>', 'you have 2 minutes'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->agent->succeed('firewall.set_enabled', ['changed' => true, 'pending' => ['id' => 'p1', 'summary' => 'firewall: enable', 'deadline' => gmdate('c', time() + 120)]]);
        $this->agent->succeed('hulk.sync', ['changed' => true]);
        $this->post('/security/firewall/toggle', ['enabled' => '1', 'allow_services' => '1', 'allow_me' => '1']);
        self::assertSame(['enabled' => true, 'allow_services' => true, 'allow_from' => '192.0.2.10'], $this->lastCall('firewall.set_enabled'));
        self::assertStringContainsString('192.0.2.10', $this->app->container->get(\WhaCP\Services\SettingsService::class)->string('hulk_whitelist'), 'also whitelisted in cPHulk');
        // Without the options: only SSH and the panel (the agent always opens those).
        $this->post('/security/firewall/toggle', ['enabled' => '1', 'allow_services' => '0']);
        self::assertSame(['enabled' => true, 'allow_services' => false, 'allow_from' => ''], $this->lastCall('firewall.set_enabled'));
        // A loopback address (SSH tunnel) is never sent.
        $this->remoteIp = '127.0.0.1';
        $this->post('/security/firewall/toggle', ['enabled' => '1', 'allow_services' => '1', 'allow_me' => '1']);
        self::assertSame('', $this->lastCall('firewall.set_enabled')['allow_from']);
    }

    public function testSshAndFail2ban(): void
    {
        $this->agent->succeed('security.pending', ['pending' => null]);
        $this->agent->succeed('ssh.status', ['effective' => ['port' => '22', 'permitrootlogin' => 'without-password', 'passwordauthentication' => 'yes', 'maxauthtries' => '6'], 'root_has_keys' => false, 'admins_with_keys' => [], 'managed_file' => '/etc/ssh/sshd_config.d/00-whacp.conf']);
        self::assertStringContainsString('add a key before turning off passwords', $this->request('GET', '/security/ssh')->body());
        $this->agent->succeed('ssh.configure', ['changed' => true, 'pending' => ['id' => 'x']]);
        $this->post('/security/ssh', ['port' => '2222', 'permit_root_login' => 'no', 'password_authentication' => '0', 'max_auth_tries' => '3']);
        $call = $this->lastCall('ssh.configure');
        self::assertSame(2222, $call['port']);
        self::assertFalse($call['password_authentication']);

        $this->agent->succeed('fail2ban.status', ['installed' => true, 'running' => true, 'jails' => [['name' => 'sshd', 'currently_banned' => 1, 'total_banned' => 3, 'currently_failed' => 0, 'banned_ips' => ['203.0.113.9']]], 'managed_config' => '']);
        self::assertStringContainsString('203.0.113.9', $this->request('GET', '/security/fail2ban')->body());
        $this->agent->succeed('fail2ban.configure', ['ok' => true]);
        $this->post('/security/fail2ban', ['jails' => ['sshd', 'recidive'], 'ban_minutes' => '60', 'find_minutes' => '10', 'max_retry' => '5', 'ignore' => "198.51.100.1, 10.0.0.0/8"]);
        self::assertSame(['198.51.100.1', '10.0.0.0/8'], $this->lastCall('fail2ban.configure')['ignore']);
        $this->agent->succeed('fail2ban.unban', ['unbanned' => 'x']);
        $before = $this->calls('fail2ban.unban');
        $this->post('/security/fail2ban/unban', ['jail' => 'sshd', 'ip' => 'nope']);
        self::assertSame($before, $this->calls('fail2ban.unban'));
    }

    public function testPackagesAndServerSettings(): void
    {
        $this->agent->succeed('packages.list', [
            ['name' => 'fail2ban', 'description' => 'Brute-force protection', 'installed' => false],
            // Detected by what it does, not only by the package.
            ['name' => 'libnginx-mod-brotli', 'description' => 'Brotli', 'installed' => true, 'version' => '', 'state' => 'functional', 'source' => 'other'],
            ['name' => 'libnginx-mod-http-modsecurity', 'description' => 'ModSecurity', 'installed' => true, 'version' => '1.0.3', 'state' => 'installed', 'source' => 'package', 'reason' => 'Nginx does not load it.'],
        ]);
        $this->agent->succeed('packages.updates', ['updates' => [['name' => 'openssl', 'current' => '1', 'new' => '2', 'security' => true]], 'count' => 1, 'security' => 1]);
        $page = $this->request('GET', '/packages')->body();
        self::assertStringContainsString('without the package (built or installed by hand)', $page);
        self::assertStringContainsString('Installed, not usable', $page);
        self::assertStringContainsString('Nginx does not load it.', $page);
        self::assertStringContainsString('openssl', $page);
        self::assertStringContainsString('Install all updates', $page);
        $this->agent->succeed('packages.install', ['job_id' => '1111111111111111']);
        $this->post('/packages/install', ['name' => 'fail2ban']);
        self::assertSame('1111111111111111', $this->flash('job'));

        // The installation queue: JSON, no redirect; the component list as JSON.
        $json = ['accept' => 'application/json', 'content-type' => 'application/json', 'x-csrf-token' => $this->app->container->get(\WhaCP\Security\Csrf::class)->token()];
        $this->agent->succeed('packages.install', ['job_id' => '2222222222222222']);
        $res = $this->request('POST', '/packages/install', [], $json, '{"name":"fail2ban"}');
        self::assertSame(200, $res->status());
        self::assertSame(['success' => true, 'name' => 'fail2ban', 'job_id' => '2222222222222222'], json_decode($res->body(), true));
        self::assertSame(['name' => 'fail2ban'], $this->lastCall('packages.install'));
        $this->agent->fail('packages.install', 400, 'invalid_params', 'name: nope is not an installable component');
        $res = $this->request('POST', '/packages/install', [], $json, '{"name":"nope"}');
        self::assertSame(422, $res->status());
        self::assertStringContainsString('not an installable component', $res->body());
        $res = $this->request('GET', '/packages', [], ['accept' => 'application/json']);
        self::assertSame('fail2ban', json_decode($res->body(), true)['packages'][0]['name']);
        self::assertSame(419, $this->request('POST', '/packages/install', [], ['accept' => 'application/json', 'content-type' => 'application/json'], '{"name":"fail2ban"}')->status(), 'no CSRF token');

        $this->agent->succeed('server.reboot', ['in_minutes' => 5]);
        $this->post('/server/reboot', ['confirm' => 'web1', 'minutes' => '5']);
        self::assertSame(['confirm' => 'web1', 'minutes' => 5], $this->lastCall('server.reboot'));

        // Customers cannot reach server administration.
        $this->seedUser('cust', 'customer');
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        foreach (['/packages', '/security', '/monitoring'] as $p) {
            self::assertSame(403, $this->request('GET', $p)->status(), $p);
        }
        $before = $this->calls('packages.install');
        self::assertSame(403, $this->request('POST', '/packages/install', [], ['accept' => 'application/json', 'content-type' => 'application/json', 'x-csrf-token' => $this->app->container->get(\WhaCP\Security\Csrf::class)->token()], '{"name":"fail2ban"}')->status());
        self::assertSame($before, $this->calls('packages.install'));
        self::assertSame(403, $this->post('/server/reboot', ['confirm' => 'web1'])->status());
    }

    public function testMonitoringAlertsOpenNotifyAndResolve(): void
    {
        $metrics = static fn (float $disk): array => ['cpu_percent' => 12.5, 'memory' => ['used_percent' => 40], 'load' => ['load1' => 0.3], 'disks' => [['mount_point' => '/', 'used_percent' => $disk]], 'network' => [['rx_bytes_per_sec' => 100, 'tx_bytes_per_sec' => 50]]];
        $this->agent->succeed('system.info', ['system' => ['cpu' => ['cores' => 2]]]);
        $this->agent->succeed('system.metrics', $metrics(95));
        $this->agent->succeed('services.status', [['unit' => 'nginx.service', 'active_state' => 'failed', 'unit_file_state' => 'enabled'], ['unit' => 'redis-server.service', 'active_state' => 'inactive', 'unit_file_state' => 'disabled']]);
        $this->agent->succeed('discovery.certificates', [['name' => 'shop.test', 'certificate' => ['days_remaining' => 5]]]);
        $this->post('/settings', ['alert_webhook_url' => 'https://hooks.example.com/abc']);
        $mailer = new \WhaCP\Tests\Support\ArrayMailer();
        $this->app->container->instance(\WhaCP\Mail\Mailer::class, $mailer);
        $this->post('/settings/mail', ['host' => 'smtp.example.com', 'port' => '465', 'encryption' => 'tls', 'from' => 'panel@example.com', 'alert_to' => 'ops@example.com']);
        self::assertStringContainsString('saved', (string) $this->flash('success'));
        $mon = $this->app->container->get(MonitoringService::class);
        $sent = [];
        $mon->sender = function (string $url, string $body) use (&$sent): bool {
            $sent[] = [$url, json_decode($body, true)['text']];
            return true;
        };
        $now = time();
        $mon->run($now);
        $db = $this->app->container->get(Connection::class);
        $open = array_column($db->fetchAll('SELECT rule FROM alerts WHERE resolved_at IS NULL'), 'rule');
        sort($open);
        self::assertSame(['certificate', 'disk', 'service'], $open, 'disabled services do not alert');
        self::assertCount(3, $sent);
        self::assertSame('https://hooks.example.com/abc', $sent[0][0]);
        self::assertCount(3, $mailer->sent, 'alerts are emailed too');
        self::assertSame(['ops@example.com'], $mailer->sent[0]['to']);

        $mon->run($now + 300);
        self::assertCount(3, $sent, "no repeat notifications while firing: " . json_encode($sent));

        $this->agent->succeed('system.metrics', $metrics(50));
        $mon->run($now + 600);
        self::assertStringContainsString('Resolved', $sent[3][1]);
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM alerts WHERE rule = 'disk' AND resolved_at IS NOT NULL"));

        self::assertCount(3, $mon->series(3600, 120, $now + 700));
        self::assertStringContainsString('Not enough data', $this->request('GET', '/monitoring')->body());
        foreach ([1, 2, 3] as $h) {
            $db->run('INSERT INTO metric_samples (taken_at, cpu, mem, disk, load1) VALUES (?, 10, 20, 30, 0.5)', [time() - $h * 3600]);
        }
        $page = $this->request('GET', '/monitoring')->body();
        self::assertStringContainsString('<svg', $page);
        self::assertStringContainsString('nginx.service is failed', $page);
        self::assertStringContainsString('active alert', $this->request('GET', '/')->body());

        $this->agent->down = true;
        $mon->run($now + 900);
        self::assertStringContainsString('agent is not reachable', end($sent)[1]);
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM alerts WHERE rule = 'service' AND resolved_at IS NULL"), 'unmeasured alerts stay open');
    }

    public function testUrlSettings(): void
    {
        foreach (['javascript:alert(1)', 'http://example.com/x', 'ftp://x', 'https://a b'] as $bad) {
            $this->post('/settings', ['panel_url' => $bad]);
            self::assertArrayHasKey('panel_url', (array) $this->flash('errors'), $bad);
        }
        $this->post('/settings', ['panel_url' => 'https://panel.example.com/']);
        $this->post('/settings', ['alert_webhook_url' => '']);
        self::assertSame('https://panel.example.com', $this->app->container->get(\WhaCP\Services\SettingsService::class)->string('panel_url'));
    }
}
