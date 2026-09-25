<?php

declare(strict_types=1);

namespace WhaCP\Tests\Security;

use WhaCP\Database\Connection;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * Release gate: a hosting customer sees and changes only their own
 * resources, whatever URL they request directly.
 */
final class TenantIsolationTest extends HttpTestCase
{
    private Connection $db;
    private int $mine;
    private int $theirs;

    /** Markers that must never reach another tenant. */
    private const SECRETS = ['victim.test', 'S3cretDumpPass', '/home/whacp/victim.test'];

    protected function setUp(): void
    {
        parent::setUp();
        $this->db = $this->app->container->get(Connection::class);
        $alice = $this->seedUser('alice', 'customer');
        $bob = $this->seedUser('bob', 'customer');
        $now = gmdate('c');
        foreach ([[$alice, 'alice.test'], [$bob, 'victim.test']] as [$owner, $domain]) {
            $this->db->run("INSERT INTO websites (server_id, owner_id, domain, system_user, root, document_root, kind, php_version, created_at, updated_at) VALUES (1, ?, ?, ?, ?, ?, 'php', '8.3', ?, ?)",
                [$owner, $domain, substr(str_replace('.', '', $domain), 0, 8), '/home/whacp/' . $domain, '/home/whacp/' . $domain . '/public_html', $now, $now]);
        }
        $this->mine = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'alice.test'");
        $this->theirs = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'victim.test'");
        $site = static fn (string $d): array => [
            'id' => md5($d), 'domain' => $d, 'aliases' => ['www.' . $d], 'enabled' => true, 'kind' => 'php',
            'document_root' => '/home/whacp/' . $d . '/public_html', 'document_root_exists' => true, 'https' => true, 'https_redirect' => false,
            'php' => ['version' => '8.3', 'pool' => 'whacp-' . $d, 'listen' => 'unix:/run/php/whacp-' . $d . '.sock', 'user' => 'x', 'resolved' => true],
            'ssl' => ['cert_file' => '/etc/letsencrypt/live/' . $d . '/fullchain.pem', 'names' => [$d], 'issuer' => 'R3', 'not_after' => '2030-01-01T00:00:00Z', 'days_remaining' => 900, 'status' => 'valid', 'lets_encrypt' => true, 'covers_domain' => true],
            'listen' => ['443 ssl'], 'config_files' => ['/etc/nginx/sites-available/whacp-' . $d . '.conf'], 'access_logs' => [], 'error_logs' => [], 'managed_by' => 'WhaCP', 'proxy_pass' => '',
        ];
        $this->agent->succeed('discovery.inventory', [
            'websites' => ['websites' => [$site('alice.test'), $site('victim.test')], 'catch_all_vhosts' => 0, 'warnings' => []],
            'php' => ['versions' => [['version' => '8.3', 'is_default' => true, 'pool_count' => 2, 'cli_binary' => '/usr/bin/php8.3', 'fpm_binary' => '/usr/sbin/php-fpm8.3']],
                'pools' => [['name' => 'whacp-victim.test', 'version' => '8.3', 'file' => '/etc/php/8.3/fpm/pool.d/whacp-victim.test.conf', 'listen' => '/run/php/x.sock', 'user' => 'victim']], 'warnings' => []],
            'databases' => ['installed' => true, 'schemas' => [['name' => 'victim_test_db']], 'warnings' => []],
            'redis' => ['installed' => false, 'warnings' => []],
            'certificates' => [['name' => 'victim.test', 'domains' => ['victim.test'], 'certificate' => ['days_remaining' => 80]]],
            'cron' => ['jobs' => [['source' => '/etc/cron.d/backup', 'user' => 'root', 'schedule' => '0 3 * * *', 'command' => 'mysqldump -pS3cretDumpPass victim_test_db']], 'warnings' => []],
            'users' => [], 'services' => [],
        ]);
        $this->agent->succeed('system.info', ['agent_version' => '1.0.0', 'os' => ['pretty_name' => 'Ubuntu 24.04 LTS', 'supported' => true],
            'system' => ['hostname' => 'web1', 'kernel' => '6.8', 'cpu' => ['cores' => 2, 'model' => 'x'], 'uptime_seconds' => 100, 'timezone' => 'UTC', 'interfaces' => []]]);
        $this->login('alice');
    }

