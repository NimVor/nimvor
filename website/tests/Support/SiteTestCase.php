<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Support;

use NimVorSite\Core\App;
use NimVorSite\Core\Request;
use NimVorSite\Core\Response;
use NimVorSite\Services\Auth;
use PHPUnit\Framework\TestCase;

/**
 * Runs the whole application in-process against a real MySQL/MariaDB database
 * and a real cache (Redis by default, TEST_CACHE=file for none). Tests use the
 * database named by DB_NAME, which is emptied: never point it at real data.
 */
abstract class SiteTestCase extends TestCase
{
    protected App $app;
    /** @var array<string, string> */
    protected array $cookies = [];
    protected string $ip = '203.0.113.7';
    private static bool $schema = false;

    protected function setUp(): void
    {
        $env = static fn (string $k, string $d): string => getenv($k) !== false && getenv($k) !== '' ? (string) getenv($k) : $d;
        $config = [
            'env' => 'test',
            'url' => 'https://nimvor.test',
            'db' => ['host' => $env('DB_HOST', '127.0.0.1'), 'port' => (int) $env('DB_PORT', '3306'), 'name' => $env('DB_NAME', 'nimvor_site_test'), 'user' => $env('DB_USER', 'site'), 'pass' => $env('DB_PASS', 'site-test-pw')],
            'cache' => [
                'driver' => $env('TEST_CACHE', 'redis'),
                'prefix' => 'test:' . bin2hex(random_bytes(4)) . ':',
                'redis' => ['host' => $env('REDIS_HOST', '127.0.0.1'), 'port' => (int) $env('REDIS_PORT', '6379'), 'password' => null],
                'memcached' => ['host' => '127.0.0.1', 'port' => 11211],
                'file_dir' => sys_get_temp_dir() . '/nimvor-site-test-' . bin2hex(random_bytes(4)),
            ],
            'session_ttl' => 600,
            'trusted_proxy' => '127.0.0.1',
        ];
        $this->app = App::boot($config, dirname(__DIR__, 2));
        $db = $this->app->db;
        if (!self::$schema) {
            foreach (['visits', 'sponsors', 'build_requests', 'contact_messages', 'posts', 'users', 'migrations'] as $t) {
                $db->run("DROP TABLE IF EXISTS {$t}");
            }
            $db->migrate(dirname(__DIR__, 2) . '/database/migrations');
            self::$schema = true;
        }
        $db->run('SET FOREIGN_KEY_CHECKS = 0');
        foreach (['visits', 'sponsors', 'build_requests', 'contact_messages', 'posts', 'users'] as $t) {
            $db->run("TRUNCATE TABLE {$t}");
        }
        $db->run('SET FOREIGN_KEY_CHECKS = 1');
        $this->cookies = [];
        $this->ip = '203.0.113.' . random_int(2, 250);
    }

    /**
     * @param array<string, string> $post
     * @param array<string, string> $server
     */
    protected function request(string $method, string $path, array $post = [], array $server = []): Response
    {
        $query = [];
        if (str_contains($path, '?')) {
            [$path, $qs] = explode('?', $path, 2);
            parse_str($qs, $query);
        }
        $req = new Request($method, $path, $query, $post, $this->cookies, $server + ['REMOTE_ADDR' => $this->ip]);
        $res = $this->app->handle($req);
        foreach ($res->cookies() as $c) {
            [$pair] = explode(';', $c, 2);
            [$name, $value] = explode('=', $pair, 2);
            if (str_contains($c, 'Max-Age=0')) {
                unset($this->cookies[$name]);
            } else {
                $this->cookies[$name] = $value;
            }
        }
        return $res;
    }

    protected function get(string $path, array $server = []): Response
    {
        return $this->request('GET', $path, [], $server);
    }

    /** @param array<string, string> $data */
    protected function post(string $path, array $data = [], bool $withToken = true): Response
    {
        if ($withToken && !isset($data['_token'])) {
            $data['_token'] = $this->token();
        }
        return $this->request('POST', $path, $data);
    }

    /** A CSRF token for the current session, read from a page that has a form. */
    protected function token(): string
    {
        $html = $this->get('/support/contact')->body;
        preg_match('/name="_token" value="([a-f0-9]{64})"/', $html, $m);
        $this->assertNotEmpty($m, 'the contact page has a CSRF token');
        return $m[1];
    }

    protected function createUser(string $email, string $name = 'Test User', string $password = 'a-long-test-password', string $role = 'client'): int
    {
        return $this->app->users->create($email, $name, Auth::hash($password), $role);
    }

    protected function login(string $email, string $password = 'a-long-test-password'): Response
    {
        $res = $this->post('/login', ['email' => $email, 'password' => $password]);
        $this->assertSame(303, $res->status, 'login redirects');
        return $res;
    }

    protected function loginAs(string $role, string $email = 'person@example.test'): int
    {
        $id = $this->createUser($email, 'Person', 'a-long-test-password', $role);
        $this->login($email);
        return $id;
    }

    protected function row(string $sql, array $params = []): ?array
    {
        return $this->app->db->one($sql, $params);
    }

    protected function rows(string $table): int
    {
        return (int) $this->app->db->scalar("SELECT COUNT(*) FROM {$table}");
    }
}
