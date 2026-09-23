<?php

declare(strict_types=1);

namespace WhaCP\Tests\Unit;

use InvalidArgumentException;
use PHPUnit\Framework\TestCase;
use WhaCP\Http\Router;

final class RouterTest extends TestCase
{
    private Router $router;

    protected function setUp(): void
    {
        $this->router = new Router();
        $this->router->get('/users', ['X', 'index'], ['name' => 'users', 'permission' => 'users.view']);
        $this->router->get('/users/{id}/edit', ['X', 'edit'], ['name' => 'users.edit']);
        $this->router->post('/users/{id}', ['X', 'update'], ['name' => 'users.update']);
    }

    public function testMatchesStaticAndParameterisedRoutes(): void
    {
        [$route, $params] = $this->router->match('GET', '/users');
        self::assertSame('users', $route?->name);
        self::assertSame('users.view', $route->permission);

        [$route, $params] = $this->router->match('GET', '/users/42/edit');
        self::assertSame('users.edit', $route?->name);
        self::assertSame(['id' => '42'], $params);
    }

    public function testParametersRejectUnsafeCharacters(): void
    {
        [$route] = $this->router->match('GET', '/users/..%2F/edit');
        self::assertNull($route);
        [$route] = $this->router->match('GET', '/users/a b/edit');
        self::assertNull($route);
    }

    public function testMethodMismatchIsReported(): void
    {
        [$route, , $mismatch] = $this->router->match('DELETE', '/users/1');
        self::assertNull($route);
        self::assertTrue($mismatch);
    }

    public function testHeadFallsBackToGet(): void
    {
        [$route] = $this->router->match('HEAD', '/users');
        self::assertNotNull($route);
    }

    public function testUrlGenerationEncodesParameters(): void
    {
        self::assertSame('/users/7/edit', $this->router->url('users.edit', ['id' => 7]));
        self::assertSame('/users?page=2', $this->router->url('users', [], ['page' => 2]));
        self::assertSame('/users/a%2Fb/edit', $this->router->url('users.edit', ['id' => 'a/b']));
    }

    public function testUnknownRouteAndMissingParameterThrow(): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->router->url('users.edit');
    }

    public function testDuplicateNamesAreRejected(): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->router->get('/other', ['X', 'y'], ['name' => 'users']);
    }
}
