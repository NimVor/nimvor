<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\WebsiteService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * REST API v1: a PHP website's PHP settings. Every endpoint calls WebsiteService, as the web interface
 * does: the values are validated, the website's PHP-FPM pool is written and checked with `php-fpm -t`,
 * and the change is undone when the server refuses it. Process limits and hardening are the
 * administrator's; a hosting customer who sends them is told so instead of being silently ignored.
 */
final class PhpApiController
{
    use ApiHelpers;

    /** Settings and their defaults, as the web interface shows them. */
    private const DEFAULTS = [
        'memory_limit_mb' => 256, 'upload_max_mb' => 64, 'max_execution_seconds' => 60,
        'max_children' => 5, 'pm' => 'ondemand', 'start_servers' => 0, 'min_spare_servers' => 0, 'max_spare_servers' => 0,
        'idle_timeout_seconds' => 10, 'max_requests' => 500, 'request_timeout_seconds' => 0, 'slowlog_seconds' => 10,
    ];

    public function __construct(
        private readonly WebsiteService $websites,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    /** @param array<string, mixed> $s */
    private function assertPhp(array $s): void
    {
        if ($s['kind'] !== 'php') {
            throw new UserFacingException('PHP settings only apply to PHP websites.');
        }
    }

    /**
     * @param array<string, mixed> $s
     * @return array<string, mixed>
     */
    private function settingsOut(array $s): array
    {
        $p = (array) $s['php_settings'];
        $out = ['php_version' => (string) $s['php_version'], 'installed_versions' => $this->websites->phpVersions()];
        foreach (self::DEFAULTS as $k => $default) {
            $out[$k] = $p[$k] ?? $default;
        }
        return $out + [
            'harden' => (bool) ($p['harden'] ?? false), 'lock_ini' => (bool) ($p['lock_ini'] ?? false),
            'php_profile' => (string) ($p['profile'] ?? ''), 'disable_functions' => (string) ($p['disable_functions'] ?? ''),
            'ini' => (object) ($p['ini'] ?? []),
        ];
    }

    public function show(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            $this->assertPhp($s);
            return self::ok($this->settingsOut($s) + ['effective' => $this->websites->phpConfig($s)]);
        });
    }

    public function update(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $this->assertPhp($s);
            $in = array_intersect_key(self::body($r), array_flip(array_merge(['php_version', 'memory_limit_mb', 'upload_max_mb', 'max_execution_seconds', 'php_profile', 'disable_functions', 'ini'], WebsiteService::SERVER_PHP_SETTINGS)));
            $user = $this->user($r);
            $server = array_intersect(array_keys($in), WebsiteService::SERVER_PHP_SETTINGS);
            if ($server !== [] && !Scope::isAll($user)) {
                throw new ValidationException(array_fill_keys($server, 'Only an administrator can change this.'));
            }
            if (isset($in['ini']) && !is_array($in['ini'])) {
                throw new ValidationException(['ini' => 'Send an object of php.ini directives, for example {"display_errors": "Off"}.']);
            }
            // Only what is sent changes; the aliases are sent back as they are.
            $this->websites->update($s, $in + ['aliases' => implode("\n", $s['aliases'])], $user);
            return self::ok($this->settingsOut((array) $this->sites->find($this->server->id(), (int) $s['id'], null)));
        });
    }

    public function reload(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            $this->assertPhp($s);
            $this->websites->restartPhp($s);
            return self::ok(['reloaded' => 'PHP ' . $s['php_version']]);
        });
    }

    public function check(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            $this->assertPhp($s);
            return self::ok((array) $this->websites->checkPhpConfig($s));
        });
    }

    public function reset(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $this->assertPhp($s);
            $this->websites->resetPhpOverrides($s, $this->user($r));
            return self::ok($this->settingsOut((array) $this->sites->find($this->server->id(), (int) $s['id'], null)));
        });
    }
}
