<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** The PHP configuration card of a website: the customer's php.ini and .user.ini as the agent reports them. */
final class SitePhpConfigTest extends HttpTestCase
{
    private int $siteId;
    private int $otherId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $sites = $this->app->container->get(WebsiteService::class);
        $users = $this->app->container->get(UserRepository::class);
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($cust));
        $this->otherId = $sites->create(['domain' => 'blog.test', 'kind' => 'php', 'php_version' => '8.3'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($other));
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id = ?', [$cust, $this->siteId]);
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id = ?', [$other, $this->otherId]);
    }

    /** @return array<string, mixed> */
    private function config(array $over = []): array
    {
        return $over + [
            'domain' => 'shop.test', 'php_version' => '8.3', 'kind' => 'php', 'managed' => true, 'lock_ini' => false, 'status' => 'active', 'message' => '',
            'last_change' => '2026-11-23T04:50:00Z', 'user_ini_cache_ttl' => 300, 'warnings' => 2,
            'php_ini' => [
                ['name' => 'php.ini', 'where' => 'document root', 'path' => '/home/u/shop.test/public_html/php.ini', 'exists' => true, 'size' => 120, 'directives' => [
                    ['key' => 'memory_limit', 'value' => '512M', 'line' => 1, 'state' => 'applied'],
                    ['key' => 'open_basedir', 'value' => '/', 'line' => 2, 'state' => 'locked', 'reason' => 'This setting is fixed by NimVor for the website\'s safety and cannot be changed here.'],
                    ['key' => 'extension', 'value' => 'evil.so', 'line' => 3, 'state' => 'unsupported', 'reason' => 'Extensions are installed by the server administrator.'],
                ]],
            ],
            'user_ini' => ['name' => '.user.ini', 'where' => 'document root', 'path' => '/home/u/shop.test/public_html/.user.ini', 'exists' => false, 'directives' => []],
            'effective' => [
                ['key' => 'memory_limit', 'value' => '512M', 'source' => 'php.ini', 'detail' => 'document root'],
                ['key' => 'open_basedir', 'value' => '/home/u/shop.test/', 'source' => 'nimvor-locked'],
            ],
        ];
    }

    public function testThePageShowsWhatTheAgentFoundAndWhereEachValueComesFrom(): void
    {
        $this->agent->succeed('sites.phpconfig', $this->config());
        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->siteId . '/php')->body();
        self::assertStringContainsString('id="php-config"', $page);
        self::assertSame('shop.test', $this->lastCall('sites.phpconfig')['domain']);
        self::assertMatchesRegularExpression('#php.ini detected</th><td><span class="badge text-bg-success">Yes#', $page);
        self::assertMatchesRegularExpression('#\.user\.ini detected</th><td><span class="badge text-bg-light border">No#', $page);
        self::assertStringContainsString('PHP 8.3', $page);
        self::assertStringContainsString('2026-11-23 04:50:00 UTC', $page);
        self::assertStringContainsString('Locked', $page);
        self::assertStringContainsString('Not supported', $page);
        self::assertStringContainsString('Extensions are installed by the server administrator.', $page);
        self::assertStringContainsString('NimVor (locked)', $page);
        self::assertStringContainsString('within 300 seconds', $page);
    }

    public function testAnErrorIsShownAndAnUnreachableAgentDoesNotBreakThePage(): void
    {
        $this->agent->succeed('sites.phpconfig', $this->config(['status' => 'error', 'message' => 'PHP-FPM did not accept the new configuration: boom']));
        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->siteId . '/php')->body();
        self::assertStringContainsString('text-bg-danger', $page);
        self::assertStringContainsString('PHP-FPM did not accept the new configuration: boom', $page);

        $this->agent->fail('sites.phpconfig', 503, 'agent.unavailable', 'down');
        $res = $this->request('GET', '/sites/' . $this->siteId . '/php');
        self::assertSame(200, $res->status());
        self::assertStringContainsString('cannot be shown right now', $res->body());
        self::assertStringContainsString('name="memory_limit_mb"', $res->body(), 'the settings form still works');
    }

    public function testCheckNowScansAndAWebsiteWithoutARecordIsAppliedOnce(): void
    {
        $this->login('cust');
        $this->agent->succeed('sites.phpconfig', $this->config(['managed' => false, 'status' => 'pending']));
        $this->agent->succeed('sites.phpconfig_scan', $this->config());
        $before = $this->calls('sites.apply');
        $this->post('/sites/' . $this->siteId . '/php-config/check');
        self::assertSame($before + 1, $this->calls('sites.apply'), 'the record is created by one apply');
        self::assertSame('shop.test', $this->lastCall('sites.phpconfig_scan')['domain']);
        self::assertStringContainsString('checked', (string) $this->flash('success'));

        // A website the agent already knows is not applied again.
        $this->agent->succeed('sites.phpconfig', $this->config());
        $before = $this->calls('sites.apply');
        $this->post('/sites/' . $this->siteId . '/php-config/check');
        self::assertSame($before, $this->calls('sites.apply'));
    }

    public function testResetClearsOnlyTheWebsiteLevelDirectives(): void
    {
        $this->db->run('UPDATE websites SET php_settings = ? WHERE id = ?', [json_encode(['memory_limit_mb' => 300, 'ini' => ['date.timezone' => 'Asia/Karachi']]), $this->siteId]);
        $this->agent->succeed('sites.phpconfig', $this->config());
        $this->login('cust');
        self::assertStringContainsString('Reset website overrides', $this->request('GET', '/sites/' . $this->siteId . '/php')->body());
        $this->post('/sites/' . $this->siteId . '/php-config/reset');
        $s = (array) json_decode((string) $this->db->scalar('SELECT php_settings FROM websites WHERE id = ?', [$this->siteId]), true);
        self::assertSame([], (array) ($s['ini'] ?? []));
        self::assertSame(300, $s['memory_limit_mb'], 'the other PHP settings are kept');
        self::assertArrayNotHasKey('ini', $this->lastCall('sites.apply')['php']);
        self::assertStringContainsString('were not changed', (string) $this->flash('success'));
        // Nothing to reset: no button.
        self::assertStringNotContainsString('Reset website overrides', $this->request('GET', '/sites/' . $this->siteId . '/php')->body());
    }

    public function testOtherCustomersCannotSeeOrChangeAWebsitesConfiguration(): void
    {
        $this->agent->succeed('sites.phpconfig', $this->config());
        $this->login('other');
        $calls = $this->calls('sites.phpconfig');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->siteId . '/php')->status());
        self::assertSame($calls, $this->calls('sites.phpconfig'), 'the agent was not asked');
        foreach (['check', 'reset'] as $op) {
            self::assertSame(404, $this->post('/sites/' . $this->siteId . '/php-config/' . $op)->status(), $op);
        }
        self::assertSame(0, $this->calls('sites.phpconfig_scan'));
    }

    public function testOnlyAdministratorsLockTheLimitsAndItReachesTheAgent(): void
    {
        $this->login('cust');
        self::assertStringNotContainsString('name="lock_ini"', $this->request('GET', '/sites/' . $this->siteId . '/php')->body());
        $this->post('/sites/' . $this->siteId . '/php', ['php_version' => '8.3', 'lock_ini' => '1']);
        self::assertArrayNotHasKey('lock_ini', $this->lastCall('sites.apply')['php'], 'a customer cannot lock or unlock');

        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('admin');
        self::assertStringContainsString('name="lock_ini"', $this->request('GET', '/sites/' . $this->siteId . '/php')->body());
        $this->post('/sites/' . $this->siteId . '/php', ['php_version' => '8.3', 'lock_ini' => '1']);
        self::assertTrue($this->lastCall('sites.apply')['php']['lock_ini']);
        $this->post('/sites/' . $this->siteId . '/php', ['php_version' => '8.3', 'lock_ini' => '0']);
        self::assertArrayNotHasKey('lock_ini', $this->lastCall('sites.apply')['php']);
    }
}
