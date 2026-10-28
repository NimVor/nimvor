<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use PHPUnit\Framework\TestCase;
use RuntimeException;
use NimVor\Http\Router;
use NimVor\Http\Session\ArraySession;
use NimVor\Security\Csrf;
use NimVor\View\View;

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
        self::assertStringNotContainsString('is-planned', $html, 'every sidebar entry is implemented');
    }

    /**
     * The Content-Security-Policy forbids inline scripts (script-src 'self'),
     * so a template must never carry an inline <script> block; page behaviour
     * belongs in an external asset loaded from the panel. Only <script src=…>
     * (and JSON data blocks, which do not execute) are allowed.
     */
    public function testNoViewUsesInlineScripts(): void
    {
        $dir = dirname(__DIR__, 2) . '/resources/views';
        $offenders = [];
        $it = new \RecursiveIteratorIterator(new \RecursiveDirectoryIterator($dir, \FilesystemIterator::SKIP_DOTS));
        foreach ($it as $file) {
            if ($file->getExtension() !== 'php') {
                continue;
            }
            // Match a <script> opening tag whose attributes carry neither a
            // src nor a non-JavaScript type (e.g. application/json data).
            if (preg_match_all('/<script\b([^>]*)>/i', (string) file_get_contents($file->getPathname()), $m)) {
                foreach ($m[1] as $attrs) {
                    if (!preg_match('/\bsrc\s*=/i', $attrs) && !preg_match('/\btype\s*=\s*["\']?(?!text\/javascript|application\/javascript)[^"\'\s>]+/i', $attrs)) {
                        $offenders[] = str_replace($dir . '/', '', $file->getPathname());
                    }
                }
            }
        }
        self::assertSame([], $offenders, 'inline <script> blocks violate the Content-Security-Policy; move the code to an external asset');
    }
}
