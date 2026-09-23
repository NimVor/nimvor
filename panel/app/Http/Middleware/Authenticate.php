<?php

declare(strict_types=1);

namespace WhaCP\Http\Middleware;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Route;
use WhaCP\Security\Authorizer;
use WhaCP\Security\AuthService;
use WhaCP\Services\AuditLogger;
use WhaCP\View\View;

/**
 * Enforces authentication and the route's permission.
 */
final class Authenticate implements Middleware
{
    public function __construct(
        private readonly AuthService $auth,
        private readonly Authorizer $authorizer,
        private readonly AuditLogger $audit,
        private readonly View $view,
    ) {
    }

    public function process(Request $request, callable $next): Response
    {
        $user = $this->auth->user();
        $this->view->share('user', $user);
        $this->view->setPermissionChecker(fn (string $p): bool => $this->authorizer->can($user, $p));
        if ($user !== null) {
            $this->audit->setActor((int) $user['id'], (string) $user['username']);
        }

        /** @var Route|null $route */
        $route = $request->attribute('route');
        if ($route === null || !$route->requiresAuth) {
            return $next($request);
        }
        if ($user === null) {
            if ($request->wantsJson()) {
                return Response::json(['success' => false, 'error' => ['message' => 'Your session has ended. Sign in again.']], 401);
            }
            return Response::redirect('/login');
        }
        if ($route->permission !== null && !$this->authorizer->can($user, $route->permission)) {
            $this->audit->record('security.access_denied', 'route', $request->path(), AuditLogger::DENIED, ['permission' => $route->permission]);
            $message = 'You do not have permission to access this page.';
            if ($request->wantsJson()) {
                return Response::json(['success' => false, 'error' => ['message' => $message]], 403);
            }
            return Response::html($this->view->render('errors/error', [
                'title' => 'Access denied',
                'message' => $message,
                'status' => 403,
            ]), 403);
        }
        return $next($request->withAttribute('user', $user));
    }
}
