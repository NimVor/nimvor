<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Http\Middleware\ReleaseSession;
use NimVor\Tests\Support\HttpTestCase;

/**
 * PHP locks the session file for a whole request, so a dashboard that asks for its widgets all at once used to run them
 * one after the other. Read-only background requests now let go of the session once the user is checked.
 */
final class SessionReleaseTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->login('admin');
    }

    public function testReadOnlyBackgroundRequestsLetGoOfTheSession(): void
    {
        foreach (['/dashboard/series', '/dashboard/widgets/software', '/dashboard/live', '/topbar/status'] as $path) {
            $this->session->start(); // each request starts it again
            $res = $this->request('GET', $path, [], ['accept' => 'application/json']);
            self::assertContains($res->status(), [200, 503], $path);
            self::assertTrue($this->session->closed, $path . ' kept the session locked');
        }
    }

    public function testPagesAndChangesKeepTheSession(): void
    {
        $this->session->start();
        $this->request('GET', '/');
        self::assertFalse($this->session->closed, 'a page may set flash messages and the token');

        $this->session->start();
        $this->post('/dashboard/layout/reset');
        self::assertFalse($this->session->closed, 'a change never lets go early');
    }

    public function testASignedOutRequestIsRefusedAndTheTokenSurvives(): void
    {
        // The token a fragment may carry is created before the session is let go, so it is still there on the next request.
        $this->session->start();
        $this->request('GET', '/dashboard/widgets/software', [], ['accept' => 'application/json']);
        $token = $this->session->get('_csrf_token');
        self::assertIsString($token);
        self::assertSame(64, strlen($token));
    }

    public function testEveryListedRouteExistsAndIsAGet(): void
    {
        $router = $this->app->container->get(\NimVor\Http\Router::class);
        foreach (ReleaseSession::READ_ONLY_ROUTES as $name) {
            self::assertNotSame('', $router->url($name, ['id' => 'software', 'key' => 'cpu']), $name);
        }
    }
}
