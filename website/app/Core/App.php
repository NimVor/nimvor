<?php

declare(strict_types=1);

namespace NimVorSite\Core;

use NimVorSite\Cache\Cache;
use NimVorSite\Cache\CacheFactory;
use NimVorSite\Http\AccountController;
use NimVorSite\Http\AdminController;
use NimVorSite\Http\AuthController;
use NimVorSite\Http\PageController;
use NimVorSite\Repositories\BuildRequests;
use NimVorSite\Repositories\Messages;
use NimVorSite\Repositories\Posts;
use NimVorSite\Repositories\Sponsors;
use NimVorSite\Repositories\Users;
use NimVorSite\Repositories\Visits;
use NimVorSite\Services\Auth;
use NimVorSite\Services\RateLimiter;
use NimVorSite\Services\Reports;

final class App
{
    public readonly Users $users;
    public readonly Posts $posts;
    public readonly Messages $messages;
    public readonly BuildRequests $requests;
    public readonly Sponsors $sponsors;
    public readonly Visits $visits;
    public readonly RateLimiter $limiter;
    public readonly Reports $reports;
    private readonly Router $router;

    /** @param array<string, mixed> $config */
    public function __construct(public readonly array $config, public readonly Db $db, public readonly Cache $cache, private readonly string $root)
    {
        $this->users = new Users($db);
        $this->posts = new Posts($db);
        $this->messages = new Messages($db);
        $this->requests = new BuildRequests($db);
        $this->sponsors = new Sponsors($db);
        $this->visits = new Visits($db);
        $this->limiter = new RateLimiter($cache);
        $this->reports = new Reports($db, $cache, $this->visits);
        $this->router = new Router();
        $this->routes();
    }

    /** @param array<string, mixed> $config */
    public static function boot(array $config, string $root): self
    {
        return new self($config, new Db($config['db']), CacheFactory::make($config['cache']), $root);
    }

    public function root(): string
    {
        return $this->root;
    }

    private function routes(): void
    {
        $r = $this->router;
        $p = new PageController($this);
        $a = new AuthController($this);
        $c = new AccountController($this);
        $m = new AdminController($this);

        $r->add('GET', '/', [$p, 'home']);
        $r->add('GET', '/features', [$p, 'features']);
        $r->add('GET', '/resources', [$p, 'resources']);
        $r->add('GET', '/resources/documentation', [$p, 'documentation']);
        $r->add('GET', '/resources/blog', [$p, 'blog']);
        $r->add('GET', '/resources/blog/{slug}', [$p, 'post']);
        $r->add('GET', '/resources/community', [$p, 'community']);
        $r->add('GET', '/resources/build-services', [$p, 'buildServices']);
        $r->add('POST', '/resources/build-services', [$p, 'buildServicesSubmit']);
        $r->add('GET', '/resources/sponsors', [$p, 'sponsors']);
        $r->add('GET', '/resources/about', [$p, 'about']);
        $r->add('GET', '/support', [$p, 'support']);
        $r->add('GET', '/support/contact', [$p, 'contact']);
        $r->add('POST', '/support/contact', [$p, 'contactSubmit']);
        $r->add('GET', '/support/privacy', [$p, 'privacy']);
        $r->add('GET', '/sponsor', [$p, 'sponsorRedirect']);
        $r->add('GET', '/robots.txt', [$p, 'robots']);
        $r->add('GET', '/sitemap.xml', [$p, 'sitemap']);
        $r->add('GET', '/health', [$p, 'health']);

        $r->add('GET', '/login', [$a, 'loginForm']);
        $r->add('POST', '/login', [$a, 'login']);
        $r->add('GET', '/register', [$a, 'registerForm']);
        $r->add('POST', '/register', [$a, 'register']);
        $r->add('POST', '/logout', [$a, 'logout']);

        $r->add('GET', '/account', [$c, 'dashboard'], 'user');
        $r->add('GET', '/account/requests', [$c, 'requests'], 'user');
        $r->add('GET', '/account/messages', [$c, 'messages'], 'user');
        $r->add('GET', '/account/profile', [$c, 'profile'], 'user');
        $r->add('POST', '/account/profile', [$c, 'profileSave'], 'user');

        $r->add('GET', '/admin', [$m, 'overview'], 'admin');
        $r->add('GET', '/admin/reports', [$m, 'reports'], 'admin');
        $r->add('GET', '/admin/reports/{type}.csv', [$m, 'csv'], 'admin');
        $r->add('GET', '/admin/users', [$m, 'users'], 'admin');
        $r->add('POST', '/admin/users/{id}/active', [$m, 'userActive'], 'admin');
        $r->add('GET', '/admin/messages', [$m, 'messages'], 'admin');
        $r->add('POST', '/admin/messages/{id}/status', [$m, 'messageStatus'], 'admin');
        $r->add('GET', '/admin/requests', [$m, 'requests'], 'admin');
        $r->add('POST', '/admin/requests/{id}/status', [$m, 'requestStatus'], 'admin');
        $r->add('GET', '/admin/posts', [$m, 'posts'], 'admin');
        $r->add('GET', '/admin/posts/new', [$m, 'postForm'], 'admin');
        $r->add('POST', '/admin/posts/new', [$m, 'postSave'], 'admin');
        $r->add('GET', '/admin/posts/{id}/edit', [$m, 'postForm'], 'admin');
        $r->add('POST', '/admin/posts/{id}/edit', [$m, 'postSave'], 'admin');
        $r->add('POST', '/admin/posts/{id}/delete', [$m, 'postDelete'], 'admin');
        $r->add('GET', '/admin/sponsors', [$m, 'sponsors'], 'admin');
        $r->add('POST', '/admin/sponsors', [$m, 'sponsorAdd'], 'admin');
        $r->add('POST', '/admin/sponsors/{id}/delete', [$m, 'sponsorDelete'], 'admin');
    }

