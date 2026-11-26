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
        self::assertSame(['tile_cpu', 'tile_memory', 'tile_disk', 'tile_load', 'software', 'alert_center', 'attention', 'performance_history', 'disk', 'website_overview', 'security_overview', 'top_processes', 'services_overview', 'database_overview', 'capacity_forecast', 'traffic_overview', 'backup_health', 'system_updates', 'deployments', 'request_trends', 'wordpress'], array_keys($reg->all()));
        foreach ($reg->all() as $w) {
            self::assertContains($w->category, WidgetRegistry::CATEGORIES, $w->id);
            self::assertArrayHasKey($w->size, Widget::SIZES, $w->id);
            self::assertNotSame('', $w->permission, $w->id);
        }
        self::assertSame(['software', 'disk'], array_keys($reg->visibleTo(static fn (string $p): bool => $p === P::SERVER_VIEW)));
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
            'order' => ['top_processes', 'website_overview', 'disk', 'attention', 'ghost_widget'],
            'widgets' => [
                'top_processes' => ['visible' => '1', 'size' => 'full'],
                'website_overview' => ['visible' => '1', 'size' => 'sm'],
                'attention' => ['visible' => '1', 'size' => 'not-a-size'],
                // disk is left unticked: hidden
                'ghost_widget' => ['visible' => '1'],
            ],
        ]);
        $page = $this->request('GET', '/')->body();
        self::assertStringNotContainsString('data-widget-host="disk"', $page, 'a hidden widget is not drawn');
        self::assertLessThan(strpos($page, 'data-widget-host="website_overview"'), strpos($page, 'data-widget-host="top_processes"'), 'the saved order is kept');
        self::assertStringContainsString('nim-span-full" data-widget-host="top_processes"', $page, 'the saved size is kept');
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

    public function testArrangingOnThePageKeepsVisibilityAndRefreshChoices(): void
    {
        $this->login('admin');
        $this->request('POST', '/dashboard/layout', $this->token() + [
            'order' => ['top_processes', 'disk', 'website_overview', 'attention'],
            'widgets' => [
                'top_processes' => ['visible' => '1', 'size' => 'lg', 'refresh' => '300'],
                'disk' => ['visible' => '1', 'size' => 'md', 'refresh' => 'not-a-number'],
                'website_overview' => ['size' => 'md'], // hidden
                'attention' => ['visible' => '1', 'size' => 'md', 'refresh' => '0'],
            ],
        ]);
        $page = $this->request('GET', '/')->body();
        self::assertMatchesRegularExpression('/data-widget-host="top_processes"[^>]*data-refresh="300"/', $page, 'the chosen refresh interval is used');
        self::assertMatchesRegularExpression('/data-widget-host="attention"[^>]*data-refresh="0"/', $page, 'manual refresh is a choice too');
        self::assertStringContainsString('data-arrange-toggle', $page);

        $r = $this->request('POST', '/dashboard/layout/arrange', $this->token() + [
            'order' => ['attention', 'ghost', 'disk', 'top_processes', 'disk'],
            'sizes' => ['attention' => 'full', 'disk' => 'nope', 'ghost' => 'sm', 'website_overview' => 'full'],
        ]);
        self::assertTrue(json_decode($r->body(), true)['success']);
        $saved = json_decode((string) $this->app->container->get(Connection::class)->scalar('SELECT layout FROM dashboard_layouts WHERE user_id = ?', [$this->adminId]), true)['widgets'];
        self::assertFalse($saved['website_overview']['visible'], 'a hidden widget stays hidden');
        self::assertSame('md', $saved['website_overview']['size'], 'and is not resized by the page');
        self::assertArrayNotHasKey('ghost', $saved);
        self::assertSame('full', $saved['attention']['size']);
        self::assertSame('md', $saved['disk']['size'], 'an unknown size is ignored');
        self::assertSame(300, $saved['top_processes']['refresh']);
        self::assertSame(0, $saved['attention']['refresh']);
        self::assertArrayNotHasKey('refresh', $saved['disk'], 'an invalid choice means the default');

        $page = $this->request('GET', '/')->body();
        $pos = static fn (string $id): int => (int) strpos($page, 'data-widget-host="' . $id . '"');
        self::assertLessThan($pos('disk'), $pos('attention'));
        self::assertLessThan($pos('top_processes'), $pos('disk'));
        self::assertStringNotContainsString('data-widget-host="website_overview"', $page);
    }

    public function testArrangingNeedsTheSessionAndAToken(): void
    {
        $this->login('admin');
        $res = $this->request('POST', '/dashboard/layout/arrange', ['order' => ['disk']]);
        self::assertContains($res->status(), [302, 303, 403, 419]);
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
        self::assertStringContainsString('data-widget-host="website_overview"', $page);
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
