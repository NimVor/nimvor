<?php

declare(strict_types=1);

namespace WhaCP\Tests\Security;

use WhaCP\Http\Router;
use WhaCP\Security\Permissions;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * Release gate: for every route with a permission and every default role
 * without that permission, a direct request is refused (403) and never
 * reaches the agent. Hidden buttons are not what protects these routes.
 */
final class RbacMatrixTest extends HttpTestCase
{
    public function testEveryRouteRefusesRolesWithoutItsPermission(): void
    {
        $router = $this->app->container->get(Router::class);
        $routes = (new \ReflectionProperty($router, 'routes'))->getValue($router);
        $roles = Permissions::defaultRoles();
        $checked = 0;
        $failures = [];
        foreach (array_keys($roles) as $role) {
            if ($role === 'admin') {
                continue;
            }
            $this->session = new \WhaCP\Http\Session\ArraySession();
            $this->app = $this->makeApp();
            $this->seedUser('u_' . $role, $role);
            $this->login('u_' . $role);
            $granted = array_fill_keys($roles[$role]['permissions'], true);
            foreach ($routes as $route) {
                if ($route->permission === null || isset($granted[$route->permission]) || str_starts_with($route->pattern, '/api/')) {
                    continue;
                }
                $url = preg_replace('/\{[a-z_]+\}/', '1', $route->pattern);
                $before = count($this->agent->requests);
                $res = $route->method === 'GET' ? $this->request('GET', $url) : $this->post($url, ['confirm' => '1', 'enabled' => '1']);
                $calls = array_filter(array_slice($this->agent->requests, $before), static fn (array $c): bool => !preg_match('#^/v1/(actions/)?(discovery\.|system\.|agent\.status|health)#', $c['path']));
                if ($res->status() !== 403 || $calls !== []) {
                    $failures[] = sprintf('%s %s %s (needs %s): status %d, agent calls %s', $role, $route->method, $route->pattern, $route->permission, $res->status(), implode(',', array_column($calls, 'path')));
                }
                $checked++;
            }
        }
        self::assertSame([], $failures, implode("\n", $failures));
        self::assertGreaterThan(200, $checked, 'the matrix covers the routes');
    }
}
