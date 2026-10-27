<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * A website's security headers and cache rules, chosen by its owner.
 *
 * Everything is a choice from a fixed list or a value checked against a strict
 * grammar; customers never write Nginx directives. The panel stores the
 * choices (websites.web_policy), sends them with the website's spec and the
 * agent renders, validates (nginx -t) and applies them, keeping the previous
 * configuration when anything fails. The agent checks the same grammar again:
 * the checks here exist for friendly messages, not for safety.
 *
 * The server-level rules (X-Content-Type-Options, dotfile and PHP
 * restrictions, request limits, ModSecurity, password protection) are not
 * part of this page and cannot be changed from it.
 */
final class WebPolicyService
{
    public const CLICKJACKING = [
        'off' => ['Off', 'Do not send a framing policy. Other websites may embed this one.'],
        'sameorigin' => ['Same origin (recommended)', 'Only this website may embed its own pages (X-Frame-Options: SAMEORIGIN and frame-ancestors \'self\').'],
        'deny' => ['Never allow framing', 'No website may embed these pages (X-Frame-Options: DENY and frame-ancestors \'none\').'],
        'allow' => ['This website and chosen sites', 'This website and the https:// sites you list may embed its pages (frame-ancestors).'],
    ];

    public const REFERRER = [
        'off' => ['Off', 'Send no Referrer-Policy; browsers use their own default.'],
        'strict-origin-when-cross-origin' => ['Strict origin when cross-origin (recommended)', 'Full address inside this website; only the site name to other sites, and nothing on downgrades to HTTP.'],
        'same-origin' => ['Same origin', 'Send the address only inside this website.'],
        'no-referrer' => ['No referrer', 'Never send the previous address.'],
        'strict-origin' => ['Strict origin', 'Only the site name, never a path, and nothing on downgrades.'],
        'origin-when-cross-origin' => ['Origin when cross-origin', 'Full address inside this website; only the site name to other sites.'],
        'no-referrer-when-downgrade' => ['No referrer when downgrade', 'Full address unless going from HTTPS to HTTP.'],
    ];

    public const PERMISSIONS = [
        'off' => ['Off', 'Send no Permissions-Policy.'],
        'restrictive' => ['Restrictive (recommended)', 'Turn off camera, microphone, location, payment and USB for this website and the pages it embeds.'],
        'strict' => ['Strict', 'Also turn off sensors, Bluetooth, MIDI, serial, HID, screen capture and ad-tracking APIs.'],
        'custom' => ['Custom', 'Write your own list, for example camera=(), geolocation=(self).'],
    ];

    /** Static asset browser-cache lifetimes (seconds). */
    public const STATIC_TTLS = [
        0 => 'Leave it to the application',
        3600 => '1 hour',
        86400 => '1 day',
        604800 => '1 week',
        2592000 => '30 days',
        31536000 => '1 year',
    ];

    public const DYNAMIC_MODES = [
        'app' => ['Leave it to the application (default)', 'NimVor adds nothing to pages the application generates.'],
        'revalidate' => ['Always revalidate', 'Browsers may keep a copy but must check with the website before using it (Cache-Control: no-cache).'],
        'no-store' => ['Never store', 'Browsers and proxies must not keep pages (Cache-Control: no-store).'],
        'public' => ['Short public cache', 'Pages that look the same for every visitor may be kept for a short time. Only for pages without personal content.'],
    ];

    public const HSTS_AGES = [
        300 => '5 minutes (testing)',
        86400 => '1 day',
        2592000 => '30 days',
        15552000 => '6 months',
        31536000 => '1 year (recommended)',
        63072000 => '2 years',
    ];

    public const MIN_STATIC_TTL = 60;
    public const MAX_STATIC_TTL = 31536000;
    public const MIN_DYNAMIC_TTL = 30;
    public const MAX_DYNAMIC_TTL = 3600;
    public const MAX_BYPASS = 20;
    public const MAX_CSP = 4000;

    private const CSP_DIRECTIVES = [
        'default-src', 'script-src', 'script-src-elem', 'script-src-attr', 'style-src', 'style-src-elem', 'style-src-attr', 'img-src', 'font-src',
        'connect-src', 'media-src', 'object-src', 'frame-src', 'child-src', 'worker-src', 'manifest-src', 'base-uri', 'form-action', 'prefetch-src',
    ];

