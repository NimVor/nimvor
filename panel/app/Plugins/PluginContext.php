<?php

declare(strict_types=1);

namespace NimVor\Plugins;

use NimVor\Agent\AgentClient;
use NimVor\Core\Container;
use NimVor\Database\Connection;
use NimVor\Http\Controllers\PluginDispatchController;
use NimVor\Http\Response;
use NimVor\Http\Router;
use NimVor\Repositories\SettingRepository;
use NimVor\Security\Permissions;
use NimVor\Services\AuditLogger;
use NimVor\Support\Events;
use NimVor\View\View;

/**
 * What a plugin can do. Passed to the function returned by plugin.php:
 *
 *     return function (NimVor\Plugins\PluginContext $ctx): void {
 *         $ctx->route('GET', '/', fn ($request) => $ctx->render('index', []), 'plugin.hello.view');
 *         $ctx->nav('Hello', 'emoji-smile', '/', 'plugin.hello.view');
 *     };
 *
 * Routes live under /plugins/<name>/ and must require a permission (the
 * plugin's own, or a core one). They get the same CSRF protection and
 * authentication as core pages.
 */
final class PluginContext
{
    private int $routes = 0;
    private bool $active = true;

    /** @param array<string, string> $permissions */
    public function __construct(
        public readonly string $name,
        private readonly string $path,
        private readonly array $permissions,
        private readonly Router $router,
        private readonly PluginManager $manager,
        private readonly Container $container,
    ) {
    }

    private function allowedPermission(string $p): bool
    {
        return isset($this->permissions[$p]) || isset(Permissions::all()[$p]);
    }

    /**
     * Register a page or action. $handler receives the Request (and path
     * parameters) and returns a Response or a string of HTML.
     */
    public function route(string $method, string $path, \Closure $handler, string $permission): string
    {
        $method = strtoupper($method);
        if (!in_array($method, ['GET', 'POST'], true)) {
            throw new \InvalidArgumentException('Plugin routes support GET and POST.');
        }
        if (!preg_match('#^/[a-z0-9/{}_-]*$#', $path) || str_contains($path, '..')) {
            throw new \InvalidArgumentException("Invalid plugin route path {$path}.");
        }
        if (!$this->allowedPermission($permission)) {
            throw new \InvalidArgumentException("Unknown permission {$permission}; declare it in plugin.json.");
        }
        $name = 'plugin.' . $this->name . '.' . (++$this->routes);
        $full = '/plugins/' . $this->name . rtrim($path, '/');
        $this->router->add($method, $full, [PluginDispatchController::class, 'dispatch'], ['name' => $name, 'permission' => $permission]);
        $this->manager->addHandler($name, $handler);
        return $name;
    }

    /** Add a sidebar entry (path relative to the plugin, e.g. "/"). */
    public function nav(string $label, string $icon, string $path, string $permission): void
    {
        if (!$this->allowedPermission($permission) || !preg_match('/^[a-z0-9-]{1,40}$/', $icon)) {
            throw new \InvalidArgumentException('Invalid navigation entry.');
        }
        $url = '/plugins/' . $this->name . rtrim($path, '/');
        $this->manager->addNav(['label' => mb_substr($label, 0, 40), 'icon' => $icon, 'route' => 'plugin.' . $this->name . '.', 'url' => $url, 'permission' => $permission]);
    }

    /** Listen to a panel event (website.created, backup.completed, …). */
    public function on(string $event, \Closure $listener): void
    {
        $safe = function (array $payload) use ($listener, $event): void {
            if (!$this->active) {
                return;
            }
            try {
                $listener($payload);
            } catch (\Throwable $e) {
                $this->container->get(\NimVor\Support\Logger::class)->error('plugin listener failed', ['plugin' => $this->name, 'event' => $event, 'error' => $e->getMessage()]);
            }
        };
        $this->container->get(Events::class)->on($event, $safe);
    }

    /** Render views/<template>.php of the plugin inside the panel layout. */
    public function render(string $template, array $data = [], int $status = 200): Response
    {
        if (!preg_match('/^[a-z0-9_-]+$/', $template)) {
            throw new \InvalidArgumentException('Invalid template name.');
        }
        return Response::html($this->container->get(View::class)->renderExternal($this->path . '/views', $template, $data + ['plugin' => $this->name]), $status);
    }

    public function agent(): AgentClient
    {
        return $this->container->get(AgentClient::class);
    }

    public function db(): Connection
    {
        return $this->container->get(Connection::class);
    }

    public function setting(string $key, ?string $default = null): ?string
    {
        $v = $this->container->get(SettingRepository::class)->all()['plugin.' . $this->name . '.' . $key] ?? null;
        return $v === null ? $default : (string) $v;
    }

    public function setSetting(string $key, string $value): void
    {
        if (!preg_match('/^[a-z0-9_.-]{1,60}$/', $key)) {
            throw new \InvalidArgumentException('Invalid setting key.');
        }
        $this->container->get(SettingRepository::class)->set('plugin.' . $this->name . '.' . $key, $value, null);
    }

    /** @param array<string, mixed> $details */
    public function audit(string $action, array $details = []): void
    {
        $this->container->get(AuditLogger::class)->record('plugin.' . $this->name . '.' . $action, 'plugin', $this->name, AuditLogger::SUCCESS, $details);
    }

    /** @internal Undo registrations after a failed boot. */
    public function rollback(): void
    {
        $this->active = false;
        $this->manager->removeHandlers('plugin.' . $this->name . '.');
        $this->router->disable('/plugins/' . $this->name);
    }
}
