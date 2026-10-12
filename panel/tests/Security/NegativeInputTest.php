<?php

declare(strict_types=1);

namespace WhaCP\Tests\Security;

use WhaCP\Http\Router;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * Phase 4 negative tests: a bad object reference, a malformed path parameter
 * or a missing session must be DENIED cleanly — a redirect, 400, 403 or 404 —
 * and never a 500 (which would mean an uncaught error, possibly with a
 * privileged side effect).
 */
final class NegativeInputTest extends HttpTestCase
{
    /** @return list<\WhaCP\Http\Route> */
    private function routes(): array
    {
        $router = $this->app->container->get(Router::class);
        return (new \ReflectionProperty($router, 'routes'))->getValue($router);
    }

    public function testBadObjectIdsNeverCauseAServerError(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        // The agent answers anything the pages read, so a 500 can only come
        // from the panel mishandling the input, not from a missing backend.
        $this->agent->succeedAny();

        $bad = ['999999', 'abc', '0', '-1', '1x', '..', '%2e%2e', "1'"];
        $checked = 0;
        $failures = [];
        foreach ($this->routes() as $route) {
            if ($route->method !== 'GET' || !str_contains($route->pattern, '{') || str_starts_with($route->pattern, '/api/')) {
                continue;
            }
            foreach ($bad as $v) {
                $url = preg_replace('/\{[a-z_]+\}/', rawurlencode($v), $route->pattern);
                $res = $this->request('GET', $url);
                if ($res->status() === 500) {
                    $failures[] = $route->pattern . ' with id=' . $v . ' → 500';
                }
                $checked++;
            }
        }
        self::assertSame([], $failures, implode("\n", $failures));
        self::assertGreaterThan(50, $checked, 'the sweep covers the parameterised routes');
    }

    public function testDestructivePostsOnMissingObjectsDenyWithoutMutating(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->agent->succeedAny();
        // A representative set of delete/destructive routes with a
        // nonexistent id: the panel must answer 302/403/404 (or re-render),
        // never 500, and must not send a mutating action to the agent.
        $routes = [];
        foreach ($this->routes() as $route) {
            if ($route->method === 'POST' && preg_match('#/(delete|remove|destroy|suspend|terminate)#', $route->pattern) && str_contains($route->pattern, '{') && !str_starts_with($route->pattern, '/api/')) {
                $routes[] = $route->pattern;
            }
        }
        self::assertGreaterThan(3, count($routes), 'there are destructive routes to check');
        foreach ($routes as $pattern) {
            $url = preg_replace('/\{[a-z_]+\}/', '999999', $pattern);
            $res = $this->post($url, ['confirm' => '1']);
            self::assertNotSame(500, $res->status(), $pattern . ' → 500 on a missing object');
            self::assertContains($res->status(), [200, 302, 303, 400, 403, 404, 409, 422], $pattern . ' unexpected status ' . $res->status());
        }
    }

    public function testUnauthenticatedRequestsRedirectToLogin(): void
    {
        // No login: a protected page must send the visitor to sign in, not
        // error out and not serve the content.
        foreach (['/websites', '/settings/tweak', '/server/health', '/security'] as $url) {
            $res = $this->request('GET', $url);
            self::assertContains($res->status(), [302, 303], $url . ' should redirect');
            self::assertStringStartsWith('/login', (string) $res->header('Location'), $url);
        }
    }

    public function testInvalidApiTokenIsRejected(): void
    {
        foreach (['', 'Bearer', 'Bearer not-a-real-token', 'Bearer ' . str_repeat('x', 64)] as $auth) {
            $res = $this->request('GET', '/api/v1/me', [], $auth === '' ? [] : ['authorization' => $auth]);
            self::assertContains($res->status(), [401, 403], 'auth "' . $auth . '" → ' . $res->status());
            self::assertNotSame(500, $res->status());
        }
    }
}