    private const PERMISSION_FEATURES = [
        'accelerometer', 'ambient-light-sensor', 'autoplay', 'battery', 'bluetooth', 'browsing-topics', 'camera', 'clipboard-read', 'clipboard-write',
        'display-capture', 'document-domain', 'encrypted-media', 'fullscreen', 'gamepad', 'geolocation', 'gyroscope', 'hid', 'idle-detection',
        'interest-cohort', 'magnetometer', 'microphone', 'midi', 'payment', 'picture-in-picture', 'publickey-credentials-get', 'screen-wake-lock',
        'serial', 'usb', 'web-share', 'xr-spatial-tracking',
    ];

    public function __construct(
        private readonly WebsiteService $websites,
    ) {
    }

    /** What a form starts from for a website that has not saved a policy: the recommended settings. */
    public static function defaultHeaders(): array
    {
        return [
            'clickjacking' => 'sameorigin', 'frame_ancestors' => [], 'csp' => '', 'csp_report_only' => false,
            'referrer' => 'strict-origin-when-cross-origin', 'permissions' => 'restrictive', 'permissions_custom' => '',
            'hsts' => ['enabled' => false, 'max_age' => 31536000, 'include_subdomains' => false, 'preload' => false],
        ];
    }

    public static function defaultCache(): array
    {
        return ['static_ttl' => 0, 'dynamic_mode' => 'app', 'dynamic_ttl' => 60, 'bypass_paths' => []];
    }

    /**
     * HSTS may only be sent by a website whose HTTPS works: it has a
     * certificate and plain HTTP is redirected to it.
     *
     * @param array<string, mixed> $site
     */
    public static function hstsAvailable(array $site): bool
    {
        return !empty($site['https']) && !empty($site['force_https']) && ($site['ssl_mode'] ?? 'none') !== 'none' && (string) ($site['cert_file'] ?? '') !== '';
    }

    /**
     * What the page shows: the saved choices, or the recommended settings.
     *
     * @param array<string, mixed> $site
     * @return array{headers: array<string, mixed>, cache: array<string, mixed>, headers_custom: bool, cache_custom: bool, hsts_available: bool}
     */
    public function state(array $site): array
    {
        $p = (array) ($site['web_policy'] ?? []);
        $h = isset($p['headers']) && is_array($p['headers']) ? array_replace_recursive(self::defaultHeaders(), $p['headers']) : self::defaultHeaders();
        $c = isset($p['cache']) && is_array($p['cache']) ? array_replace(self::defaultCache(), $p['cache']) : self::defaultCache();
        return [
            'headers' => $h, 'cache' => $c,
            'headers_custom' => isset($p['headers']), 'cache_custom' => isset($p['cache']),
            'hsts_available' => self::hstsAvailable($site),
        ];
    }

