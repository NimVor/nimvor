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
        private readonly \WhaCP\Services\SettingsService $settings,
        private readonly \WhaCP\Security\PasswordPolicy $passwordPolicy,
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
        $this->view->share('route_checker', fn (string $name): bool => $this->authorizer->routeAllowed($user, $name));
        if ($user !== null) {
            $this->audit->setActor((int) $user['id'], (string) $user['username']);
            if (!$request->wantsJson()) {
                $this->view->share('agent_status', fn () => $this->agentStatus->get());
                $this->view->share('password_rules', fn (): string => $this->passwordPolicy->describe());
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
        // Security policy: Password Age.
        $maxAge = $this->settings->int('policy_password_age_days');
        $changed = strtotime((string) ($user['password_changed_at'] ?? '')) ?: 0;
        if ($maxAge > 0 && !self::setupAllowed((string) $route->name) && time() - $changed > $maxAge * 86400) {
            $message = sprintf('Your password is older than %d days. Choose a new password to continue.', $maxAge);
            if ($request->wantsJson()) {
                return Response::json(['success' => false, 'error' => ['message' => $message]], 403);
            }
            $this->session->flash('error', $message);
            return Response::redirect('/profile#password');
        }
        $featureOff = !$this->authorizer->routeAllowed($user, $route->name);
        if ($featureOff || ($route->permission !== null && !$this->authorizer->can($user, $route->permission))) {
            $this->audit->record('security.access_denied', 'route', $request->path(), AuditLogger::DENIED, $featureOff ? ['feature' => 'off', 'route' => $route->name] : ['permission' => $route->permission]);
            $message = $featureOff ? 'This feature is not included in your hosting plan.' : 'You do not have permission to access this page.';
            if ($request->wantsJson()) {
                return Response::json(['success' => false, 'error' => ['message' => $message]], 403);
            }
            return Response::html($this->view->render('errors/error', [
                'title' => 'Access denied',
                'message' => $message,
                'status' => 403,
            ]), 403);
        }
        // Step-up: changes to accounts, roles, API tokens and sign-in
        // policies need the password confirmed in the last 15 minutes.
        if (in_array($route->name, self::STEP_UP, true) && !$this->auth->recentlyConfirmed()) {
            $message = 'Confirm your password to continue, then submit again.';
            $back = self::safePath($request->header('Referer'));
            if ($request->wantsJson()) {
                return Response::json(['success' => false, 'error' => ['message' => $message], 'redirect' => '/confirm-password?return=' . rawurlencode($back)], 403);
            }
            $this->session->flash('error', $message);
            return Response::redirect('/confirm-password?return=' . rawurlencode($back));
        }
        return $next($request->withAttribute('user', $user));
    }

    /** Routes that need a recent password confirmation. */
    public const STEP_UP = [
        'users.store', 'users.update', 'users.delete', 'users.2fa.reset', 'accounts.store',
        'roles.store', 'roles.update', 'roles.2fa', 'roles.delete',
        'tokens.store', 'security.policies.save',
        // SSH keys of administrators grant a root-capable login.
        'security.ssh.key_add', 'security.ssh.key_generate', 'security.ssh.key_remove',
    ];

    /** A local path to return to ('/' when the value is not one). */
    public static function safePath(string $url): string
    {
        $path = (string) (parse_url($url, PHP_URL_PATH) ?? '/');
        $query = (string) (parse_url($url, PHP_URL_QUERY) ?? '');
        if ($path === '' || $path[0] !== '/' || str_starts_with($path, '//') || str_contains($path, '\\')) {
            return '/';
        }
        return $path . ($query !== '' ? '?' . $query : '');
    }

    /** Pages a user who must set up two-factor sign-in can still reach. */
    private static function setupAllowed(string $route): bool
    {
        return in_array($route, ['profile', 'profile.password', 'logout'], true) || str_starts_with($route, 'profile.2fa.');
    }
}