    /** @return list<array{0: string, 1: string}> method and path of every route */
    private static function routes(): array
    {
        preg_match_all("/\\\$r->(get|post)\\('([^']+)'/", (string) file_get_contents(dirname(__DIR__, 2) . '/routes/web.php'), $m, PREG_SET_ORDER);
        return array_map(static fn (array $x): array => [strtoupper($x[1]), $x[2]], $m);
    }

    public function testServerWidePagesDoNotShowOtherTenants(): void
    {
        foreach (self::routes() as [$method, $path]) {
            if ($method !== 'GET' || str_contains($path, '{') || str_starts_with($path, '/api/') || in_array($path, ['/login', '/login/two-factor', '/password/forgot', '/healthz'], true)) {
                continue;
            }
            $res = $this->request('GET', $path);
            $body = $res->body();
            foreach (self::SECRETS as $secret) {
                if (str_contains($body, $secret)) {
                    $leaks[] = "GET {$path} ({$res->status()}): {$secret}";
                }
            }
        }
        self::assertSame([], $leaks ?? [], "pages showing another tenant's data:\n" . implode("\n", $leaks ?? []));
    }

    public function testOtherTenantsSitesAreRefusedEverywhere(): void
    {
        $checked = 0;
        foreach (self::routes() as [$method, $path]) {
            if (!str_starts_with($path, '/sites/{id}')) {
                continue;
            }
            $url = strtr($path, ['{id}' => (string) $this->theirs, '{job}' => '1', '{worker}' => '1', '{backup}' => '1', '{schedule}' => '1', '{app}' => 'wordpress']);
            $before = count($this->agent->requests);
            $res = $method === 'GET' ? $this->request('GET', $url) : $this->post($url, ['confirm' => 'victim.test', 'enabled' => '1', 'path' => '/', 'op' => 'list']);
            $calls = array_slice($this->agent->requests, $before);
            $mutating = array_filter($calls, static fn (array $c): bool => !preg_match('#^/v1/(actions/)?(discovery\.|system\.|agent\.status|monitoring\.|health)#', $c['path']));
            self::assertSame([], array_values(array_column($mutating, 'path')), "{$method} {$url} reached the agent for another tenant's site");
            if ($res->status() === 200) {
                foreach (self::SECRETS as $secret) {
                    self::assertStringNotContainsString($secret, $res->body(), "{$method} {$url} shows another tenant's site");
                }
            }
            $checked++;
        }
        self::assertGreaterThan(40, $checked);
        // The owner's own site works (sanity check that the test is meaningful).
        self::assertSame(200, $this->request('GET', '/sites/' . $this->mine)->status());
    }

    public function testOtherTenantsDatabasesAndMailAreRefused(): void
    {
        $now = gmdate('c');
        $this->db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, ?, (SELECT owner_id FROM websites WHERE id = ?), 'victim_test_db', ?)", [$this->theirs, $this->theirs, $now]);
        $db = (int) $this->db->scalar("SELECT id FROM databases WHERE name = 'victim_test_db'");
        foreach (self::routes() as [$method, $path]) {
            if (!preg_match('#^/(databases|database-users|mail/domains|dns/zones)/\{id\}#', $path)) {
                continue;
            }
            $url = strtr($path, ['{id}' => (string) $db, '{mailbox}' => '1', '{alias}' => '1', '{record}' => '1']);
            $before = count($this->agent->requests);
            $res = $method === 'GET' ? $this->request('GET', $url) : $this->post($url, ['confirm' => 'victim_test_db', 'password' => 'a-new-long-password-1', 'op' => 'tables']);
            $calls = array_filter(array_slice($this->agent->requests, $before), static fn (array $c): bool => !preg_match('#^/v1/(actions/)?(discovery\.|system\.|agent\.status|health)#', $c['path']));
            self::assertSame([], array_values($calls), "{$method} {$url} reached the agent for another tenant's resource");
            self::assertStringNotContainsString('victim_test_db', $res->status() === 200 ? $res->body() : '', "{$method} {$url}");
        }
    }
}