    public function handle(Request $req): Response
    {
        $session = new Session($this->cache, (int) $this->config['session_ttl']);
        $session->load($req->cookie(Session::COOKIE));
        $auth = new Auth($this->users, $session, $this->limiter);
        $view = new View($this->root . '/resources/views', $this->root . '/public', [
            'path' => $req->path, 'siteUrl' => $this->config['url'], 'flash' => null, 'user' => null, 'errors' => [], 'old' => [],
        ]);
        $ctx = new Ctx($this, $req, $session, $auth, $view);
        $view->share('v', $view);

        $route = null;
        try {
            $resp = $this->dispatch($ctx, $route);
        } catch (\Throwable $e) {
            error_log('nimvor-site: ' . $e::class . ': ' . $e->getMessage() . ' in ' . $e->getFile() . ':' . $e->getLine());
            $resp = $this->error($ctx, 500);
        }

        if ($resp->status === 200 && $req->method === 'GET' && $route !== null && $route['auth'] === null && !str_starts_with($route['pattern'], '/health') && !in_array($route['pattern'], ['/robots.txt', '/sitemap.xml', '/login', '/register'], true)) {
            try {
                $this->visits->record($route['pattern']);
            } catch (\Throwable) {
                // A statistics failure never breaks a page.
            }
        }
        $secure = $req->isHttps((string) $this->config['trusted_proxy']);
        if (($cookie = $session->commit($secure)) !== null) {
            $resp->cookie($cookie);
        }
        return $this->secure($resp, $req, $secure, $auth->user() !== null || $resp->cookies() !== [] || !in_array($req->method, ['GET', 'HEAD'], true));
    }

    /** @param array{pattern: string, auth: ?string}|null $route */
    private function dispatch(Ctx $ctx, ?array &$route): Response
    {
        $req = $ctx->req;
        $m = $this->router->match($req->method, $req->path);
        if ($m === null) {
            return $this->error($ctx, 404);
        }
        if ($m === 'method') {
            return $this->error($ctx, 405)->header('Allow', 'GET, POST');
        }
        $route = ['pattern' => $m['route']['pattern'], 'auth' => $m['route']['auth']];
        if ($req->method === 'POST' && !$ctx->session->checkCsrf($req->input('_token'))) {
            return $this->error($ctx, 419);
        }
        if ($m['route']['auth'] !== null) {
            if ($ctx->user() === null) {
                $ctx->flash('info', 'Sign in to continue.');
                return $ctx->redirect('/login?next=' . rawurlencode($req->path));
            }
            if ($m['route']['auth'] === 'admin' && !$ctx->auth->isAdmin()) {
                return $this->error($ctx, 403);
            }
        }
        return ($m['route']['handler'])($ctx, $m['params']);
    }

    public function error(Ctx $ctx, int $status): Response
    {
        $known = [403, 404, 405, 419, 429, 500];
        try {
            return $ctx->render('errors/' . (in_array($status, $known, true) ? $status : 500), [], $status);
        } catch (\Throwable) {
            return Response::text('Error ' . $status, 'text/plain', $status);
        }
    }

    private function secure(Response $r, Request $req, bool $https, bool $private): Response
    {
        $r->header('Content-Security-Policy', "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'");
        $r->header('X-Content-Type-Options', 'nosniff');
        $r->header('X-Frame-Options', 'DENY');
        $r->header('Referrer-Policy', 'strict-origin-when-cross-origin');
        $r->header('Permissions-Policy', 'camera=(), microphone=(), geolocation=(), payment=()');
        $r->header('Cross-Origin-Opener-Policy', 'same-origin');
        if ($https) {
            $r->header('Strict-Transport-Security', 'max-age=31536000; includeSubDomains');
        }
        // Pages that depend on who is signed in, or that set a cookie, are never shared or stored.
        $r->header('Cache-Control', $private ? 'private, no-store' : 'public, max-age=60');
        return $r;
    }
}
