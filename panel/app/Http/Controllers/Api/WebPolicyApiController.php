<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\WebPolicyService;
use NimVor\Support\ValidationException;

/**
 * REST API v1: a website's browser security headers (framing, Content-Security-Policy, Referrer-Policy,
 * Permissions-Policy, HSTS) and cache rules (static and dynamic cache headers, the Nginx page cache, Brotli).
 * Every endpoint calls WebPolicyService, as the web interface does: the values are validated, the website's web server
 * configuration is checked before it is reloaded, and the previous settings are restored if the server refuses them.
 * A change names only the fields to change; the others keep their current values.
 */
final class WebPolicyApiController
{
    use ApiHelpers;

    private const HEADER_FIELDS = ['clickjacking', 'frame_ancestors', 'csp', 'csp_report_only', 'referrer', 'permissions', 'permissions_custom', 'hsts'];
    private const HSTS_FIELDS = ['enabled', 'max_age', 'include_subdomains', 'preload'];
    private const CACHE_FIELDS = ['static_ttl', 'dynamic_mode', 'dynamic_ttl', 'bypass_paths', 'page_cache', 'brotli'];
    private const PAGE_CACHE_FIELDS = ['enabled', 'ttl'];

    public function __construct(
        private readonly WebPolicyService $policy,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    /**
     * @param array<string, mixed> $site
     * @return array<string, mixed>
     */
    private function stateOut(array $site): array
    {
        return $this->policy->state($site);
    }

    /** The values each choice accepts, for a client that builds a form. @return array<string, mixed> */
    private static function options(): array
    {
        return [
            'clickjacking' => array_keys(WebPolicyService::CLICKJACKING),
            'referrer' => array_keys(WebPolicyService::REFERRER),
            'permissions' => array_keys(WebPolicyService::PERMISSIONS),
            'hsts_max_age' => array_keys(WebPolicyService::HSTS_AGES),
            'static_ttl' => ['min' => WebPolicyService::MIN_STATIC_TTL, 'max' => WebPolicyService::MAX_STATIC_TTL, 'suggested' => array_keys(WebPolicyService::STATIC_TTLS)],
            'dynamic_mode' => array_keys(WebPolicyService::DYNAMIC_MODES),
            'dynamic_ttl' => ['min' => WebPolicyService::MIN_DYNAMIC_TTL, 'max' => WebPolicyService::MAX_DYNAMIC_TTL],
            'page_cache_ttl' => array_keys(WebPolicyService::PAGE_CACHE_TTLS),
            'bypass_paths_max' => WebPolicyService::MAX_BYPASS,
            'csp_max_length' => WebPolicyService::MAX_CSP,
        ];
    }

    /**
     * Refuses a body that names a field this endpoint does not have, or gives a value of the wrong kind.
     *
     * @param array<string, mixed> $in
     * @param list<string> $known
     * @param array<string, string> $kinds field => string|int|bool|list|object
     */
    private static function check(array $in, array $known, array $kinds, string $prefix = ''): void
    {
        $errors = [];
        foreach ($in as $k => $v) {
            $name = $prefix . $k;
            if (!in_array($k, $known, true)) {
                $errors[$name] = 'Not a setting of this endpoint.';
                continue;
            }
            $ok = match ($kinds[$k] ?? 'string') {
                'string' => is_string($v),
                'int' => is_int($v),
                'bool' => is_bool($v),
                'list' => is_array($v) && array_is_list($v) && array_filter($v, 'is_string') === $v,
                'object' => is_array($v) && !array_is_list($v),
                default => false,
            };
            if (!$ok) {
                $errors[$name] = match ($kinds[$k] ?? 'string') {
                    'int' => 'Send a whole number.',
                    'bool' => 'Send true or false.',
                    'list' => 'Send a list of strings.',
                    'object' => 'Send an object.',
                    default => 'Send a string.',
                };
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
    }

    private static function flag(bool $v): string
    {
        return $v ? '1' : '0';
    }

    public function show(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : self::ok($this->stateOut($s) + ['options' => self::options()]);
    }

    public function updateHeaders(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $id, $s): Response {
            $in = self::body($r);
            self::check($in, self::HEADER_FIELDS, ['frame_ancestors' => 'list', 'csp_report_only' => 'bool', 'hsts' => 'object']);
            $hin = (array) ($in['hsts'] ?? []);
            self::check($hin, self::HSTS_FIELDS, ['enabled' => 'bool', 'max_age' => 'int', 'include_subdomains' => 'bool', 'preload' => 'bool'], 'hsts.');
            // What the form posts, filled in from the current settings for everything that was not sent.
            $cur = $this->policy->state($s)['headers'];
            $h = array_replace($cur, array_intersect_key($in, array_flip(['clickjacking', 'frame_ancestors', 'csp', 'csp_report_only', 'referrer', 'permissions', 'permissions_custom'])));
            $hsts = array_replace((array) $cur['hsts'], $hin);
            $form = [
                'clickjacking' => $h['clickjacking'], 'frame_ancestors' => implode("\n", (array) $h['frame_ancestors']),
                'csp' => $h['csp'], 'csp_report_only' => self::flag((bool) $h['csp_report_only']),
                'referrer' => $h['referrer'], 'permissions' => $h['permissions'], 'permissions_custom' => $h['permissions_custom'],
                'hsts_enabled' => self::flag((bool) $hsts['enabled']), 'hsts_max_age' => $hsts['max_age'],
                'hsts_include_subdomains' => self::flag((bool) $hsts['include_subdomains']), 'hsts_preload' => self::flag((bool) $hsts['preload']),
            ];
            $this->policy->saveHeaders($s, $form);
            return self::ok($this->stateOut($this->site($r, $id) ?? $s));
        });
    }

    public function updateCache(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $id, $s): Response {
            $in = self::body($r);
            self::check($in, self::CACHE_FIELDS, ['static_ttl' => 'int', 'dynamic_ttl' => 'int', 'bypass_paths' => 'list', 'page_cache' => 'object', 'brotli' => 'bool']);
            $pin = (array) ($in['page_cache'] ?? []);
            self::check($pin, self::PAGE_CACHE_FIELDS, ['enabled' => 'bool', 'ttl' => 'int'], 'page_cache.');
            $cur = $this->policy->state($s)['cache'];
            $c = array_replace($cur, array_intersect_key($in, array_flip(['static_ttl', 'dynamic_mode', 'dynamic_ttl', 'bypass_paths', 'brotli'])));
            $page = array_replace((array) $cur['page_cache'], $pin);
            $form = [
                'static_ttl' => $c['static_ttl'], 'dynamic_mode' => $c['dynamic_mode'], 'dynamic_ttl' => $c['dynamic_ttl'],
                'bypass_paths' => implode("\n", (array) $c['bypass_paths']),
                'page_cache' => self::flag((bool) $page['enabled']), 'page_cache_ttl' => $page['ttl'], 'brotli' => self::flag((bool) $c['brotli']),
            ];
            $this->policy->saveCache($s, $form);
            return self::ok($this->stateOut($this->site($r, $id) ?? $s));
        });
    }

    public function purge(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $this->policy->purgePageCache($s, (int) ($this->user($r)['id'] ?? 0));
            return self::ok(['purged' => (string) $s['domain']]);
        });
    }

    public function reset(Request $r, string $id): Response
    {
        return $this->resetSection($r, $id, 'all');
    }

    public function resetHeaders(Request $r, string $id): Response
    {
        return $this->resetSection($r, $id, 'headers');
    }

    public function resetCache(Request $r, string $id): Response
    {
        return $this->resetSection($r, $id, 'cache');
    }

    private function resetSection(Request $r, string $id, string $section): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $id, $s, $section): Response {
            $this->policy->reset($s, $section);
            return self::ok($this->stateOut($this->site($r, $id) ?? $s));
        });
    }
}
