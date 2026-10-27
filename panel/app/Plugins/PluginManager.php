<?php

declare(strict_types=1);

namespace WhaCP\Plugins;

use Throwable;
use WhaCP\Core\Container;
use WhaCP\Database\Connection;
use WhaCP\Http\Router;
use WhaCP\Repositories\SettingRepository;
use WhaCP\Support\Logger;

/**
 * Discovers, enables and boots plugins.
 *
 * Trust model: a plugin is PHP code running inside the panel. Plugins are
 * installed on disk by an administrator (never uploaded through the web
 * interface) and only run once enabled under Administration → Plugins.
 * Files that are group- or world-writable are refused, so a plugin cannot
 * be modified by anyone but its owner.
 *
 * Layout: <plugins dir>/<name>/plugin.json, plugin.php, views/*.php
 */
final class PluginManager
{
    public const NAME = '/^[a-z][a-z0-9-]{1,39}$/';
    private const ENABLED_KEY = '_plugins_enabled';

    /** @var array<string, \Closure> route name => handler */
    private array $handlers = [];
    /** @var list<array{label: string, icon: string, route: string, permission: string}> */
    private array $nav = [];
    /** @var array<string, string> name => boot error */
    private array $errors = [];
    private bool $booted = false;

    public function __construct(
        private readonly string $dir,
        private readonly SettingRepository $settings,
        private readonly Connection $db,
        private readonly Logger $log,
        private readonly Container $container,
    ) {
    }

    public function directory(): string
    {
        return $this->dir;
    }

    /**
     * All plugins on disk with their manifest and problems.
     *
     * @return array<string, array{name: string, manifest: array<string, mixed>, path: string, problems: list<string>, enabled: bool, error: ?string}>
     */
    public function discover(): array
    {
        $out = [];
        $enabled = $this->enabledNames();
        foreach (glob($this->dir . '/*/plugin.json') ?: [] as $file) {
            $path = dirname($file);
            $name = basename($path);
            [$manifest, $problems] = $this->inspect($name, $path);
            $out[$name] = ['name' => $name, 'manifest' => $manifest, 'path' => $path, 'problems' => $problems, 'enabled' => in_array($name, $enabled, true), 'error' => $this->errors[$name] ?? null];
        }
        ksort($out);
        return $out;
    }

    /**
     * Validate a plugin directory.
     *
     * @return array{0: array<string, mixed>, 1: list<string>}
     */
    public function inspect(string $name, string $path): array
    {
        $problems = [];
        if (!preg_match(self::NAME, $name)) {
            return [[], ['The folder name must be 2–40 lowercase letters, digits or dashes.']];
        }
        $manifest = json_decode((string) @file_get_contents($path . '/plugin.json'), true);
        if (!is_array($manifest)) {
            return [[], ['plugin.json is not valid JSON.']];
        }
        foreach (['name', 'title', 'version'] as $k) {
            if (!is_string($manifest[$k] ?? null) || $manifest[$k] === '') {
                $problems[] = "plugin.json must set \"{$k}\".";
            }
        }
        if (($manifest['name'] ?? '') !== $name) {
            $problems[] = 'plugin.json "name" must match the folder name.';
        }
        foreach ((array) ($manifest['permissions'] ?? []) as $perm => $label) {
            if (!is_string($perm) || !str_starts_with($perm, 'plugin.' . $name . '.') || !preg_match('/^[a-z0-9.-]{1,80}$/', $perm)) {
                $problems[] = "Permission \"{$perm}\" must be named plugin.{$name}.<something>.";
            }
        }
        if (!is_file($path . '/plugin.php')) {
            $problems[] = 'plugin.php is missing.';
        }
        $iter = new \RecursiveIteratorIterator(new \RecursiveDirectoryIterator($path, \FilesystemIterator::SKIP_DOTS), \RecursiveIteratorIterator::SELF_FIRST);
        foreach ([$path => new \SplFileInfo($path)] + iterator_to_array($iter) as $f) {
            /** @var \SplFileInfo $f */
            if ($f->isLink()) {
                $problems[] = 'Symbolic links are not allowed: ' . substr($f->getPathname(), strlen($path) + 1);
            } elseif (($f->getPerms() & 0o022) !== 0) {
                $problems[] = 'Writable by group or others: ' . ($f->getPathname() === $path ? '(plugin folder)' : substr($f->getPathname(), strlen($path) + 1));
            }
        }
        return [$manifest, array_values(array_unique($problems))];
    }

