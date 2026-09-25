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
        private readonly \WhaCP\Services\ServerContext $serverContext,
        private readonly \WhaCP\Services\ServerService $servers,
        private readonly AuthService $auth,
        private readonly Authorizer $authorizer,
        private readonly AuditLogger $audit,
        private readonly View $view,
        private readonly \WhaCP\Services\AgentStatus $agentStatus,
        private readonly \WhaCP\Security\TwoFactor $twoFactor,
        private readonly \WhaCP\Security\WebAuthn $webAuthn,
        private readonly \WhaCP\Http\Session\SessionInterface $session,
        private readonly \WhaCP\Services\NotificationService $notifications,
    ) {
    }

    /** Automatic requests that must not keep an idle session alive. */
    private const PASSIVE_ROUTES = ['notifications.summary'];

    public function process(Request $request, callable $next): Response
    {
        /** @var Route|null $current */
        $current = $request->attribute('route');
        if ($current !== null && in_array($current->name, self::PASSIVE_ROUTES, true)) {
            $this->auth->passive();
        }
        $user = $this->auth->user();
        $this->view->share('user', $user);
        $this->view->setPermissionChecker(fn (string $p): bool => $this->authorizer->can($user, $p));
        if ($user !== null) {
            $this->audit->setActor((int) $user['id'], (string) $user['username']);
            if (!$request->wantsJson()) {
                $this->view->share('agent_status', fn () => $this->agentStatus->get());
                $this->view->share('notification_count', fn (): ?int => $this->authorizer->can($user, \WhaCP\Security\Permissions::NOTIFICATIONS_VIEW)
                    ? $this->notifications->unreadCount($user) : null);
                $this->view->share('server_switcher', fn (): array => [
                    'current' => $this->serverContext->id(),
                    'servers' => $this->servers->visibleTo($user),
                ]);
            }
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
        if (!empty($user['role_requires_2fa']) && !self::setupAllowed((string) $route->name) && !$this->twoFactor->enabled((int) $user['id']) && !$this->webAuthn->has((int) $user['id'])) {
            $message = 'Your role requires two-factor sign-in. Set it up to continue.';
            if ($request->wantsJson()) {
                return Response::json(['success' => false, 'error' => ['message' => $message]], 403);
            }
            $this->session->flash('error', $message);
            return Response::redirect('/profile#two-factor');
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

    /** Pages a user who must set up two-factor sign-in can still reach. */
    private static function setupAllowed(string $route): bool
    {
        return in_array($route, ['profile', 'profile.password', 'logout'], true) || str_starts_with($route, 'profile.2fa.');
    }
}
