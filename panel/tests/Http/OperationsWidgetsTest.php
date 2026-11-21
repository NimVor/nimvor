<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** Backup health, system updates, DNS & mail, deployments and WordPress dashboard widgets. */
final class OperationsWidgetsTest extends HttpTestCase
{
    private Connection $conn;
    /** @var array<string, int> */
    private array $site = [];

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->conn = $this->app->container->get(Connection::class);
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        foreach (['safe.test', 'stale.test'] as $d) {
            $this->site[$d] = $sites->create(['domain' => $d, 'kind' => 'static'], (array) $users->find($admin));
        }
    }

    /** @return array<string, mixed> */
    private function widget(string $id): array
    {
        $res = $this->request('GET', '/dashboard/widgets/' . $id, [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertSame(200, $res->status(), $res->body());
        return (array) json_decode($res->body(), true);
    }

    private function backup(string $domain, string $status, int $ageHours, string $error = '', ?int $storage = null): void
    {
        $this->conn->run(
            "INSERT INTO backups (server_id, website_id, kind, domain, backup_id, size, encrypted, storage_id, status, error, created_at) VALUES (1, ?, 'site', ?, ?, 1048576, 1, ?, ?, ?, ?)",
            [$this->site[$domain], $domain, uniqid('b', true), $storage, $status, $error, gmdate('c', time() - $ageHours * 3600)]
        );
    }

    public function testBackupHealthNamesWhatIsNotCoveredAndTheLastFailure(): void
    {
        $this->backup('safe.test', 'completed', 5);
        $this->backup('stale.test', 'completed', 24 * 10);
        $this->backup('stale.test', 'failed', 3, 'disk full');
        $this->login('admin');
        $w = $this->widget('backup_health');
        self::assertSame('ok', $w['state']);
        self::assertStringContainsString('1 / 2', $w['html']);
        self::assertStringContainsString('stale.test', $w['html']);
        self::assertStringContainsString('disk full', $w['html']);
        self::assertStringContainsString('Test restores are not tracked', $w['html']);
    }

    public function testSystemUpdatesAndRestart(): void
    {
        $this->agent->succeed('packages.updates', ['count' => 12, 'security' => 3, 'index_stale' => true, 'index_refreshed' => '2026-11-01T00:00:00Z']);
        $this->agent->succeed('server.settings', ['reboot_required' => true, 'reboot_required_by' => ['linux-image']]);
        $this->login('admin');
        $html = $this->widget('system_updates')['html'];
        foreach (['>12<', '>3<', 'A restart is needed', 'linux-image', 'not refreshed'] as $s) {
            self::assertStringContainsString($s, $html);
        }
    }

    public function testUpdatesThatCannotBeReadAreUnavailableAndAnUnknownRestartIsSaid(): void
    {
        $this->login('admin');
        self::assertSame('unavailable', $this->widget('system_updates')['state']);
        $this->conn->run('DELETE FROM widget_cache');
        $this->agent->succeed('packages.updates', ['count' => 0, 'security' => 0]);
        $html = $this->widget('system_updates')['html'];
        self::assertStringContainsString('could not be read', $html);
    }

    public function testDnsAndMail(): void
    {
        $this->agent->succeed('dns.status', ['backend_label' => 'PowerDNS', 'installed' => true, 'running' => true, 'zones' => [['name' => 'a.test'], ['name' => 'b.test']], 'signed' => ['a.test']]);
        $this->agent->succeed('mail.server_status', ['services' => [['name' => 'Postfix', 'active' => true, 'installed' => true], ['name' => 'Dovecot', 'active' => false, 'installed' => true], ['name' => 'Rspamd', 'active' => false, 'installed' => false]]]);
        $this->agent->succeed('mail.queue', ['total' => 7, 'counts' => ['deferred' => 5, 'active' => 2]]);
        $this->login('admin');
        $html = $this->widget('dns_mail')['html'];
        foreach (['PowerDNS', '2 zones', '1 signed', 'Postfix', 'stopped', '7 messages', '5 deferred'] as $s) {
            self::assertStringContainsString($s, $html);
        }
        self::assertStringNotContainsString('Rspamd', $html, 'a service that is not installed is not listed');
    }

    public function testDeploymentsAreEmptyThenCounted(): void
    {
        $this->login('admin');
        self::assertSame('empty', $this->widget('deployments')['state']);
        $now = gmdate('c');
        $a = $this->site['safe.test'];
        $this->conn->run('DELETE FROM widget_cache');
        $this->conn->run("INSERT INTO deployments (website_id, trigger, status, created_at) VALUES (?, 'manual', 'succeeded', ?)", [$a, $now]);
        $this->conn->run("INSERT INTO deployments (website_id, trigger, status, error, created_at) VALUES (?, 'webhook', 'failed', 'composer failed', ?)", [$a, $now]);
        $w = $this->widget('deployments');
        self::assertSame('ok', $w['state']);
        self::assertStringContainsString('composer failed', $w['html']);
        self::assertStringContainsString('>2<', $w['html']);
    }

    public function testWordPress(): void
    {
        $this->login('admin');
        self::assertSame('empty', $this->widget('wordpress')['state']);
        $this->conn->run('DELETE FROM widget_cache');
        $this->conn->run("INSERT INTO wordpress_installs (server_id, website_id, path, version, latest, plugin_updates, theme_updates, issues, scanned_at) VALUES (1, ?, '/', '6.3', '6.8', 4, 1, 2, ?)", [$this->site['safe.test'], '2026-11-20T10:00:00+00:00']);
        $this->conn->run("INSERT INTO wordpress_installs (server_id, website_id, path, version, latest, plugin_updates, theme_updates, issues, scanned_at) VALUES (1, ?, '/', '6.8', '6.8', 0, 0, 0, ?)", [$this->site['stale.test'], '2026-11-21T10:00:00+00:00']);
        $html = $this->widget('wordpress')['html'];
        foreach (['safe.test', 'runs 6.3, latest is 6.8', '>4<', '2 open issues', '2026-11-20 10:00 UTC'] as $s) {
            self::assertStringContainsString($s, $html);
        }
        self::assertStringNotContainsString('stale.test', $html, 'a current install is not listed as out of date');
    }
}
