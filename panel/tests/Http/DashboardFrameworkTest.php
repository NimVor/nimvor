<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Dashboard\Widget;
use NimVor\Dashboard\WidgetCache;
use NimVor\Dashboard\WidgetRegistry;
use NimVor\Database\Connection;
use NimVor\Repositories\RoleRepository;
use NimVor\Repositories\UserRepository;
use NimVor\Security\PasswordHasher;
use NimVor\Security\Permissions as P;
use NimVor\Tests\Support\HttpTestCase;

/** The widget framework of the administrator dashboard: registry, lazy loading, cache, permissions and saved layouts. */
final class DashboardFrameworkTest extends HttpTestCase
{
    private const LIVE = [
        'processes' => 142,
        'top_cpu' => [
            ['pid' => 1201, 'name' => 'php-fpm8.3', 'user' => 'site1', 'state' => 'R', 'cpu_percent' => 41.5, 'rss_bytes' => 210000000, 'mem_percent' => 5.2],
            ['pid' => 77, 'name' => 'mariadbd', 'user' => 'mysql', 'state' => 'S', 'cpu_percent' => 12.0, 'rss_bytes' => 640000000, 'mem_percent' => 8.1],
        ],
        'top_memory' => [
            ['pid' => 77, 'name' => 'mariadbd', 'user' => 'mysql', 'state' => 'S', 'cpu_percent' => 12.0, 'rss_bytes' => 640000000, 'mem_percent' => 8.1],
        ],
    ];

    private int $adminId;