    /** @return list<string> */
    public function enabledNames(): array
    {
        $v = json_decode((string) ($this->settings->all()[self::ENABLED_KEY] ?? '[]'), true);
        return is_array($v) ? array_values(array_filter($v, 'is_string')) : [];
    }

    public function setEnabled(string $name, bool $enabled, ?int $userId): void
    {
        $plugins = $this->discover();
        $p = $plugins[$name] ?? throw new \InvalidArgumentException('Unknown plugin.');
        if ($enabled && $p['problems'] !== []) {
            throw new \InvalidArgumentException('The plugin cannot be enabled: ' . implode(' ', $p['problems']));
        }
        $names = array_values(array_diff($this->enabledNames(), [$name]));
        if ($enabled) {
            $names[] = $name;
            $this->syncPermissions($name, (array) ($p['manifest']['permissions'] ?? []));
        } else {
            $this->db->run("DELETE FROM permissions WHERE name LIKE ?", ['plugin.' . $name . '.%']);
        }
        sort($names);
        $this->settings->set(self::ENABLED_KEY, (string) json_encode($names), $userId);
    }

    /**
     * Register the plugin's permissions and grant them to administrators.
     *
     * @param array<string, string> $perms
     */
    private function syncPermissions(string $name, array $perms): void
    {
        foreach ($perms as $perm => $label) {
            $this->db->run(
                'INSERT INTO permissions (name, label, grp) VALUES (?, ?, ?) ON CONFLICT(name) DO UPDATE SET label = excluded.label',
                [$perm, mb_substr((string) $label, 0, 120), 'Plugin: ' . $name]
            );
            $this->db->run(
                "INSERT OR IGNORE INTO role_permissions (role_id, permission_id) SELECT r.id, p.id FROM roles r, permissions p WHERE r.name = 'admin' AND p.name = ?",
                [$perm]
            );
        }
    }

    /** Load enabled plugins; called once when the router is built. */
    public function boot(Router $router): void
    {
        if ($this->booted) {
            return;
        }
        $this->booted = true;
        $plugins = $this->discover();
        foreach ($this->enabledNames() as $name) {
            $p = $plugins[$name] ?? null;
            if ($p === null || $p['problems'] !== []) {
                $this->errors[$name] = $p === null ? 'The plugin folder is missing.' : implode(' ', $p['problems']);
                $this->log->warning('plugin not loaded', ['plugin' => $name, 'error' => $this->errors[$name]]);
                continue;
            }
            $ctx = new PluginContext($name, $p['path'], (array) ($p['manifest']['permissions'] ?? []), $router, $this, $this->container);
            try {
                $init = (static fn (string $file): mixed => require $file)($p['path'] . '/plugin.php');
                if (!$init instanceof \Closure) {
                    throw new \RuntimeException('plugin.php must return a function(PluginContext $ctx).');
                }
                $init($ctx);
            } catch (Throwable $e) {
                $this->errors[$name] = $e->getMessage();
                $this->log->error('plugin failed to boot', ['plugin' => $name, 'error' => $e->getMessage(), 'file' => $e->getFile() . ':' . $e->getLine()]);
                $ctx->rollback();
            }
        }
    }

    /** @internal used by PluginContext */
    public function addHandler(string $routeName, \Closure $handler): void
    {
        $this->handlers[$routeName] = $handler;
    }

    /** @internal */
    public function removeHandlers(string $prefix): void
    {
        foreach (array_keys($this->handlers) as $k) {
            if (str_starts_with($k, $prefix)) {
                unset($this->handlers[$k]);
            }
        }
        $this->nav = array_values(array_filter($this->nav, static fn (array $n): bool => !str_starts_with($n['route'], $prefix)));
    }

    public function handler(string $routeName): ?\Closure
    {
        return $this->handlers[$routeName] ?? null;
    }

    /** @internal */
    public function addNav(array $item): void
    {
        $this->nav[] = $item;
    }

    /** @return list<array{label: string, icon: string, route: string, permission: string}> */
    public function nav(): array
    {
        return $this->nav;
    }
}
