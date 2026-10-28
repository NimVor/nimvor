<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use PHPUnit\Framework\TestCase;
use NimVor\Agent\AgentTransport;
use NimVor\Core\Application;
use NimVor\Core\Config;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\ArraySession;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\RoleRepository;
use NimVor\Repositories\UserRepository;
use NimVor\Security\Csrf;
use NimVor\Security\PasswordHasher;
use NimVor\Tests\Support\FakeTransport;

/**
 * Drives the full HTTP kernel (middleware, routing, controllers, views).
 */
final class ApplicationTest extends TestCase
{
    private string $dir;
    private ArraySession $session;
    private FakeTransport $agent;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/nimvor-http-' . bin2hex(random_bytes(4));
        mkdir($this->dir);
        file_put_contents($this->dir . '/token', 'test-token');
        $this->session = new ArraySession();
        $this->agent = new FakeTransport();
        $this->agent->succeed('system.info', [
            'agent_version' => '0.4.0', 'os' => ['pretty_name' => 'Ubuntu 24.04 LTS', 'supported' => true],
            'system' => ['hostname' => 'web1', 'kernel' => '6.8', 'cpu' => ['cores' => 2, 'model' => 'x'], 'uptime_seconds' => 100, 'timezone' => 'UTC', 'interfaces' => []],
        ]);
        $this->agent->succeed('discovery.inventory', [
            'websites' => ['websites' => [[
                'id' => 'abc123', 'domain' => 'example.com', 'aliases' => ['www.example.com'], 'enabled' => true, 'kind' => 'php',
                'document_root' => '/var/www/example.com', 'document_root_exists' => true, 'https' => true, 'https_redirect' => true,
                'php' => ['version' => '8.3', 'pool' => 'www', 'listen' => 'unix:/run/php/php8.3-fpm.sock', 'user' => 'www-data', 'resolved' => true],
                'ssl' => ['cert_file' => '/etc/x.pem', 'issuer' => 'R3', 'not_after' => '2030-01-01T00:00:00Z', 'days_remaining' => 900, 'status' => 'valid', 'lets_encrypt' => true, 'covers_domain' => true],
                'listen' => ['443 ssl'], 'config_files' => ['/etc/nginx/sites-available/example.com'], 'access_logs' => [], 'error_logs' => [],
                'managed_by' => '', 'proxy_pass' => '',
            ]], 'catch_all_vhosts' => 0, 'warnings' => []],
            'php' => ['versions' => [['version' => '8.3', 'is_default' => true, 'pool_count' => 1, 'cli_binary' => '/usr/bin/php8.3', 'fpm_binary' => '/usr/sbin/php-fpm8.3']], 'pools' => [], 'warnings' => []],
            'databases' => ['installed' => false, 'schemas' => [], 'warnings' => []],
            'redis' => ['installed' => false, 'warnings' => []],
            'certificates' => [], 'cron' => ['jobs' => [], 'warnings' => []], 'users' => [], 'services' => [],
        ]);
    }

    protected function tearDown(): void
    {
        foreach (glob($this->dir . '/*') ?: [] as $f) {
            @unlink($f);
        }
        @rmdir($this->dir);
    }

    private function app(): Application
    {
        $defaults = require dirname(__DIR__, 2) . '/config/app.php';
        $cfg = new Config(array_replace_recursive($defaults, [
            'database' => ['path' => $this->dir . '/test.sqlite'],
            'log' => ['file' => $this->dir . '/panel.log'],
            'agent' => ['token_file' => $this->dir . '/token'],
        ]));
        $app = new Application($cfg);
        $app->container->instance(SessionInterface::class, $this->session);
        $app->container->instance(AgentTransport::class, $this->agent);
        $app->migrate();
        $app->container->get(\NimVor\Services\SettingsService::class)->update(['setup_wizard_seen' => true, 'setup_wizard_done' => true], null);
        return $app;
    }

    private function seedUser(Application $app, string $username, string $role): void
    {
        $roles = $app->container->get(RoleRepository::class);
        $app->container->get(UserRepository::class)->create([
            'username' => $username, 'email' => $username . '@example.test', 'display_name' => '',
            'password_hash' => (new PasswordHasher())->hash('a-long-test-password'),
            'role_id' => (int) $roles->findByName($role)['id'], 'is_active' => true,
        ]);
    }

    /** @param array<string, mixed> $body */
    private function request(Application $app, string $method, string $path, array $body = [], array $headers = []): Response
    {
        return $app->handle(new Request($method, $path, [], $body, ['REMOTE_ADDR' => '192.0.2.10'], $headers));
    }

    private function token(Application $app): string
    {
        return $app->container->get(Csrf::class)->token();
    }

    private function login(Application $app, string $username): void
    {
        $res = $this->request($app, 'POST', '/login', ['username' => $username, 'password' => 'a-long-test-password', '_token' => $this->token($app)]);
        self::assertSame('/', $res->header('Location'), 'login should redirect to the dashboard');
    }

    public function testGuestsAreRedirectedToLogin(): void
    {
        $res = $this->request($this->app(), 'GET', '/websites');
        self::assertSame(303, $res->status());
        self::assertSame('/login', $res->header('Location'));
    }

    public function testJsonGuestsGet401(): void
    {
        $res = $this->request($this->app(), 'GET', '/dashboard/live', [], ['accept' => 'application/json']);
        self::assertSame(401, $res->status());
    }

    public function testSecurityHeadersArePresent(): void
    {
        $res = $this->request($this->app(), 'GET', '/login');
        self::assertSame(200, $res->status());
        self::assertStringContainsString("script-src 'self'", (string) $res->header('Content-Security-Policy'));
        self::assertStringNotContainsString('unsafe-inline', (string) $res->header('Content-Security-Policy'));
        self::assertSame('DENY', $res->header('X-Frame-Options'));
        self::assertSame('nosniff', $res->header('X-Content-Type-Options'));
    }

    public function testPostWithoutCsrfTokenIsRejected(): void
    {
        $app = $this->app();
        $this->seedUser($app, 'admin', 'admin');
        $res = $this->request($app, 'POST', '/login', ['username' => 'admin', 'password' => 'a-long-test-password']);
        self::assertSame(419, $res->status());
    }

    public function testLoginAndBrowseDiscoveredWebsites(): void
    {
        $app = $this->app();
        $this->seedUser($app, 'admin', 'admin');
        $this->login($app, 'admin');

        $res = $this->request($app, 'GET', '/');
        self::assertSame(200, $res->status());
        self::assertStringContainsString('web1', $res->body());

        $res = $this->request($app, 'GET', '/websites');
        self::assertSame(200, $res->status());
        self::assertStringContainsString('example.com', $res->body());

        $res = $this->request($app, 'GET', '/websites/abc123');
        self::assertSame(200, $res->status());
        self::assertStringContainsString('/var/www/example.com', $res->body());

        $res = $this->request($app, 'GET', '/websites/doesnotexist');
        self::assertSame(404, $res->status());

        foreach (['/php', '/databases', '/ssl/hosts', '/ssl/certificates', '/ssl/keys', '/ssl/tls', '/cron', '/discovery', '/system-users', '/users', '/settings', '/audit', '/profile'] as $path) {
            self::assertSame(200, $this->request($app, 'GET', $path)->status(), $path);
        }
    }

    public function testCliOnlyPhpVersionOffersFpmInstall(): void
    {
        $app = $this->app();
        $this->seedUser($app, 'admin', 'admin');
        $this->login($app, 'admin');
        // PHP 8.3 with only the command line (as the installer leaves it for
        // the panel), and the repositories offer only 8.3.
        $this->agent->succeed('discovery.inventory', [
            'websites' => ['websites' => [], 'catch_all_vhosts' => 0, 'warnings' => []],
            'php' => ['versions' => [['version' => '8.3', 'is_default' => true, 'pool_count' => 0, 'cli_binary' => '/usr/bin/php8.3', 'fpm_binary' => '']], 'pools' => [], 'warnings' => []],
            'databases' => ['installed' => false, 'schemas' => [], 'warnings' => []],
            'redis' => ['installed' => false, 'warnings' => []],
            'certificates' => [], 'cron' => ['jobs' => [], 'warnings' => []], 'users' => [], 'services' => [],
        ]);
        $this->agent->succeed('php.available', ['8.3']);
        $this->agent->succeed('services.status', []);
        $body = $this->request($app, 'GET', '/php')->body();
        self::assertStringContainsString('Install PHP-FPM', $body);
        self::assertStringContainsString('<option value="8.3">PHP 8.3</option>', $body);
        self::assertStringNotContainsString('No other versions available', $body);
    }

    public function testPreviewOperatingSystemIsMarked(): void
    {
        $app = $this->app();
        $this->seedUser($app, 'admin', 'admin');
        $this->login($app, 'admin');
        $badge = '>preview</span>';
        self::assertStringNotContainsString($badge, $this->request($app, 'GET', '/')->body(), 'Ubuntu 24.04 is not a preview');

        $this->agent->succeed('system.info', [
            'agent_version' => '1.0.0', 'os' => ['pretty_name' => 'Ubuntu 26.04 LTS', 'supported' => true, 'preview' => true],
            'system' => ['hostname' => 'web1', 'kernel' => '7.0', 'cpu' => ['cores' => 2, 'model' => 'x'], 'uptime_seconds' => 100, 'timezone' => 'UTC', 'interfaces' => []],
        ]);
        $this->agent->succeed('system.metrics', []);
        $this->agent->succeed('discovery.firewall', []);
        foreach (['/', '/server'] as $path) {
            $body = $this->request($app, 'GET', $path)->body();
            self::assertStringContainsString($badge, $body, $path);
            self::assertStringNotContainsString('unsupported', $body, $path);
        }
    }

    public function testViewerCannotManageUsersOrRunDiscovery(): void
    {
        $app = $this->app();
        $this->seedUser($app, 'viewer', 'viewer');
        $this->login($app, 'viewer');

        self::assertSame(200, $this->request($app, 'GET', '/websites')->status());
        self::assertSame(403, $this->request($app, 'GET', '/users')->status());
        self::assertSame(403, $this->request($app, 'GET', '/settings')->status());
        self::assertSame(403, $this->request($app, 'POST', '/discovery/run', ['_token' => $this->token($app)])->status());
        self::assertSame(403, $this->request($app, 'POST', '/users/1/delete', ['_token' => $this->token($app)])->status());

        $denied = $app->container->get(\NimVor\Database\Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'security.access_denied'");
        self::assertSame(4, (int) $denied);
    }

    public function testAgentOutageShowsFriendlyMessage(): void
    {
        $app = $this->app();
        $this->seedUser($app, 'admin', 'admin');
        $this->login($app, 'admin');
        $this->agent->down = true;

        $res = $this->request($app, 'GET', '/websites');
        self::assertSame(200, $res->status());
        self::assertStringContainsString('not reachable', $res->body());

        $res = $this->request($app, 'GET', '/dashboard/live', [], ['accept' => 'application/json']);
        self::assertSame(503, $res->status());
        self::assertFalse(json_decode($res->body(), true)['success']);
    }

    public function testUnknownRouteIs404AndWrongMethodIs405(): void
    {
        $app = $this->app();
        self::assertSame(404, $this->request($app, 'GET', '/definitely-not-here')->status());
        self::assertSame(419, $this->request($app, 'DELETE', '/login')->status(), 'CSRF is checked before routing');
        self::assertSame(405, $this->request($app, 'DELETE', '/login', [], ['x-csrf-token' => $this->token($app)])->status());
    }

    public function testInternalErrorsDoNotLeakDetails(): void
    {
        $app = $this->app();
        $this->seedUser($app, 'admin', 'admin');
        $this->login($app, 'admin');
        $this->agent->responses['/v1/actions/system.info'] = new \NimVor\Agent\AgentHttpResponse(500, '{"success":false,"error":{"code":"internal_error","message":"The agent could not complete the action."},"request_id":"r1"}');
        $res = $this->request($app, 'GET', '/server');
        self::assertStringNotContainsString('SQLSTATE', $res->body());
        self::assertStringNotContainsString('/home/', $res->body());
        self::assertStringContainsString('could not complete', $res->body());
    }

    public function testLogoutRequiresPostAndEndsSession(): void
    {
        $app = $this->app();
        $this->seedUser($app, 'admin', 'admin');
        $this->login($app, 'admin');
        self::assertSame(405, $this->request($app, 'GET', '/logout')->status());
        $this->request($app, 'POST', '/logout', ['_token' => $this->token($app)]);
        $fresh = $this->app();
        self::assertSame('/login', $this->request($fresh, 'GET', '/')->header('Location'));
    }
}