    protected function setUp(): void
    {
        parent::setUp();
        $this->adminId = $this->seedUser('admin', 'admin');
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'srv1'], 'os' => ['pretty_name' => 'Ubuntu', 'supported' => true]]);
        $this->agent->succeed('disk.filesystems', [['mount' => '/', 'device' => '/dev/vda1', 'type' => 'ext4', 'total_bytes' => 100000000000, 'used_bytes' => 40000000000, 'avail_bytes' => 60000000000, 'used_percent' => 40.0, 'inodes' => 100, 'inodes_used' => 10, 'inodes_percent' => 10.0]]);
        $this->agent->succeed('system.live', self::LIVE);
    }

    private function json(): array
    {
        return ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest'];
    }

    private function token(): array
    {
        return ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
    }

    /** @return array<string, mixed> */
    private function widget(string $id, int $expect = 200): array
    {
        $res = $this->request('GET', '/dashboard/widgets/' . $id, [], $this->json());
        self::assertSame($expect, $res->status(), $res->body());
        return (array) json_decode($res->body(), true);
    }

    public function testTheRegistryListsEveryWidgetWithItsCategoryAndPermission(): void
    {
        $reg = new WidgetRegistry();
        self::assertSame(['attention', 'disk', 'backups', 'top_processes', 'website_health', 'security_overview', 'ssl_overview'], array_keys($reg->all()));
        foreach ($reg->all() as $w) {
            self::assertContains($w->category, WidgetRegistry::CATEGORIES, $w->id);
            self::assertArrayHasKey($w->size, Widget::SIZES, $w->id);
            self::assertNotSame('', $w->permission, $w->id);
        }
        self::assertSame(['disk'], array_keys($reg->visibleTo(static fn (string $p): bool => $p === P::SERVER_VIEW)));
    }

    public function testCheapWidgetsAreDrawnWithThePageAndTheOthersAreLoadedAfterIt(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('data-widget="disk"', $page, 'a cheap widget comes with the page');
        self::assertStringNotContainsString('mariadbd', $page, 'a measured widget does not hold the page back');
        self::assertMatchesRegularExpression('/data-widget-host="top_processes"[^>]*data-widget-lazy/', $page);
        self::assertSame(0, $this->calls('system.live'), 'the page itself did not measure the processes');
    }

    public function testTopProcessesShowTheRealNumbersAndNotAvailableWhereThereAreNone(): void
    {
        $this->login('admin');
        $w = $this->widget('top_processes');
        self::assertTrue($w['success']);
        self::assertSame('ok', $w['state']);
        self::assertStringContainsString('php-fpm8.3', $w['html']);
        self::assertStringContainsString('41.5%', $w['html']);
        self::assertStringContainsString('142 processes running', $w['html']);
        // A reading the agent did not give is N/A, never a made-up zero.
        $this->agent->succeed('system.live', ['processes' => 1, 'top_cpu' => [['pid' => 5, 'name' => 'x', 'user' => 'u', 'state' => 'S']], 'top_memory' => []]);
        $this->app->container->get(WidgetCache::class)->forget('top_processes');
        $html = $this->widget('top_processes')['html'];
        self::assertStringContainsString('N/A', $html);
        self::assertStringNotContainsString('0.0%', $html);
    }

    public function testAMeasurementIsKeptForTheWidgetsTimeSoRefreshesCostOnce(): void
    {
        $this->login('admin');
        $this->widget('top_processes');
        $this->widget('top_processes');
        $this->widget('top_processes');
        self::assertSame(1, $this->calls('system.live'));
    }

    public function testAnUnreachableAgentIsShownAsUnavailableNotAsZeros(): void
    {
        $this->agent->fail('system.live', 503, 'unavailable', 'The agent did not answer.');
        $this->login('admin');
        $w = $this->widget('top_processes');
        self::assertSame('unavailable', $w['state']);
        self::assertStringContainsString('Not available right now', $w['html']);
        self::assertStringNotContainsString('<td', $w['html']);
    }

    public function testNoProcessesIsAnEmptyStateAndAFailedMeasurementIsNotKept(): void
    {
        $this->agent->succeed('system.live', ['processes' => 0, 'top_cpu' => [], 'top_memory' => []]);
        $this->login('admin');
        self::assertSame('empty', $this->widget('top_processes')['state']);
        // Malformed data is an error state, not a fatal page.
        $this->agent->succeed('system.live', 'not-an-object');
        $this->app->container->get(WidgetCache::class)->forget('top_processes');
        self::assertContains($this->widget('top_processes')['state'], ['empty', 'error']);
    }

    public function testAWidgetIsOnlyForThoseWhoMaySeeIt(): void
    {
        $roles = $this->app->container->get(RoleRepository::class);
        $id = $roles->create('dash_only', 'Dashboard only', 'Sees the dashboard and nothing else.');
        $roles->setPermissions($id, [P::DASHBOARD_VIEW]);
        $this->app->container->get(UserRepository::class)->create([
            'username' => 'bare', 'email' => 'bare@example.test', 'display_name' => '',
            'password_hash' => (new PasswordHasher())->hash('a-long-test-password'), 'role_id' => $id, 'is_active' => true,
        ]);
        $this->login('bare');
        $w = $this->widget('top_processes', 403);
        self::assertFalse($w['success']);
        self::assertStringNotContainsString('php-fpm', json_encode($w));
        self::assertSame(0, $this->calls('system.live'), 'the data was not even collected');
        $page = $this->request('GET', '/')->body();
        self::assertStringNotContainsString('data-widget-host="top_processes"', $page);
        self::assertStringNotContainsString('data-widget="disk"', $page);
        $this->widget('no_such_widget', 404);
    }

    public function testALayoutIsSavedPerUserAndRestoredToTheDefault(): void
    {
        $this->login('admin');
        $default = $this->request('GET', '/')->body();
        self::assertStringContainsString('data-widget-host="disk"', $default);
        $this->request('POST', '/dashboard/layout', $this->token() + [
            'order' => ['top_processes', 'backups', 'disk', 'attention', 'ghost_widget'],
            'widgets' => [
                'top_processes' => ['visible' => '1', 'size' => 'full'],
                'backups' => ['visible' => '1', 'size' => 'sm'],
                'attention' => ['visible' => '1', 'size' => 'not-a-size'],
                // disk is left unticked: hidden
                'ghost_widget' => ['visible' => '1'],
            ],
        ]);
        $page = $this->request('GET', '/')->body();
        self::assertStringNotContainsString('data-widget-host="disk"', $page, 'a hidden widget is not drawn');
        self::assertLessThan(strpos($page, 'data-widget-host="backups"'), strpos($page, 'data-widget-host="top_processes"'), 'the saved order is kept');
        self::assertStringContainsString('col-12" data-widget-host="top_processes"', $page, 'the saved size is kept');
        self::assertStringNotContainsString('ghost_widget', $page, 'an unknown widget is ignored');
        self::assertStringContainsString('Restore default', $page);

        // Another user is not affected.
        $this->seedUser('admin2', 'admin');
        $this->login('admin2');
        self::assertStringContainsString('data-widget-host="disk"', $this->request('GET', '/')->body());
        $this->login('admin');

        $this->request('POST', '/dashboard/layout/reset', $this->token());
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('data-widget-host="disk"', $page);
        self::assertStringNotContainsString('Restore default', $page);
    }

    public function testAWidgetAddedLaterAppearsWithoutRewritingASavedLayout(): void
    {
        $db = $this->app->container->get(Connection::class);
        // A layout saved when only two widgets existed, with a widget that no longer exists.
        $db->run('INSERT INTO dashboard_layouts (user_id, version, layout, updated_at) VALUES (?, 1, ?, ?)', [
            $this->adminId, json_encode(['widgets' => ['disk' => ['visible' => false, 'size' => 'sm', 'order' => 0], 'removed_long_ago' => ['visible' => true, 'order' => 1]]]), gmdate('c'),
        ]);
        $before = (string) $db->scalar('SELECT layout FROM dashboard_layouts WHERE user_id = ?', [$this->adminId]);
        $this->login('admin');
        $page = $this->request('GET', '/')->body();
        self::assertStringNotContainsString('data-widget-host="disk"', $page, 'the user kept disk hidden');
        self::assertStringContainsString('data-widget-host="top_processes"', $page, 'new widgets appear with their defaults');
        self::assertStringContainsString('data-widget-host="backups"', $page);
        self::assertSame($before, (string) $db->scalar('SELECT layout FROM dashboard_layouts WHERE user_id = ?', [$this->adminId]), 'reading does not rewrite the layout');
    }

    public function testExistingDashboardPartsAreStillThere(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/')->body();
        foreach (['data-metric="cpu"', 'data-metric="mem"', 'id="nim-dashboard"', 'nim-hero', 'Network'] as $part) {
            self::assertStringContainsString($part, $page, $part);
        }
    }
}
