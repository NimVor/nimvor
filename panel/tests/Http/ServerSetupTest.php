<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Services\ServerSetupService;
use NimVor\Services\SettingsService;
use NimVor\Tests\Support\HttpTestCase;

/** The server setup (installer profiles): every step through the agent, idempotent. */
final class ServerSetupTest extends HttpTestCase
{
    private const JOB = 'abcdef0123456789';

    /** @param list<array{0: string, 1: string, 2?: list<string>}> $steps */
    private static function component(string $id, bool $installed, array $steps): array
    {
        return ['id' => $id, 'name' => ucfirst($id), 'installed' => $installed, 'steps' => array_map(static fn (array $s): array => ['id' => $s[0], 'status' => $s[1], 'items' => $s[2] ?? []], $steps)];
    }

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('components.status', [
            self::component('web', true, [['installed', 'ok'], ['ports', 'todo', ['80/tcp', '443/tcp']]]),
            self::component('php', true, [['installed', 'ok']]),
            self::component('mail', true, [['installed', 'todo', ['rspamd']], ['setup', 'todo'], ['running', 'todo', ['postfix.service']], ['boot', 'ok']]),
            self::component('dns', true, [['installed', 'ok'], ['setup', 'todo']]),
            self::component('ftp', true, [['installed', 'ok'], ['setup', 'todo']]),
            self::component('redis', true, [['installed', 'ok'], ['boot', 'todo', ['redis-server.service']]]),
            self::component('clamav', false, [['installed', 'todo', ['clamav', 'clamav-freshclam']]]),
            // Not in the minimal profile:
            self::component('memcached', false, [['installed', 'todo', ['memcached']]]),
        ]);
        $this->agent->succeed('packages.install', ['job_id' => self::JOB]);
        $this->agent->succeed('jobs.log', ['log' => "working\n", 'offset' => 8, 'done' => true, 'job' => ['id' => self::JOB, 'status' => 'succeeded']]);
        $this->agent->succeed('php.available', ['8.1', '8.3', '8.4']);
        $this->agent->succeed('php.repository', ['kind' => 'ondrej-ppa', 'name' => 'PPA', 'supported' => true, 'enabled' => false]);
        $this->agent->succeed('php.repository.enable', ['job_id' => self::JOB]);
        $this->agent->succeed('php.catalog', ['extensions' => [], 'defaults' => ['curl', 'mbstring']]);
        $this->agent->succeed('php.extensions', [['name' => 'intl', 'installed' => true, 'enabled' => true], ['name' => 'gd', 'installed' => true, 'enabled' => false]]);
        $this->agent->succeed('php.profile_plan', ['changes' => true, 'removals' => false, 'items' => []]);
        $this->agent->succeed('php.profile_apply', ['job_id' => self::JOB]);
        $this->agent->succeed('mail.configure', ['ok' => true]);
        $this->agent->succeed('dns.configure', ['ok' => true]);
        $this->agent->succeed('ftp.configure', ['ok' => true]);
        $this->agent->succeed('redis.info', ['maxmemory' => '0']);
        $this->agent->succeed('redis.configure', ['ok' => true]);
        $this->agent->succeed('services.control', ['active' => true]);
        $this->agent->succeed('firewall.status', ['installed' => true, 'active' => true, 'required' => [['port' => '22', 'proto' => 'tcp', 'service' => 'SSH', 'critical' => true, 'open' => true]]]);
        $this->agent->succeed('firewall.allow_ports', ['changed' => true, 'pending' => ['id' => 'aaaabbbbcccc']]);
        $this->agent->succeed('firewall.protection', ['config' => ['enabled' => false]]);
        $this->agent->succeed('firewall.protection_set', ['changed' => true, 'pending' => ['id' => 'ddddeeeeffff']]);
        $this->agent->succeed('security.confirm', ['confirmed' => 'x']);
        $this->agent->succeed('security.undo', ['rolled_back' => 'x']);
        $this->agent->succeed('fail2ban.status', ['installed' => true, 'running' => true, 'jails' => []]);
        $this->agent->succeed('fail2ban.configure', ['ok' => true]);
        $this->agent->succeed('security.fix_kernel', ['changed' => ['net.ipv4.tcp_syncookies']]);
        $this->agent->succeed('hardening.status', [
            'smtp' => ['enabled' => false, 'available' => true], 'compilers' => ['restricted' => false], 'egress' => ['metadata_blocked' => false, 'available' => true, 'tcp_ports' => [], 'udp_ports' => []],
            'forkbomb' => ['enabled' => false], 'traceroute' => ['enabled' => true, 'programs' => [['path' => '/usr/bin/traceroute']]],
        ]);
        $this->agent->succeed('hardening.smtp', ['enabled' => true]);
        $this->agent->succeed('hardening.compilers', ['restricted' => true]);
        $this->agent->succeed('hardening.egress', ['metadata_blocked' => true]);
        $this->agent->succeed('hardening.forkbomb', ['enabled' => true]);
        $this->agent->succeed('hardening.traceroute', ['enabled' => false]);
        $this->agent->succeed('hulk.sync', ['ok' => true]);
        $this->agent->succeed('modsec.status', ['module_loaded' => false, 'active' => false]);
        $this->agent->succeed('security.auto_updates', ['job_id' => self::JOB]);
    }

    private function service(): ServerSetupService
    {
        $s = $this->app->container->get(ServerSetupService::class);
        $s->pollMicroseconds = 0;
        return $s;
    }

    /** @param list<array<string, string>> $results */
    private static function byStep(array $results): array
    {
        return array_column($results, 'status', 'id');
    }

    /** @param list<array<string, string>> $results */
    private static function byStepDetail(array $results, string $id): string
    {
        return (string) array_column($results, 'detail', 'id')[$id];
    }

    private function bodies(string $action): array
    {
        $out = [];
        foreach ($this->agent->requests as $r) {
            if ($r['path'] === '/v1/actions/' . $action) {
                $out[] = json_decode((string) $r['body'], true);
            }
        }
        return $out;
    }

    public function testFullProfileSetsUpEveryPart(): void
    {
        $lines = [];
        $res = $this->service()->run('full', ['hostname' => 'server.example.com'], static function (string $l) use (&$lines): void {
            $lines[] = $l;
        });
        $st = self::byStep($res);
        self::assertSame(array_keys(ServerSetupService::STEPS), array_keys($st), 'every step of the full profile');
        self::assertNotContains('fail', $st, json_encode($res));
        // Missing packages of the profile's components only.
        self::assertSame(['names' => ['rspamd', 'clamav', 'clamav-freshclam', 'memcached']], $this->lastCall('packages.install'));
        self::assertContains('   | working', $lines, 'job logs are shown');
        // 8.4 is offered: no third-party repository.
        self::assertSame(0, $this->calls('php.repository.enable'));
        // Nothing removed: the installed 8.3 and its extensions stay.
        $php = $this->lastCall('php.profile_apply');
        self::assertSame(['8.3', '8.4'], $php['versions']);
        self::assertSame(['curl', 'mbstring', 'intl'], $php['extensions']);
        self::assertFalse($php['confirm_removals']);
        self::assertSame(['hostname' => 'server.example.com', 'confirm_edit_package_config' => true], $this->lastCall('mail.configure'));
        // The package's own pure-ftpd service, started by the install, may be replaced while it has no accounts.
        self::assertSame(['replace_unused_stock' => true], $this->lastCall('ftp.configure'));
        self::assertSame(['confirm_edit_package_config' => true], $this->lastCall('dns.configure'));
        self::assertSame(['maxmemory_mb' => 256, 'policy' => 'allkeys-lru'], $this->lastCall('redis.configure'));
        self::assertSame([['unit' => 'postfix.service', 'op' => 'start'], ['unit' => 'redis-server.service', 'op' => 'enable']], $this->bodies('services.control'));
        self::assertSame(['ports' => [['port' => '80', 'proto' => 'tcp'], ['port' => '443', 'proto' => 'tcp']], 'comment' => 'Web'], $this->lastCall('firewall.allow_ports'));
        // Guarded changes are kept once the critical ports are open.
        self::assertSame([['id' => 'aaaabbbbcccc'], ['id' => 'ddddeeeeffff']], $this->bodies('security.confirm'));
        self::assertSame(0, $this->calls('security.undo'));
        self::assertTrue($this->lastCall('firewall.protection_set')['invalid']);
        self::assertSame(['enabled' => true], $this->lastCall('hardening.smtp'));
        self::assertSame(['enabled' => true], $this->lastCall('security.auto_updates'));
        self::assertSame(['sshd', 'recidive', 'nginx-http-auth', 'nginx-botsearch', 'nginx-limit-req'], $this->lastCall('fail2ban.configure')['jails']);

        // The recommended security settings are on, once.
        self::assertSame(['restricted' => true, 'users' => []], $this->lastCall('hardening.compilers'));
        self::assertTrue($this->lastCall('hardening.egress')['metadata_blocked']);
        self::assertSame(['enabled' => true], $this->lastCall('hardening.forkbomb'));
        self::assertSame(['enabled' => false], $this->lastCall('hardening.traceroute'), 'traceroute is disabled for users');
        self::assertSame(['60', '10', '5'], array_map('strval', [$this->lastCall('fail2ban.configure')['ban_minutes'], $this->lastCall('fail2ban.configure')['find_minutes'], $this->lastCall('fail2ban.configure')['max_retry']]));
        $conf = $this->app->container->get(SettingsService::class);
        self::assertSame(7, $conf->int('av_scan_days'));
        self::assertTrue($conf->bool('av_quarantine'));
        self::assertTrue($conf->bool('hulk_enabled'));
        self::assertSame(5, $conf->int('hulk_user_max'));
        self::assertTrue($conf->bool('security_defaults_applied'));
        self::assertStringContainsString('ModSecurity (The Nginx connector is not installed yet)', self::byStepDetail($res, 'security_defaults'));

        $db = $this->app->container->get(Connection::class);
        self::assertSame(['Starter', 'Business', 'Pro', 'Email only'], array_column($db->fetchAll('SELECT name FROM plans ORDER BY id'), 'name'));
        self::assertSame('Email only', $db->scalar('SELECT l.name FROM plans p JOIN feature_lists l ON l.id = p.feature_list_id WHERE p.name = ?', ['Email only']));
        self::assertSame(['ssh'], array_column($db->fetchAll("SELECT feature FROM feature_list_disabled d JOIN feature_lists l ON l.id = d.list_id WHERE l.name = 'Hosting'"), 'feature'));
        $sched = $db->fetchAll('SELECT kind, all_sites, frequency, weekday, retention FROM backup_schedules ORDER BY id');
        self::assertSame([['kind' => 'site', 'all_sites' => 1, 'frequency' => 'weekly', 'weekday' => 0, 'retention' => 4], ['kind' => 'server', 'all_sites' => 0, 'frequency' => 'weekly', 'weekday' => 0, 'retention' => 4]], $sched);

        $settings = $this->app->container->get(SettingsService::class);
        self::assertSame(implode(',', ServerSetupService::PROFILES['full']), $settings->string('components_expected'));
        $last = $this->service()->last();
        self::assertSame('full', $last['profile']);
        self::assertNotNull($last['finished_at']);
        self::assertSame('ok', $last['steps']['plans']['status']);
        self::assertSame(['8.3', '8.4'], json_decode($settings->string('php_profile'), true)['versions']);
    }

    public function testRunningAgainKeepsWhatExists(): void
    {
        $this->service()->run('full', ['hostname' => 'server.example.com']);
        $db = $this->app->container->get(Connection::class);
        $db->run("UPDATE plans SET max_websites = 9 WHERE name = 'Starter'");
        $res = self::byStep($this->service()->run('full', ['hostname' => 'server.example.com']));
        self::assertSame('skip', $res['plans']);
        self::assertSame('skip', $res['backups']);
        self::assertSame('skip', $res['security_defaults'], 'security settings an administrator changed are not set again');
        self::assertSame(4, (int) $db->scalar('SELECT COUNT(*) FROM plans'));
        self::assertSame(9, (int) $db->scalar("SELECT max_websites FROM plans WHERE name = 'Starter'"), 'an edited plan is left alone');
        self::assertSame(2, (int) $db->scalar('SELECT COUNT(*) FROM backup_schedules'));
    }

    public function testFirewallChangeIsUndoneWhenACriticalPortWouldClose(): void
    {
        $this->agent->succeed('firewall.status', ['installed' => true, 'active' => true, 'required' => [['port' => '22', 'proto' => 'tcp', 'service' => 'SSH', 'critical' => true, 'open' => false]]]);
        $res = self::byStep($this->service()->run('full', ['hostname' => 'server.example.com', 'only' => ['ports', 'protection']]));
        self::assertSame(['ports' => 'fail', 'protection' => 'fail'], $res);
        self::assertSame(0, $this->calls('security.confirm'));
        self::assertSame([['id' => 'aaaabbbbcccc'], ['id' => 'ddddeeeeffff']], $this->bodies('security.undo'));
    }

    public function testFailedStepDoesNotStopTheOthers(): void
    {
        $this->agent->fail('mail.configure', 409, 'conflict', 'The mail packages are not complete.');
        $res = self::byStep($this->service()->run('full', ['hostname' => 'server.example.com']));
        self::assertSame('fail', $res['mail']);
        self::assertSame('ok', $res['dns']);
        self::assertSame('ok', $res['plans']);
        self::assertStringContainsString('not complete', $this->service()->last()['steps']['mail']['detail']);
    }

    public function testMailNeedsAFullHostName(): void
    {
        $res = $this->service()->run('full', ['hostname' => '', 'only' => ['mail']]);
        if (str_contains((string) gethostname(), '.')) {
            self::assertSame('ok', $res[0]['status']);
        } else {
            self::assertSame('fail', $res[0]['status']);
            self::assertStringContainsString('--hostname', $res[0]['detail']);
            self::assertSame(0, $this->calls('mail.configure'));
        }
    }

    public function testMinimalProfileLeavesMailDnsFtpAndCachesAlone(): void
    {
        $res = self::byStep($this->service()->run('minimal'));
        foreach (['mail', 'dns', 'ftp', 'caches', 'protection'] as $s) {
            self::assertArrayNotHasKey($s, $res);
        }
        self::assertSame(0, $this->calls('packages.install'), 'mail, ClamAV and Memcached are not part of it');
        self::assertSame(['ports' => [['port' => '80', 'proto' => 'tcp'], ['port' => '443', 'proto' => 'tcp']], 'comment' => 'Web'], $this->lastCall('firewall.allow_ports'));
    }

    public function testThirdPartyRepositoryOnlyWhenAllowed(): void
    {
        $this->agent->succeed('php.available', ['8.1', '8.3']);
        $res = $this->service()->run('web', ['third_party_repos' => false, 'only' => ['php_repository', 'php']]);
        self::assertSame('skip', $res[0]['status']);
        self::assertStringContainsString('--no-third-party-repos', $res[0]['detail']);
        self::assertSame(0, $this->calls('php.repository.enable'));
        self::assertSame(['8.3'], $this->lastCall('php.profile_apply')['versions'], 'only the offered versions');

        $res = $this->service()->run('web', ['only' => ['php_repository']]);
        self::assertSame('ok', $res[0]['status']);
        self::assertSame(1, $this->calls('php.repository.enable'));
    }

    public function testInvalidOptionsAreRefused(): void
    {
        foreach ([['huge', []], ['full', ['hostname' => 'not a host']], ['full', ['php' => ['latest']]]] as [$p, $o]) {
            try {
                $this->service()->run($p, $o);
                self::fail('accepted ' . json_encode([$p, $o]));
            } catch (\NimVor\Support\ValidationException) {
                self::assertSame(0, count($this->agent->requests));
            }
        }
    }
}
