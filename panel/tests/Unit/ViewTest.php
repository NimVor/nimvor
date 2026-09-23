<?php

declare(strict_types=1);

namespace WhaCP\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RuntimeException;
use WhaCP\Http\Router;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Security\Csrf;
use WhaCP\View\View;

final class ViewTest extends TestCase
{
    private View $view;

    protected function setUp(): void
    {
        $router = new Router();
        (require dirname(__DIR__, 2) . '/routes/web.php')($router);
        $base = dirname(__DIR__, 2);
        $this->view = new View($base . '/resources/views', $router, new Csrf(new ArraySession()), $base . '/public');
    }

    public function testRejectsTraversalInTemplateNames(): void
    {
        $this->expectException(RuntimeException::class);
        $this->view->render('../config/app', [], null);
    }

    public function testErrorTemplateEscapesOutput(): void
    {
        $html = $this->view->render('errors/error', ['title' => '<b>x</b>', 'message' => '<script>alert(1)</script>', 'status' => 500], null);
        self::assertStringNotContainsString('<script>alert(1)</script>', $html);
        self::assertStringContainsString('&lt;script&gt;', $html);
    }

    public function testStatusBadgeEscapesLabel(): void
    {
        self::assertStringContainsString('text-bg-success', $this->view->statusBadge('running'));
        self::assertStringNotContainsString('<i>', $this->view->statusBadge('x', '<i>'));
    }

    public function testPermissionCheckerDefaultsToDeny(): void
    {
        self::assertFalse($this->view->can('dashboard.view'));
        $this->view->setPermissionChecker(static fn (string $p): bool => $p === 'dashboard.view');
        self::assertTrue($this->view->can('dashboard.view'));
    }

    public function testEveryNamedRouteInTheSidebarExists(): void
    {
        $this->view->share('current_path', '/');
        $this->view->setPermissionChecker(static fn (): bool => true);
        $html = $this->view->partial('partials/sidebar');
        self::assertStringContainsString('href="/websites"', $html);
        self::assertStringContainsString('is-planned', $html);
    }
}
