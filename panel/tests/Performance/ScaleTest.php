<?php

declare(strict_types=1);

namespace WhaCP\Tests\Performance;

use WhaCP\Database\Connection;
use WhaCP\Http\Request;
use WhaCP\Services\ApiTokenService;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * Measured limits for the release notes: with 100 websites, 500 domains,
 * 100 databases, 100 cron jobs, 100 workers, 1000 audit records and 100
 * backups, every main page and the API answer well within a second on a
 * CI runner (the agent is faked, so this measures the panel). Timings are
 * printed; a page slower than 2 s fails the test.
 */
final class ScaleTest extends HttpTestCase
{
    public function testMainPagesAtScale(): void
    {
        $db = $this->app->container->get(Connection::class);
        $admin = $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $now = gmdate('c');
        $inventory = [];
        $db->pdo()->beginTransaction();
        for ($i = 1; $i <= 100; $i++) {
            $domain = "site{$i}.scale.test";
            $db->run("INSERT INTO websites (server_id, owner_id, domain, system_user, root, document_root, kind, php_version, created_at, updated_at) VALUES (1, ?, ?, ?, ?, ?, 'php', '8.3', ?, ?)",
                [$i % 4 === 0 ? $cust : $admin, $domain, "sc{$i}", "/home/whacp/{$domain}", "/home/whacp/{$domain}/public_html", $now, $now]);
            $site = (int) $db->pdo()->lastInsertId();
            foreach (['', 'www.', 'shop.', 'api.', 'cdn.'] as $j => $prefix) {
                $db->run('INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (1, ?, ?, ?)', [$site, $prefix . $domain, $j === 0 ? 1 : 0]);
            }
            $db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, ?, ?, ?, ?)", [$site, $admin, "sc_db{$i}", $now]);
            $db->run("INSERT INTO cron_jobs (server_id, website_id, schedule, command, created_at, updated_at) VALUES (1, ?, '*/5 * * * *', 'php artisan schedule:run', ?, ?)", [$site, $now, $now]);
            $db->run("INSERT INTO workers (server_id, website_id, slug, name, command, created_at, updated_at) VALUES (1, ?, ?, 'queue', 'php artisan queue:work', ?, ?)", [$site, "w{$i}", $now, $now]);
            $db->run("INSERT INTO backups (server_id, website_id, kind, domain, backup_id, size, encrypted, status, created_at, finished_at) VALUES (1, ?, 'site', ?, ?, 1048576, 1, 'completed', ?, ?)", [$site, $domain, sprintf('20260101T000000Z-%06d', $i), $now, $now]);
            $inventory[] = ['id' => md5($domain), 'domain' => $domain, 'aliases' => ['www.' . $domain, 'shop.' . $domain, 'api.' . $domain, 'cdn.' . $domain], 'enabled' => true, 'kind' => 'php',
                'document_root' => "/home/whacp/{$domain}/public_html", 'document_root_exists' => true, 'https' => true, 'https_redirect' => true,
                'php' => ['version' => '8.3', 'pool' => 'whacp-' . $domain, 'listen' => '/run/php/x.sock', 'user' => "sc{$i}", 'resolved' => true],
                'ssl' => ['cert_file' => "/etc/letsencrypt/live/{$domain}/fullchain.pem", 'names' => [$domain], 'issuer' => 'R3', 'not_after' => '2030-01-01T00:00:00Z', 'days_remaining' => 900, 'status' => 'valid', 'lets_encrypt' => true, 'covers_domain' => true],
                'listen' => ['443 ssl'], 'config_files' => ["/etc/nginx/sites-available/whacp-{$domain}.conf"], 'access_logs' => [], 'error_logs' => [], 'managed_by' => 'WhaCP', 'proxy_pass' => ''];
        }
        for ($i = 1; $i <= 1000; $i++) {
            $db->run("INSERT INTO audit_logs (user_id, username, action, target_type, target_id, result, ip, details, created_at) VALUES (?, 'admin', 'website.update', 'website', ?, 'success', '192.0.2.1', '{}', ?)", [$admin, (string) ($i % 100 + 1), $now]);
        }
        $db->pdo()->commit();
        $this->agent->succeed('discovery.inventory', ['websites' => ['websites' => $inventory, 'catch_all_vhosts' => 0, 'warnings' => []],
            'php' => ['versions' => [['version' => '8.3', 'is_default' => true, 'pool_count' => 100]], 'pools' => [], 'warnings' => []],
            'databases' => ['installed' => true, 'schemas' => [], 'warnings' => []], 'redis' => ['installed' => false, 'warnings' => []],
            'certificates' => [], 'cron' => ['jobs' => [], 'warnings' => []], 'users' => [], 'services' => []]);
        $this->agent->succeed('system.info', ['agent_version' => '1.0.0-rc1', 'os' => ['pretty_name' => 'Ubuntu 24.04 LTS', 'supported' => true],
            'system' => ['hostname' => 'web1', 'kernel' => '6.8', 'cpu' => ['cores' => 2, 'model' => 'x'], 'uptime_seconds' => 100, 'timezone' => 'UTC', 'interfaces' => []]]);
        $this->agent->succeed('workers.status', []);

        $this->login('admin');
        $timings = [];
        $measure = function (string $label, callable $fn) use (&$timings): void {
            $fn(); // warm-up (caches, discovery snapshot)
            $t = hrtime(true);
            $status = $fn();
            $ms = (hrtime(true) - $t) / 1e6;
            $timings[$label] = sprintf('%6.1f ms (HTTP %d)', $ms, $status);
            self::assertLessThan(2000, $ms, "{$label} took {$ms} ms");
        };
        foreach (['/', '/websites', '/domains', '/databases', '/workers', '/backups', '/audit', '/ssl'] as $path) {
            $measure('GET ' . $path, fn (): int => $this->request('GET', $path)->status());
        }
        $token = $this->app->container->get(ApiTokenService::class)->create($admin, ['name' => 'perf', 'ability' => 'read', 'expires_days' => 1]);
        $measure('API GET /api/v1/websites', fn (): int => $this->app->handle(new Request('GET', '/api/v1/websites', [], [], ['REMOTE_ADDR' => '192.0.2.9'], ['authorization' => 'Bearer ' . $token], ''))->status());
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        $measure('GET /websites (customer, 25 own)', fn (): int => $this->request('GET', '/websites')->status());
        fwrite(STDOUT, "\nScale timings (100 websites, 500 domains, 100 databases/cron/workers/backups, 1000 audit records):\n");
        foreach ($timings as $label => $t) {
            fwrite(STDOUT, sprintf("  %-38s %s\n", $label, $t));
        }
    }
}
