<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\RuntimeService;
use NimVor\Services\ServerContext;
use NimVor\Services\SiteEnvService;
use NimVor\Services\WebsiteService;
use NimVor\Services\WorkerService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * REST API v1: a website's own settings (aliases, document-root folder, upload limit, proxy address, directory listing,
 * uptime checks), re-applying its configuration, the file-permission check and repair, its environment variables and
 * the runtime of a Node.js or Python website. Every endpoint calls the service the web interface calls, so the same
 * validation, the same check-before-reload and the same rollback apply. Values of environment variables are never
 * returned: they are write-only here, as secrets should be.
 */
final class SiteSettingsApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly WebsiteService $websites,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly SiteEnvService $env,
        private readonly WorkerService $workers,
        private readonly RuntimeService $runtimes,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    /**
     * @param array<string, mixed> $s
     * @return array<string, mixed>
     */
    private function settingsOut(array $s): array
    {
        $sub = '';
        $root = rtrim((string) $s['root'], '/') . '/';
        if (str_starts_with((string) $s['document_root'], $root)) {
            $sub = substr((string) $s['document_root'], strlen($root));
        }
        return [
            'domain' => (string) $s['domain'], 'kind' => (string) $s['kind'],
            'aliases' => array_values((array) $s['aliases']),
            'document_root' => (string) $s['document_root'], 'document_root_sub' => $sub,
            'client_max_body_mb' => (int) ($s['client_max_body_mb'] ?? 0),
            'proxy_pass' => (string) ($s['proxy_pass'] ?? ''),
            'directory_listing' => !empty($s['directory_listing']),
            'monitoring' => empty($s['monitor_disabled']),
        ];
    }

    private function fresh(Request $r, string $id): array
    {
        return $this->site($r, $id) ?? [];
    }

    public function show(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : self::ok($this->settingsOut($s));
    }

    public function update(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $id, $s): Response {
            $in = self::body($r);
            $errors = [];
            $known = ['aliases', 'document_root_sub', 'client_max_body_mb', 'proxy_pass', 'directory_listing', 'monitoring'];
            foreach (array_diff(array_keys($in), $known) as $k) {
                $errors[(string) $k] = 'Not a website setting.';
            }
            foreach (['directory_listing', 'monitoring'] as $flag) {
                if (isset($in[$flag]) && !is_bool($in[$flag])) {
                    $errors[$flag] = 'Send true or false.';
                }
            }
            if (isset($in['aliases']) && (!is_array($in['aliases']) || array_filter($in['aliases'], 'is_string') !== $in['aliases'])) {
                $errors['aliases'] = 'Send a list of domain names.';
            }
            if ($errors !== []) {
                throw new ValidationException($errors);
            }
            // The website form always posts the whole set, so anything not sent is filled in from the stored values.
            $cur = $this->settingsOut($s);
            $form = [
                'aliases' => implode("\n", array_key_exists('aliases', $in) ? (array) $in['aliases'] : $cur['aliases']),
                'client_max_body_mb' => $in['client_max_body_mb'] ?? $cur['client_max_body_mb'],
            ];
            if (isset($in['document_root_sub'])) {
                $form['document_root_sub'] = (string) $in['document_root_sub'];
            }
            if (isset($in['proxy_pass'])) {
                $form['proxy_pass'] = (string) $in['proxy_pass'];
            }
            $changed = array_intersect_key($in, array_flip(['aliases', 'document_root_sub', 'client_max_body_mb', 'proxy_pass'])) !== [];
            if ($changed) {
                $this->websites->update($s, $form, $this->user($r));
                $s = $this->fresh($r, $id);
            }
            if (isset($in['directory_listing']) && $in['directory_listing'] !== !empty($s['directory_listing'])) {
                $this->websites->setDirectoryListing($s, $in['directory_listing']);
                $s = $this->fresh($r, $id);
            }
            if (isset($in['monitoring']) && $in['monitoring'] !== empty($s['monitor_disabled'])) {
                $this->websites->setMonitoring($s, $in['monitoring']);
                $s = $this->fresh($r, $id);
            }
            return self::ok($this->settingsOut($s));
        });
    }

    public function reapply(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            $this->websites->reapply($s);
            return self::ok(['reapplied' => (string) $s['domain']]);
        });
    }

    public function permissions(Request $r, string $id): Response
    {
        return $this->permissionRun($r, $id, false);
    }

    public function repairPermissions(Request $r, string $id): Response
    {
        return $this->permissionRun($r, $id, true);
    }

    private function permissionRun(Request $r, string $id, bool $repair): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s, $repair): Response {
            $res = $this->websites->permissions($s, $repair);
            return self::ok([
                'repaired' => $repair,
                'checked' => (int) ($res['checked'] ?? 0),
                'wrong' => (int) ($res['wrong'] ?? $res['fixed'] ?? 0),
                'fixed' => (int) ($res['fixed'] ?? 0),
                'skipped' => (int) ($res['skipped_count'] ?? 0),
            ]);
        });
    }

    /** @return array<string, mixed> */
    private function envOut(array $s): array
    {
        $names = array_keys($this->env->get($s));
        sort($names);
        return ['names' => array_map('strval', $names), 'count' => count($names), 'max' => SiteEnvService::MAX_VARS];
    }

    public function envShow(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : self::ok($this->envOut($s));
    }

    public function envUpdate(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $set = $in['set'] ?? [];
            $unset = $in['unset'] ?? [];
            $errors = [];
            if (!is_array($set) || ($set !== [] && array_is_list($set))) {
                $errors['set'] = 'Send an object of NAME: value.';
            } elseif (array_filter($set, static fn ($v): bool => !is_string($v) && !is_int($v) && !is_float($v)) !== []) {
                $errors['set'] = 'Every value must be a string.';
            }
            if (!is_array($unset) || array_filter($unset, 'is_string') !== $unset) {
                $errors['unset'] = 'Send a list of names to remove.';
            }
            if (!isset($in['set']) && !isset($in['unset'])) {
                $errors['set'] = 'Send `set`, `unset` or both.';
            }
            if ($errors !== []) {
                throw new ValidationException($errors);
            }
            $note = $this->env->change($s, array_map('strval', $set), array_values($unset), $this->user($r));
            return self::ok($this->envOut($s) + ['note' => $note]);
        });
    }

    /** @param array<string, mixed> $s */
    private function runtimeOut(array $s): array
    {
        $type = WebsiteService::appType($s);
        $prefix = $type === 'python' ? 'python' : 'node';
        $versions = array_values(array_filter($this->runtimes->selectable(), static fn (string $x): bool => str_starts_with($x, $prefix . ':')));
        return ['type' => $type, 'app_runtime' => (string) ($s['app_runtime'] ?? ''), 'available' => $versions, 'proxy_pass' => (string) ($s['proxy_pass'] ?? '')];
    }

    public function runtimeShow(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            $this->assertApp($s);
            return self::ok($this->runtimeOut($s));
        });
    }

    public function runtimeUpdate(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $id, $s): Response {
            $this->assertApp($s);
            $runtime = (string) (self::body($r)['app_runtime'] ?? '');
            $this->websites->setRuntime($s, $runtime);
            foreach ($this->workers->forSite($s) as $w) {
                if (($w['name'] ?? '') === 'app') {
                    try {
                        $this->workers->changeRuntime($s, $w, $runtime);
                    } catch (AgentException) {
                        // As in the web interface: the website keeps the new runtime; the worker follows at its next start.
                    }
                }
            }
            return self::ok($this->runtimeOut($this->fresh($r, $id)));
        });
    }

    /** @param array<string, mixed> $s */
    private function assertApp(array $s): void
    {
        if (WebsiteService::appType($s) === '') {
            throw new UserFacingException('Only Node.js and Python websites have a runtime.');
        }
    }
}
