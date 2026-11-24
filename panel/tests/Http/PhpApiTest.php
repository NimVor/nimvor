<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\UserRepository;
use NimVor\Services\ApiTokenService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: a website's PHP settings. */
final class PhpApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    private int $site;
    private int $adminSite;
    private int $staticSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'reseller');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.2', 'fpm_binary' => '/usr/sbin/php-fpm8.2', 'fpm_service' => 'php8.2-fpm.service'], ['version' => '8.3', 'fpm_binary' => '/usr/sbin/php-fpm8.3', 'fpm_service' => 'php8.3-fpm.service']]]);
        $this->site = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($this->cust));
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'php', 'php_version' => '8.3'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($this->admin));
        $this->staticSite = $sites->create(['domain' => 'static.test', 'kind' => 'static'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($this->cust));
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id IN (?, ?)', [$this->cust, $this->site, $this->staticSite]);
        $this->agent->succeed('sites.phpconfig', ['managed' => true, 'effective' => [['key' => 'memory_limit', 'value' => '256M', 'source' => 'nimvor']]]);
        $this->agent->succeed('sites.phpconfig_scan', ['status' => 'ok']);
        $this->agent->succeed('services.control', ['ok' => true]);
    }

    /** @param array<string, mixed> $json */
    private function api(string $method, string $path, ?string $token, array $json = []): Response
    {
        $h = ['content-type' => 'application/json'] + ($token === null ? [] : ['authorization' => 'Bearer ' . $token]);
        return $this->app->handle(new Request($method, $path, [], [], ['REMOTE_ADDR' => '192.0.2.50'], $h, $json === [] ? '' : (string) json_encode($json)));
    }

    /** @return array<string, mixed> */
    private static function json(Response $r): array
    {
        return (array) json_decode($r->body(), true);
    }

    private function token(int $userId, string $ability = 'write'): string
    {
        return $this->app->container->get(ApiTokenService::class)->create($userId, ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => $ability, 'all_scopes' => '1', 'expires_days' => 30]);
    }

    public function testReadAndChangeOnlyWhatIsSent(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/php';
        $res = $this->api('GET', $base, $cust);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame(['8.3', ['8.2', '8.3'], 256, 64], [$d['php_version'], $d['installed_versions'], $d['memory_limit_mb'], $d['upload_max_mb']]);
        self::assertSame('memory_limit', $d['effective']['effective'][0]['key']);

        $res = $this->api('PATCH', $base, $cust, ['memory_limit_mb' => 512, 'php_version' => '8.2', 'ini' => ['display_errors' => 'Off']]);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame(['8.2', 512, 64, ['display_errors' => 'Off']], [$d['php_version'], $d['memory_limit_mb'], $d['upload_max_mb'], $d['ini']]);
        $row = $this->db->fetch('SELECT php_version, php_settings FROM websites WHERE id = ?', [$this->site]);
        self::assertSame('8.2', $row['php_version']);
        self::assertSame('shop.test', $this->lastCall('sites.apply')['sites'][0]['domain'] ?? 'shop.test', 'the website is applied again');

        // A second change keeps the first.
        $d = self::json($this->api('PATCH', $base, $cust, ['upload_max_mb' => 128]))['data'];
        self::assertSame([512, 128, '8.2', ['display_errors' => 'Off']], [$d['memory_limit_mb'], $d['upload_max_mb'], $d['php_version'], $d['ini']]);
        // The directives are replaced as a set and can be cleared.
        $d = self::json($this->api('DELETE', $base . '/ini', $cust))['data'];
        self::assertSame([], (array) $d['ini']);
        self::assertSame(512, $d['memory_limit_mb']);
    }

    public function testValidationAndLimits(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/php';
        foreach ([['memory_limit_mb' => 8], ['memory_limit_mb' => 'lots'], ['upload_max_mb' => 99999], ['php_version' => '5.6'], ['php_profile' => 'wild'], ['ini' => ['allow_url_include' => 'On']], ['ini' => 'display_errors=Off']] as $bad) {
            $res = $this->api('PATCH', $base, $cust, $bad);
            self::assertSame(422, $res->status(), json_encode($bad) . $res->body());
        }
        // Process limits and hardening are for administrators.
        $res = $this->api('PATCH', $base, $cust, ['max_children' => 100, 'harden' => false, 'memory_limit_mb' => 300]);
        self::assertSame(422, $res->status());
        self::assertSame(['max_children', 'harden'], array_keys(self::json($res)['error']['fields']));
        self::assertSame(256, self::json($this->api('GET', $base, $cust))['data']['memory_limit_mb'], 'nothing was applied');
        $admin = $this->token($this->admin);
        $res = $this->api('PATCH', '/api/v1/websites/' . $this->site . '/php', $admin, ['max_children' => 20, 'pm' => 'dynamic']);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame([20, 'dynamic'], [self::json($res)['data']['max_children'], self::json($res)['data']['pm']]);
    }

    public function testNotAvailableForOtherWebsiteTypes(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->staticSite . '/php';
        self::assertSame(409, $this->api('GET', $base, $cust)->status());
        self::assertSame(409, $this->api('PATCH', $base, $cust, ['memory_limit_mb' => 512])->status());
        self::assertSame(409, $this->api('POST', $base . '/reload', $cust)->status());
    }

    public function testReloadAndCheck(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/php';
        $res = $this->api('POST', $base . '/reload', $cust);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('services.control');
        self::assertSame(['php8.3-fpm.service', 'reload'], [$sent['unit'], $sent['op']]);
        $res = $this->api('POST', $base . '/check', $cust);
        self::assertSame([200, 'ok'], [$res->status(), self::json($res)['data']['status']]);
        self::assertSame('shop.test', $this->lastCall('sites.phpconfig_scan')['domain']);
    }

    public function testTheServerRefusingTheConfigurationRestoresTheSettings(): void
    {
        $cust = $this->token($this->cust);
        $this->agent->fail('sites.apply', 500, 'failed', 'php-fpm -t failed');
        $res = $this->api('PATCH', '/api/v1/websites/' . $this->site . '/php', $cust, ['memory_limit_mb' => 999]);
        self::assertSame(502, $res->status());
        $this->agent->succeed('sites.apply', ['changed' => []]);
        self::assertSame(256, self::json($this->api('GET', '/api/v1/websites/' . $this->site . '/php', $cust))['data']['memory_limit_mb'], 'the previous value is back');
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $base = '/api/v1/websites/' . $this->site . '/php';
        self::assertSame(200, $this->api('GET', $base, $ro)->status());
        foreach ([['PATCH', '', ['memory_limit_mb' => 512]], ['POST', '/reload', []], ['POST', '/check', []], ['DELETE', '/ini', []]] as [$m, $p, $b]) {
            self::assertSame(403, $this->api($m, $base . $p, $ro, $b)->status(), "$m $p");
        }
        self::assertSame(401, $this->api('GET', $base, null)->status());
        $other = '/api/v1/websites/' . $this->adminSite . '/php';
        foreach ([['GET', ''], ['PATCH', ''], ['POST', '/reload'], ['POST', '/check'], ['DELETE', '/ini']] as [$m, $p]) {
            self::assertSame(404, $this->api($m, $other . $p, $cust, $m === 'PATCH' ? ['memory_limit_mb' => 512] : [])->status(), "$m $p");
        }
        self::assertSame(200, $this->api('GET', $other, $this->token($this->admin))->status());
    }
}
