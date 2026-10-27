<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\RoleRepository;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

final class PluginsTest extends HttpTestCase
{
    private string $plugins;

    protected function setUp(): void
    {
        $this->plugins = sys_get_temp_dir() . '/whacp-plugins-' . bin2hex(random_bytes(4));
        mkdir($this->plugins, 0o755);
        exec('cp -r ' . escapeshellarg(dirname(__DIR__, 3) . '/plugins/example-status') . ' ' . escapeshellarg($this->plugins . '/'));
        $this->writePlugin('broken', "<?php return function (\\WhaCP\\Plugins\\PluginContext \$ctx): void {\n \$ctx->route('GET', '/', fn () => 'x', 'plugin.broken.view');\n throw new RuntimeException('boom at boot');\n};");
        $this->writePlugin('unsafe', "<?php return function (\$ctx): void {};");
        chmod($this->plugins . '/unsafe/plugin.php', 0o666);
        exec('chmod -R go-w ' . escapeshellarg($this->plugins . '/example-status') . ' ' . escapeshellarg($this->plugins . '/broken'));
        $this->config = ['plugins' => ['path' => $this->plugins]];
        parent::setUp();
    }

    protected function tearDown(): void
    {
        parent::tearDown();
        exec('rm -rf ' . escapeshellarg($this->plugins));
    }

    private function writePlugin(string $name, string $php): void
    {
        mkdir($this->plugins . '/' . $name, 0o755);
        file_put_contents($this->plugins . '/' . $name . '/plugin.json', json_encode(['name' => $name, 'title' => ucfirst($name), 'version' => '1', 'permissions' => ['plugin.' . $name . '.view' => 'View']]));
        file_put_contents($this->plugins . '/' . $name . '/plugin.php', $php);
    }

    private function fresh(string $user): void
    {
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testLifecycle(): void
    {
        $admin = $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->login('admin');
        $page = $this->request('GET', '/admin/plugins')->body();
        self::assertStringContainsString('Example: status page', $page);
        self::assertStringContainsString('Writable by group or others: plugin.php', $page);

        $this->post('/admin/plugins/unsafe/toggle', ['enabled' => '1']);
        self::assertStringContainsString('cannot be enabled', (string) $this->flash('error'));
        $this->post('/admin/plugins/example-status/toggle', ['enabled' => '1']);
        $this->post('/admin/plugins/broken/toggle', ['enabled' => '1']);

        // Plugins load when the next request builds the router.
        $this->fresh('admin');
        $res = $this->request('GET', '/plugins/example-status');
        self::assertSame(200, $res->status(), $res->body());
        self::assertStringContainsString('Status note', $res->body());
        self::assertStringContainsString('href="/plugins/example-status"', $this->request('GET', '/')->body(), 'sidebar entry');
        self::assertSame(419, $this->request('POST', '/plugins/example-status/note', ['note' => 'x'])->status(), 'plugin routes are CSRF-protected');
        $this->post('/plugins/example-status/note', ['note' => 'Maintenance on Friday']);
        self::assertStringContainsString('Maintenance on Friday', $this->request('GET', '/plugins/example-status')->body());

        // A plugin that throws while booting is isolated.
        self::assertSame(404, $this->request('GET', '/plugins/broken')->status());
        self::assertStringContainsString('boom at boot', $this->request('GET', '/admin/plugins')->body());

        // Events reach plugin listeners.
        $this->app->container->get(WebsiteService::class)->create(['domain' => 'evt.test', 'kind' => 'static'], (array) $this->app->container->get(UserRepository::class)->find($admin));
        self::assertSame('evt.test', $this->app->container->get(\WhaCP\Repositories\SettingRepository::class)->all()['plugin.example-status.last_website'] ?? null);

        // Plugin permissions survive migrations and gate access.
        $this->app->container->get(RoleRepository::class)->syncDefaults();
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM permissions WHERE name = 'plugin.example-status.view'"));
        $this->fresh('cust');
        self::assertSame(403, $this->request('GET', '/plugins/example-status')->status());
        self::assertSame(403, $this->request('GET', '/admin/plugins')->status());

        // Disabling removes the pages and permissions.
        $this->fresh('admin');
        $this->post('/admin/plugins/example-status/toggle', ['enabled' => '0']);
        $this->fresh('admin');
        self::assertSame(404, $this->request('GET', '/plugins/example-status')->status());
        self::assertSame(0, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM permissions WHERE name LIKE 'plugin.example-status.%'"));
    }
}
