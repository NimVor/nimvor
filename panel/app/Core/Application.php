<?php

declare(strict_types=1);

namespace WhaCP\Core;

use Throwable;
use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentTransport;
use WhaCP\Agent\UnixSocketTransport;
use WhaCP\Database\Connection;
use WhaCP\Database\Migrator;
use WhaCP\Http\Middleware\Authenticate;
use WhaCP\Http\Middleware\Middleware;
use WhaCP\Http\Middleware\SecurityHeaders;
use WhaCP\Http\Middleware\VerifyCsrfToken;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Router;
use WhaCP\Http\Session\NativeSession;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\LoginAttemptRepository;
use WhaCP\Security\AuthService;
use WhaCP\Security\Csrf;
use WhaCP\Security\LoginThrottle;
use WhaCP\Security\PasswordHasher;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\SettingsService;
use WhaCP\Support\Logger;
use WhaCP\View\View;

/**
 * Composition root and HTTP kernel.
 */
final class Application
{
    public readonly Container $container;

    public function __construct(public readonly Config $config, ?Container $container = null)
    {
        $this->container = $container ?? new Container();
        $this->register();
    }

    public static function basePath(string $path = ''): string
    {
        return dirname(__DIR__, 2) . ($path === '' ? '' : '/' . ltrim($path, '/'));
    }

    /**
     * Build the application from config/app.php and the local override
     * (WHACP_CONFIG, default /etc/whacp/panel.php).
     */
    public static function fromEnvironment(): self
    {
        $override = getenv('WHACP_CONFIG');
        $override = is_string($override) && $override !== '' ? $override : '/etc/whacp/panel.php';
        return new self(Config::load(self::basePath('config/app.php'), $override));
    }

    private function register(): void
    {
        $c = $this->container;
        $cfg = $this->config;
        $c->instance(Config::class, $cfg);
        $c->instance(self::class, $this);

        $c->set(Logger::class, static fn () => new Logger($cfg->string('log.file'), $cfg->string('log.level', 'info')));
        $c->set(Connection::class, static fn () => Connection::sqlite($cfg->string('database.path')));
        $c->set(Migrator::class, static fn (Container $c) => new Migrator($c->get(Connection::class), self::basePath('database/migrations')));
        $c->set(SessionInterface::class, static fn () => new NativeSession(
            $cfg->string('session.name', 'whacp_session'),
            $cfg->string('session.path'),
            $cfg->bool('session.secure_cookie', true),
        ));
        $c->set(Router::class, function (): Router {
            $router = new Router();
            (require self::basePath('routes/web.php'))($router);
            return $router;
        });
        $c->set(View::class, static fn (Container $c) => new View(
            self::basePath('resources/views'),
            $c->get(Router::class),
            $c->get(Csrf::class),
            self::basePath('public'),
        ));
        $c->set(LoginThrottle::class, static fn (Container $c) => new LoginThrottle(
            $c->get(LoginAttemptRepository::class),
            $cfg->int('security.login_max_per_ip', 10),
            $cfg->int('security.login_max_per_user', 5),
            $cfg->int('security.login_window_seconds', 900),
        ));
        $c->set(AuthService::class, static function (Container $c) use ($cfg): AuthService {
            /** @var SettingsService $settings */
            $settings = $c->get(SettingsService::class);
            return new AuthService(
                $c->get(SessionInterface::class),
                $c->get(UserRepository::class),
                $c->get(PasswordHasher::class),
                $c->get(LoginThrottle::class),
                $c->get(AuditLogger::class),
                $settings->int('session_idle_minutes') * 60,
                $cfg->int('session.absolute_lifetime', 43200),
            );
        });
        $c->set(AgentTransport::class, static fn () => new UnixSocketTransport($cfg->string('agent.socket')));
        $c->set(AgentClient::class, static fn (Container $c) => new AgentClient(
            $c->get(AgentTransport::class),
            $cfg->string('agent.token_file'),
            $c->get(Logger::class),
            $cfg->int('agent.timeout', 30),
        ));
        $c->set(ErrorHandler::class, static fn (Container $c) => new ErrorHandler(
            $c->get(Logger::class),
            $c->get(View::class),
            $cfg->bool('app.debug'),
        ));
    }