    /**
     * The parts of the website's agent spec that come from the policy. HSTS
     * is dropped here unless the website's HTTPS is ready, so a stale choice
     * can never put a website behind HTTPS it does not have.
     *
     * @param array<string, mixed> $site
     * @return array<string, mixed>
     */
    public static function specParts(array $site): array
    {
        $p = (array) ($site['web_policy'] ?? []);
        $out = [];
        if (isset($p['headers']) && is_array($p['headers'])) {
            $h = array_replace_recursive(self::defaultHeaders(), $p['headers']);
            $hsts = (array) $h['hsts'];
            if (!self::hstsAvailable($site)) {
                $hsts = ['enabled' => false];
            }
            $out['headers'] = [
                'clickjacking' => (string) $h['clickjacking'],
                'frame_ancestors' => array_values((array) $h['frame_ancestors']),
                'csp' => (string) $h['csp'],
                'csp_report_only' => (bool) $h['csp_report_only'],
                'referrer' => (string) $h['referrer'],
                'permissions' => (string) $h['permissions'],
                'permissions_custom' => (string) $h['permissions_custom'],
                'hsts' => [
                    'enabled' => (bool) ($hsts['enabled'] ?? false), 'max_age' => (int) ($hsts['max_age'] ?? 0),
                    'include_subdomains' => (bool) ($hsts['include_subdomains'] ?? false), 'preload' => (bool) ($hsts['preload'] ?? false),
                ],
            ];
        }
        if (isset($p['cache']) && is_array($p['cache'])) {
            $c = array_replace(self::defaultCache(), $p['cache']);
            $out['cache'] = [
                'static_ttl' => (int) $c['static_ttl'], 'dynamic_mode' => (string) $c['dynamic_mode'],
                'dynamic_ttl' => (int) $c['dynamic_ttl'], 'bypass_paths' => array_values((array) $c['bypass_paths']),
            ];
        }
        return $out;
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $input
     * @return string what was done
     */
    public function saveHeaders(array $site, array $input): string
    {
        $headers = $this->validateHeaders($input, $site);
        $policy = (array) ($site['web_policy'] ?? []);
        $policy['headers'] = $headers;
        $this->websites->patch($site, ['web_policy' => $policy], 'website.web_policy');
        return 'Security headers saved and applied.';
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $input
     */
    public function saveCache(array $site, array $input): string
    {
        $cache = $this->validateCache($input);
        $policy = (array) ($site['web_policy'] ?? []);
        $policy['cache'] = $cache;
        $this->websites->patch($site, ['web_policy' => $policy], 'website.web_policy');
        return 'Cache rules saved and applied.';
    }

    /**
     * Back to the defaults: forget the saved choices of one section (or both).
     *
     * @param array<string, mixed> $site
     */
    public function reset(array $site, string $section): string
    {
        if (!in_array($section, ['headers', 'cache', 'all'], true)) {
            throw new UserFacingException('Choose what to restore.');
        }
        $policy = (array) ($site['web_policy'] ?? []);
        if ($section === 'all') {
            $policy = [];
        } else {
            unset($policy[$section]);
        }
        $this->websites->patch($site, ['web_policy' => $policy], 'website.web_policy_reset');
        return match ($section) {
            'headers' => 'Security headers are back to the defaults.',
            'cache' => 'Cache rules are back to the defaults.',
            default => 'Security headers and cache rules are back to the defaults.',
        };
    }

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed> $site
     * @return array<string, mixed>
     */
    public function validateHeaders(array $input, array $site): array
    {
        $errors = [];
        $click = (string) ($input['clickjacking'] ?? '');
        if (!isset(self::CLICKJACKING[$click])) {
            $errors['clickjacking'] = 'Choose one of the listed framing options.';
        }
        $ancestors = [];
        if ($click === 'allow') {
            foreach (preg_split('/[\s,]+/', trim((string) ($input['frame_ancestors'] ?? ''))) ?: [] as $o) {
                if ($o === '') {
                    continue;
                }
                if (!preg_match('#^https://(?:\*\.)?[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+(?::[0-9]{1,5})?$#', $o) || in_array($o, $ancestors, true)) {
                    $errors['frame_ancestors'] = '“' . mb_substr($o, 0, 60) . '” must be an https:// address such as https://example.com, listed once.';
                    break;
                }
                $ancestors[] = $o;
            }
            if (!isset($errors['frame_ancestors']) && ($ancestors === [] || count($ancestors) > 10)) {
                $errors['frame_ancestors'] = 'List 1 to 10 sites that may embed this website, one per line.';
            }
        }
        $csp = '';
        try {
            $csp = self::normalizeCsp((string) ($input['csp'] ?? ''));
        } catch (\InvalidArgumentException $e) {
            $errors['csp'] = $e->getMessage();
        }
        $referrer = (string) ($input['referrer'] ?? '');
        if (!isset(self::REFERRER[$referrer])) {
            $errors['referrer'] = 'Choose one of the listed Referrer-Policy values.';
        }
        $perm = (string) ($input['permissions'] ?? '');
        $permCustom = '';
        if (!isset(self::PERMISSIONS[$perm])) {
            $errors['permissions'] = 'Choose one of the listed Permissions-Policy options.';
        } elseif ($perm === 'custom') {
            try {
                $permCustom = self::normalizePermissions((string) ($input['permissions_custom'] ?? ''));
            } catch (\InvalidArgumentException $e) {
                $errors['permissions_custom'] = $e->getMessage();
            }
        }
        $hsts = ['enabled' => false, 'max_age' => 31536000, 'include_subdomains' => false, 'preload' => false];
        if (($input['hsts_enabled'] ?? '0') === '1') {
            if (!self::hstsAvailable($site)) {
                $errors['hsts_enabled'] = 'HSTS needs working HTTPS with plain HTTP redirected to it. Set that up under SSL first.';
            } else {
                $age = filter_var($input['hsts_max_age'] ?? 31536000, FILTER_VALIDATE_INT);
                $sub = ($input['hsts_include_subdomains'] ?? '0') === '1';
                $preload = ($input['hsts_preload'] ?? '0') === '1';
                if ($age === false || $age < 300 || $age > 63072000) {
                    $errors['hsts_max_age'] = 'Choose a lifetime between 5 minutes and 2 years.';
                } elseif ($preload && (!$sub || $age < 31536000)) {
                    $errors['hsts_preload'] = 'Preload needs “include subdomains” and a lifetime of at least one year.';
                } else {
                    $hsts = ['enabled' => true, 'max_age' => $age, 'include_subdomains' => $sub, 'preload' => $preload];
                }
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return [
            'clickjacking' => $click, 'frame_ancestors' => $ancestors, 'csp' => $csp,
            'csp_report_only' => $csp !== '' && ($input['csp_report_only'] ?? '0') === '1',
            'referrer' => $referrer, 'permissions' => $perm, 'permissions_custom' => $permCustom, 'hsts' => $hsts,
        ];
    }

    /**
     * @param array<string, mixed> $input
     * @return array<string, mixed>
     */
    public function validateCache(array $input): array
    {
        $errors = [];
        $static = filter_var($input['static_ttl'] ?? 0, FILTER_VALIDATE_INT);
        $custom = trim((string) ($input['static_ttl_custom'] ?? ''));
        if ($custom !== '') {
            $static = filter_var($custom, FILTER_VALIDATE_INT);
        }
        if ($static === false || ($static !== 0 && ($static < self::MIN_STATIC_TTL || $static > self::MAX_STATIC_TTL))) {
            $errors['static_ttl'] = 'Use 0, or a lifetime between ' . self::MIN_STATIC_TTL . ' seconds and one year (' . self::MAX_STATIC_TTL . ' seconds).';
        }
        $mode = (string) ($input['dynamic_mode'] ?? 'app');
        if (!isset(self::DYNAMIC_MODES[$mode])) {
            $errors['dynamic_mode'] = 'Choose one of the listed options.';
        }
        $ttl = filter_var($input['dynamic_ttl'] ?? 60, FILTER_VALIDATE_INT);
        if ($mode === 'public' && ($ttl === false || $ttl < self::MIN_DYNAMIC_TTL || $ttl > self::MAX_DYNAMIC_TTL)) {
            $errors['dynamic_ttl'] = 'Use between ' . self::MIN_DYNAMIC_TTL . ' seconds and one hour (' . self::MAX_DYNAMIC_TTL . ' seconds).';
        }
        $paths = [];
        foreach (preg_split('/\R/', (string) ($input['bypass_paths'] ?? '')) ?: [] as $line) {
            $p = rtrim(trim($line), '/');
            if ($p === '' ) {
                continue;
            }
            if (strlen($p) > 100 || !preg_match('#^/[A-Za-z0-9._~-]+(?:/[A-Za-z0-9._~-]+)*$#', $p) || str_contains($p, '..')) {
                $errors['bypass_paths'] = '“' . mb_substr($p, 0, 60) . '” must be a path such as /members (letters, digits and . _ ~ - only).';
                break;
            }
            if (!in_array($p, $paths, true)) {
                $paths[] = $p;
            }
        }
        if (!isset($errors['bypass_paths']) && count($paths) > self::MAX_BYPASS) {
            $errors['bypass_paths'] = 'At most ' . self::MAX_BYPASS . ' paths.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return [
            'static_ttl' => (int) $static, 'dynamic_mode' => $mode,
            'dynamic_ttl' => $mode === 'public' ? (int) $ttl : 60, 'bypass_paths' => $paths,
        ];
    }

    /** @throws \InvalidArgumentException with a message for the customer */
    public static function normalizeCsp(string $text): string
    {
        $text = trim($text);
        if ($text === '') {
            return '';
        }
        if (strlen($text) > self::MAX_CSP) {
            throw new \InvalidArgumentException('The policy is longer than ' . self::MAX_CSP . ' characters.');
        }
        if (preg_match('/["\\\\$`{}<>\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/', $text) === 1) {
            throw new \InvalidArgumentException('The policy contains a character that is not allowed (quotes other than \' \', backslash, $, braces, angle brackets or control characters).');
        }
        $seen = [];
        $out = [];
        foreach (explode(';', preg_replace('/\s+/', ' ', $text) ?? '') as $part) {
            $fields = preg_split('/ +/', trim($part)) ?: [];
            if ($fields === [] || $fields[0] === '') {
                continue;
            }
            $name = strtolower((string) array_shift($fields));
            if ($name === 'frame-ancestors') {
                throw new \InvalidArgumentException('frame-ancestors is set by “Framing protection” above; remove it from this policy.');
            }
            if (isset($seen[$name])) {
                throw new \InvalidArgumentException($name . ' appears twice.');
            }
            $seen[$name] = true;
            if ($name === 'upgrade-insecure-requests' || $name === 'block-all-mixed-content') {
                if ($fields !== []) {
                    throw new \InvalidArgumentException($name . ' takes no value.');
                }
            } elseif ($name === 'report-uri') {
                if (count($fields) !== 1 || !preg_match('#^(?:https?://[A-Za-z0-9.-]+(?::[0-9]{1,5})?)?/[A-Za-z0-9._~/%?=&+-]{0,300}$#', $fields[0])) {
                    throw new \InvalidArgumentException('report-uri needs one address or path, for example /csp-report.');
                }
            } elseif (in_array($name, self::CSP_DIRECTIVES, true)) {
                if ($fields === []) {
                    throw new \InvalidArgumentException($name . " needs at least one source, for example 'self'.");
                }
                if (count($fields) > 40) {
                    throw new \InvalidArgumentException($name . ' has too many sources.');
                }
                foreach ($fields as $v) {
                    if (!self::validCspSource($v)) {
                        throw new \InvalidArgumentException('“' . mb_substr($v, 0, 60) . '” in ' . $name . ' is not a valid source.');
                    }
                }
                if (count($fields) > 1 && in_array("'none'", $fields, true)) {
                    throw new \InvalidArgumentException("'none' cannot be combined with other sources in " . $name . '.');
                }
            } else {
                throw new \InvalidArgumentException($name . ' is not a supported directive.');
            }
            $out[] = implode(' ', array_merge([$name], $fields));
        }
        return implode('; ', $out);
    }

    private static function validCspSource(string $v): bool
    {
        return $v === '*'
            || preg_match("/^'(?:self|none|unsafe-inline|unsafe-eval|strict-dynamic|report-sample|unsafe-hashes|wasm-unsafe-eval)'$/", $v) === 1
            || preg_match("/^'(?:sha256|sha384|sha512)-[A-Za-z0-9+\\/=_-]{20,200}'$/", $v) === 1
            || preg_match('/^(?:https|http|data|blob|wss|ws):$/', $v) === 1
            || (!str_starts_with($v, "'") && preg_match('#^(?:(?:https?|wss?)://)?(?:\*\.)?[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*(?::(?:[0-9]{1,5}|\*))?(?:/[A-Za-z0-9._~/%-]*)?$#', $v) === 1);
    }

    /** @throws \InvalidArgumentException with a message for the customer */
    public static function normalizePermissions(string $text): string
    {
        $text = trim($text);
        if ($text === '') {
            throw new \InvalidArgumentException('Enter at least one directive, for example camera=(), geolocation=(self).');
        }
        if (strlen($text) > 1500) {
            throw new \InvalidArgumentException('The policy is too long.');
        }
        $origin = '"https://[A-Za-z0-9.-]+(?::[0-9]{1,5})?"';
        $list = '#^(?:\*|\(\)|\((?:self|' . $origin . ')(?: (?:self|' . $origin . '))*\))$#';
        $seen = [];
        foreach (explode(',', $text) as $entry) {
            $entry = trim($entry);
            $parts = explode('=', $entry, 2);
            if (count($parts) !== 2 || !preg_match('/^[a-z][a-z-]{1,40}$/', trim($parts[0]))) {
                throw new \InvalidArgumentException('“' . mb_substr($entry, 0, 60) . '” is not in the form feature=(allowlist).');
            }
            [$name, $allow] = [trim($parts[0]), trim($parts[1])];
            if (!in_array($name, self::PERMISSION_FEATURES, true)) {
                throw new \InvalidArgumentException($name . ' is not a supported feature.');
            }
            if ($allow === 'self') {
                $allow = '(self)';
            }
            if (preg_match($list, $allow) !== 1) {
                throw new \InvalidArgumentException('The allowlist of ' . $name . ' must be (), *, (self) or (self "https://example.com").');
            }
            if (isset($seen[$name])) {
                throw new \InvalidArgumentException($name . ' appears twice.');
            }
            $seen[$name] = $allow;
        }
        ksort($seen);
        return implode(', ', array_map(static fn (string $k, string $v): string => $k . '=' . $v, array_keys($seen), $seen));
    }
}
