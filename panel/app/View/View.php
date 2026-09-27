<?php

declare(strict_types=1);

namespace WhaCP\View;

use RuntimeException;
use WhaCP\Http\Router;
use WhaCP\Security\Csrf;

/**
 * Plain-PHP template renderer.
 *
 * Templates are executed with $this bound to the View, so helpers are
 * available as $this->url(), $this->can(), etc. All output must be escaped
 * with e() unless it is trusted markup produced by the View itself.
 */
final class View
{
    /** @var array<string, mixed> */
    private array $shared = [];

    /** @var array<string, string> */
    private array $sections = [];

    /** @var callable(string): bool */
    private $permissionChecker;

    public function __construct(
        private readonly string $directory,
        private readonly Router $router,
        private readonly Csrf $csrf,
        private readonly string $publicDir,
    ) {
        $this->permissionChecker = static fn (string $p): bool => false;
    }

    public function share(string $key, mixed $value): void
    {
        $this->shared[$key] = $value;
    }

    public function shared(string $key, mixed $default = null): mixed
    {
        return $this->shared[$key] ?? $default;
    }

    /** @param callable(string): bool $checker */
    public function setPermissionChecker(callable $checker): void
    {
        $this->permissionChecker = $checker;
    }

    /** @param array<string, mixed> $data */
    public function render(string $template, array $data = [], ?string $layout = 'layouts/app'): string
    {
        $this->sections = [];
        $content = $this->renderFile($template, $data);
        if ($layout === null) {
            return $content;
        }
        return $this->renderFile($layout, $data + ['content' => $content]);
    }

    /**
     * Render a template from another directory (a plugin's views) inside
     * the panel layout, with the same helpers as core templates.
     *
     * @param array<string, mixed> $data
     */
    public function renderExternal(string $directory, string $template, array $data = []): string
    {
        if (!preg_match('#^[a-z0-9_-]+$#', $template)) {
            throw new RuntimeException('Invalid template name');
        }
        $file = realpath($directory . '/' . $template . '.php');
        $dir = realpath($directory);
        if ($file === false || $dir === false || !str_starts_with($file, $dir . '/')) {
            throw new RuntimeException("Template {$template} not found");
        }
        $this->sections = [];
        $content = (function (string $__file, array $__data): string {
            extract($__data + $this->shared, EXTR_SKIP);
            ob_start();
            try {
                include $__file;
            } catch (\Throwable $e) {
                ob_end_clean();
                throw $e;
            }
            return (string) ob_get_clean();
        })($file, $data);
        return $this->renderFile('layouts/app', $data + ['content' => $content]);
    }

    /** @param array<string, mixed> $data */
    public function partial(string $template, array $data = []): string
    {
        return $this->renderFile($template, $data);
    }

    /** Capture a named section (e.g. page actions) for the layout. */
    public function start(): void
    {
        ob_start();
    }

    public function end(string $name): void
    {
        $this->sections[$name] = (string) ob_get_clean();
    }

    public function section(string $name): string
    {
        return $this->sections[$name] ?? '';
    }

    /** @param array<string, string|int> $params @param array<string, mixed> $query */
    public function url(string $name, array $params = [], array $query = []): string
    {
        return $this->router->url($name, $params, $query);
    }

    public function hasRoute(string $name): bool
    {
        return $this->router->has($name);
    }

    public function csrfToken(): string
    {
        return $this->csrf->token();
    }

    public function csrfField(): string
    {
        return '<input type="hidden" name="_token" value="' . e($this->csrf->token()) . '">';
    }

    public function asset(string $path): string
    {
        $path = '/' . ltrim($path, '/');
        $file = $this->publicDir . $path;
        $v = is_file($file) ? substr(md5((string) filemtime($file)), 0, 8) : '0';
        return $path . '?v=' . $v;
    }

    public function can(string $permission): bool
    {
        return ($this->permissionChecker)($permission);
    }

    /**
     * Hosting customers get the simple layout: no sidebar, their websites
     * as the home page. (Own-scope roles that manage users, i.e.
     * resellers, keep the full layout.)
     */
    public function simpleLayout(): bool
    {
        $user = $this->shared['user'] ?? null;
        return is_array($user) && ($user['role_scope'] ?? 'all') === 'own'
            && !$this->can(\WhaCP\Security\Permissions::USERS_VIEW);
    }

    public function isActive(string $prefix): bool
    {
        $path = (string) ($this->shared['current_path'] ?? '');
        return $prefix === '/' ? $path === '/' : ($path === $prefix || str_starts_with($path, rtrim($prefix, '/') . '/'));
    }

    /** Badge markup for a status keyword. */
    public function statusBadge(string $status, ?string $label = null): string
    {
        $class = match ($status) {
            'active', 'running', 'valid', 'enabled', 'success', 'ok', 'yes', 'succeeded', 'completed' => 'text-bg-success',
            'expiring', 'warning', 'reloading', 'activating', 'denied' => 'text-bg-warning',
            'failed', 'expired', 'error', 'failure', 'inactive', 'dead' => 'text-bg-danger',
            default => 'text-bg-secondary',
        };
        return '<span class="badge ' . $class . '">' . e($label ?? $status) . '</span>';
    }

    /** @param array<string, mixed> $data */
    private function renderFile(string $template, array $data): string
    {
        if (!preg_match('#^[a-z0-9_/-]+$#', $template)) {
            throw new RuntimeException('Invalid template name');
        }
        $file = $this->directory . '/' . $template . '.php';
        if (!is_file($file)) {
            throw new RuntimeException("Template {$template} not found");
        }
        $vars = $data + $this->shared;
        $render = function (string $__file, array $__vars): string {
            extract($__vars, EXTR_SKIP);
            ob_start();
            try {
                include $__file;
            } catch (\Throwable $e) {
                ob_end_clean();
                throw $e;
            }
            return (string) ob_get_clean();
        };
        return $render->call($this, $file, $vars);
    }
}
