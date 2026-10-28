<?php

declare(strict_types=1);

namespace NimVor\Tests\Support;

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

/**
 * Full-kernel tests with a fake agent and a fresh database.
 */
abstract class HttpTestCase extends TestCase
{
    protected string $dir;
    protected ArraySession $session;
    protected FakeTransport $agent;
    protected Application $app;
    /** @var array<string, mixed> extra configuration for makeApp() */
    protected array $config = [];

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/nimvor-http-' . bin2hex(random_bytes(4));
        mkdir($this->dir);
        file_put_contents($this->dir . '/token', 'test-token');
        $this->session = new ArraySession();
        $this->agent = new FakeTransport();
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.3', 'fpm_binary' => '/usr/sbin/php-fpm8.3']]]);
        $this->agent->succeed('agent.status', ['sites_root' => '/home/nimvor', 'write_enabled' => true]);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->app = $this->makeApp();
    }

    protected function tearDown(): void
    {
        exec('rm -rf ' . escapeshellarg($this->dir));
    }

    protected function makeApp(): Application
    {
        $defaults = require dirname(__DIR__, 2) . '/config/app.php';
        $cfg = new Config(array_replace_recursive($defaults, [
            'database' => ['path' => $this->dir . '/test.sqlite'],
            'log' => ['file' => $this->dir . '/panel.log'],
            'agent' => ['token_file' => $this->dir . '/token'],
            'security' => ['key_file' => $this->dir . '/panel.key'],
        ], $this->config));
        $app = new Application($cfg);
        $app->container->instance(SessionInterface::class, $this->session);
        $app->container->instance(AgentTransport::class, $this->agent);
        $app->migrate();
        // The dashboard opens the setup wizard on the first visit; tests
        // about other pages start past it (SetupWizardTest resets it).
        $app->container->get(\NimVor\Services\SettingsService::class)->update(['setup_wizard_seen' => true, 'setup_wizard_done' => true], null);
        return $app;
    }

    protected function seedUser(string $username, string $role): int
    {
        $roles = $this->app->container->get(RoleRepository::class);
        return $this->app->container->get(UserRepository::class)->create([
            'username' => $username, 'email' => $username . '@example.test', 'display_name' => '',
            'password_hash' => (new PasswordHasher())->hash('a-long-test-password'),
            'role_id' => (int) $roles->findByName($role)['id'], 'is_active' => true,
        ]);
    }

    /**
     * @param array<string, mixed> $body
     * @param array<string, string> $headers
     */
    /** The client address of requests. */
    protected string $remoteIp = '192.0.2.10';

    protected function request(string $method, string $path, array $body = [], array $headers = [], string $raw = '', array $query = []): Response
    {
        return $this->app->handle(new Request($method, $path, $query, $body, ['REMOTE_ADDR' => $this->remoteIp], $headers, $raw));
    }

    /** @param array<string, mixed> $body */
    protected function post(string $path, array $body = []): Response
    {
        return $this->request('POST', $path, $body + ['_token' => $this->app->container->get(Csrf::class)->token()]);
    }

    protected function login(string $username): void
    {
        $res = $this->post('/login', ['username' => $username, 'password' => 'a-long-test-password']);
        self::assertSame('/', $res->header('Location'), 'login should redirect to the dashboard');
    }

    /** @return array<string, mixed> */
    protected function lastCall(string $action): array
    {
        foreach (array_reverse($this->agent->requests) as $r) {
            if ($r['path'] === '/v1/actions/' . $action) {
                return (array) json_decode((string) $r['body'], true);
            }
        }
        self::fail($action . ' was not called');
    }

    protected function calls(string $action): int
    {
        return count(array_filter($this->agent->requests, static fn (array $r): bool => $r['path'] === '/v1/actions/' . $action));
    }

    protected function flash(string $key): mixed
    {
        return $this->session->pullFlash($key);
    }
}
