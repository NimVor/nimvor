<?php

declare(strict_types=1);

namespace WhaCP\Http\Middleware;

use WhaCP\Core\Config;
use WhaCP\Database\Connection;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Route;
use WhaCP\Security\Authorizer;
use WhaCP\Services\ApiTokenService;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\ServerContext;

/**
 * Authentication for /api/v1: bearer tokens only (no session, no cookies,
 * no CSRF). The token's user must hold the route's permission; read-only
 * tokens may only use GET. X-WhaCP-Server selects the server.
 */
final class ApiAuthenticate implements Middleware
{
    public function __construct(
        private readonly ApiTokenService $tokens,
        private readonly Authorizer $authorizer,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly Connection $db,
        private readonly Config $config,
    ) {
    }

    public static function error(int $status, string $code, string $message, array $extra = []): Response
    {
        return Response::json(['success' => false, 'error' => ['code' => $code, 'message' => $message] + $extra], $status);
    }

    public function process(Request $request, callable $next): Response
    {
        /** @var Route|null $route */
        $route = $request->attribute('route');
        if ($route === null || !$route->requiresAuth) {
            return $next($request);
        }
        $ip = $request->ip((array) $this->config->get('security.trusted_proxies', []));
        [$scheme, $token] = array_pad(explode(' ', $request->header('authorization'), 2), 2, '');
        $auth = strcasecmp($scheme, 'Bearer') === 0 ? $this->tokens->authenticate(trim($token), $ip) : null;
        if ($auth === null) {
            $this->audit->record('api.auth_failed', 'route', $request->path(), AuditLogger::DENIED, ['ip' => $ip]);
            return self::error(401, 'unauthorized', 'A valid API token is required (Authorization: Bearer whacp_…).');
        }
        $user = $auth['user'];
        $this->audit->setActor((int) $user['id'], (string) $user['username'] . ' (api)');
        if (!$this->tokens->hit((int) $auth['token']['id'])) {
            return self::error(429, 'rate_limited', 'Too many requests; at most ' . ApiTokenService::RATE_PER_MINUTE . ' per minute per token.');
        }
        if ($auth['token']['ability'] === 'read' && !$request->isSafeMethod()) {
            return self::error(403, 'read_only_token', 'This token is read-only.');
        }
        if ($route->permission !== null && !$this->authorizer->can($user, $route->permission)) {
            $this->audit->record('security.access_denied', 'route', $request->path(), AuditLogger::DENIED, ['permission' => $route->permission, 'via' => 'api']);
            return self::error(403, 'forbidden', 'The token\'s user lacks the permission ' . $route->permission . '.');
        }
        $serverHeader = $request->header('x-whacp-server');
        if ($serverHeader !== '') {
            $sid = filter_var($serverHeader, FILTER_VALIDATE_INT);
            if ($sid === false || $this->db->scalar('SELECT COUNT(*) FROM servers WHERE id = ?', [$sid]) === 0) {
                return self::error(404, 'unknown_server', 'X-WhaCP-Server does not name a server.');
            }
            $this->server->force($sid);
        } else {
            $this->server->force(1);
        }
        return $next($request->withAttribute('user', $user)->withAttribute('api_token', $auth['token']));
    }
}