    public function handle(Request $request): Response
    {
        try {
            /** @var SessionInterface $session */
            $session = $this->container->get(SessionInterface::class);
            $session->start();

            /** @var AuditLogger $audit */
            $audit = $this->container->get(AuditLogger::class);
            /** @var list<string> $proxies */
            $proxies = (array) $this->config->get('security.trusted_proxies', []);
            $audit->setRequest($request, $proxies);

            /** @var View $view */
            $view = $this->container->get(View::class);
            $view->share('current_path', $request->path());
            $view->share('flash_success', $session->pullFlash('success'));
            $view->share('flash_error', $session->pullFlash('error'));
            $view->share('flash_details', $session->pullFlash('details'));
            $view->share('flash_job', $session->pullFlash('job'));
            $view->share('errors', $session->pullFlash('errors', []));
            $view->share('old', $session->pullFlash('old', []));
            $view->share('app_version', $this->config->string('app.version'));
            try {
                $view->share('panel_name', $this->container->get(SettingsService::class)->string('panel_name'));
            } catch (Throwable) {
                $view->share('panel_name', 'WhaCP');
            }

            $pipeline = [
                $this->container->get(SecurityHeaders::class),
                $this->container->get(VerifyCsrfToken::class),
                $this->container->get(Authenticate::class),
            ];
            return $this->run($pipeline, $request);
        } catch (Throwable $e) {
            return (new SecurityHeaders())->process($request, fn () => $this->errorHandler()->handle($e, $request));
        }
    }

    /** @param list<Middleware> $middleware */
    private function run(array $middleware, Request $request): Response
    {
        /** @var Router $router */
        $router = $this->container->get(Router::class);
        [$route, $params, $methodMismatch] = $router->match($request->method(), $request->path());
        if ($route !== null) {
            $request = $request->withAttribute('route', $route)->withAttribute('params', $params);
        }

        $core = function (Request $req) use ($route, $params, $methodMismatch): Response {
            if ($route === null) {
                return $this->notFound($req, $methodMismatch ? 405 : 404);
            }
            [$class, $method] = $route->handler;
            $controller = $this->container->get($class);
            try {
                return $controller->{$method}($req, ...array_values($params));
            } catch (Throwable $e) {
                return $this->errorHandler()->handle($e, $req);
            }
        };

        $next = $core;
        foreach (array_reverse($middleware) as $mw) {
            $next = static fn (Request $req): Response => $mw->process($req, $next);
        }
        return $next($request);
    }

    private function notFound(Request $request, int $status): Response
    {
        $message = $status === 405 ? 'This action is not allowed here.' : 'The page you requested does not exist.';
        if ($request->wantsJson()) {
            return Response::json(['success' => false, 'error' => ['message' => $message]], $status);
        }
        /** @var View $view */
        $view = $this->container->get(View::class);
        $layout = $view->shared('user') === null ? 'layouts/minimal' : 'layouts/app';
        return Response::html($view->render('errors/error', ['title' => $status === 405 ? 'Method not allowed' : 'Not found', 'message' => $message, 'status' => $status], $layout), $status);
    }

    private function errorHandler(): ErrorHandler
    {
        try {
            /** @var ErrorHandler */
            return $this->container->get(ErrorHandler::class);
        } catch (Throwable) {
            return new ErrorHandler(new Logger($this->config->string('log.file', sys_get_temp_dir() . '/whacp-panel.log')), null, false);
        }
    }

    public function migrate(): void
    {
        /** @var Migrator $migrator */
        $migrator = $this->container->get(Migrator::class);
        $migrator->migrate();
        $this->container->get(\WhaCP\Repositories\RoleRepository::class)->syncDefaults();
    }
}
