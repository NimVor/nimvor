<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\SettingsService;
use NimVor\Services\SslService;
use NimVor\Support\ValidationException;

/**
 * REST API v1: a website's HTTPS. Every endpoint calls SslService, as the web interface does: a
 * certificate is installed, the web server configuration is checked and reloaded, and the website's
 * settings change only when that worked. Private keys are accepted but never returned or logged.
 */
final class SslApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly SslService $ssl,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly SettingsService $settings,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    /**
     * The website's HTTPS state, read fresh.
     *
     * @param array<string, mixed> $s
     * @return array<string, mixed>
     */
    private function status(array $s): array
    {
        $s = (array) ($this->sites->find($this->server->id(), (int) $s['id'], null) ?? $s);
        $cert = $this->ssl->certificate($s);
        $state = null;
        if ($cert !== null) {
            $state = !empty($cert['error']) ? 'error' : (!empty($cert['expired']) ? 'expired' : ((int) ($cert['days_remaining'] ?? 0) <= $this->settings->int('ssl_warning_days') ? 'expiring' : 'valid'));
        }
        $renewal = $s['ssl_mode'] === 'letsencrypt' ? $this->ssl->renewalStatus() : null;
        return [
            'https' => (bool) $s['https'], 'mode' => (string) $s['ssl_mode'], 'force_https' => (bool) $s['force_https'],
            'hsts' => (bool) $s['hsts'], 'security_headers' => (bool) $s['security_headers'], 'state' => $state,
            'certificate' => $cert === null ? null : [
                'common_name' => (string) ($cert['common_name'] ?? ''), 'names' => array_values(array_map('strval', (array) ($cert['dns_names'] ?? []))),
                'issuer' => (string) ($cert['issuer'] ?? ''), 'not_before' => $cert['not_before'] ?? null, 'not_after' => $cert['not_after'] ?? null,
                'days_remaining' => (int) ($cert['days_remaining'] ?? 0), 'self_signed' => (bool) ($cert['self_signed'] ?? false),
                'fingerprint_sha256' => (string) ($cert['fingerprint_sha256'] ?? ''),
            ],
            'auto_renewal' => $renewal === null ? null : !empty($renewal['timer_active']),
        ];
    }

    public function show(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : self::ok($this->status($s));
    }

    public function installCustom(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $this->ssl->installCustom($s, (string) ($in['certificate'] ?? ''), (string) ($in['private_key'] ?? ''), (string) ($in['chain'] ?? ''));
            return self::ok($this->status($s));
        });
    }

    public function renew(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            $this->ssl->renew($s);
            return self::ok($this->status($s));
        });
    }

    public function options(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $flag = static fn (string $k, bool $cur): bool => array_key_exists($k, $in) ? filter_var($in[$k], FILTER_VALIDATE_BOOL, FILTER_NULL_ON_FAILURE) ?? throw new ValidationException([$k => 'Must be true or false.']) : $cur;
            $force = $flag('force_https', (bool) $s['force_https']);
            $hsts = $flag('hsts', (bool) $s['hsts']);
            $headers = $flag('security_headers', (bool) $s['security_headers']);
            if ($force !== (bool) $s['force_https'] || $hsts !== (bool) $s['hsts']) {
                $this->ssl->options($s, $force, $hsts);
            }
            if ($headers !== (bool) $s['security_headers']) {
                $this->ssl->securityHeaders((array) $this->sites->find($this->server->id(), (int) $s['id'], null), $headers);
            }
            return self::ok($this->status($s));
        });
    }

    public function disable(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $this->ssl->disable($s, $r->query('delete_certificate') === '1');
            return self::ok($this->status($s));
        });
    }
}
