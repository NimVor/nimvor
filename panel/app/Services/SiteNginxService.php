<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Support\ValidationException;

/**
 * Per-website Nginx tuning (administrators only): timeouts, buffers,
 * compression, a rate limit and a short list of allowlisted directives. The
 * checks here give friendly field errors; the agent checks everything again
 * and Nginx's own test runs before the change is kept.
 */
final class SiteNginxService
{
    /** field => [label, min, max] */
    public const NUMBERS = [
        'read_timeout_seconds' => ['Read timeout (s)', 10, 3600],
        'send_timeout_seconds' => ['Send timeout (s)', 10, 3600],
        'body_timeout_seconds' => ['Request body timeout (s)', 10, 3600],
        'keepalive_seconds' => ['Keep-alive timeout (s)', 1, 600],
        'fastcgi_buffer_kb' => ['PHP response buffer (KB)', 4, 256],
        'gzip_level' => ['Compression level', 1, 9],
        'rate_limit_kbps' => ['Speed limit per connection (KB/s)', 1, 1048576],
    ];

    /** Directives a custom line may use (kept in step with the agent). */
    public const EXTRA = [
        'client_body_buffer_size', 'client_header_buffer_size', 'large_client_header_buffers', 'client_header_timeout',
        'keepalive_requests', 'reset_timedout_connection', 'tcp_nodelay', 'tcp_nopush', 'limit_rate_after',
        'fastcgi_connect_timeout', 'fastcgi_send_timeout', 'fastcgi_buffers', 'fastcgi_busy_buffers_size',
        'proxy_connect_timeout', 'proxy_send_timeout', 'proxy_buffering', 'proxy_buffer_size', 'proxy_buffers', 'proxy_busy_buffers_size',
        'gzip_min_length', 'gzip_vary', 'gzip_proxied', 'gzip_types',
    ];

    public function __construct(private readonly WebsiteService $websites)
    {
    }

    /**
     * @param array<string, mixed> $input
     * @return array<string, mixed> the settings to store (only what differs from the server-wide values)
     */
    public static function normalize(array $input, string $kind): array
    {
        $errors = [];
        $out = [];
        foreach (self::NUMBERS as $k => [$label, $lo, $hi]) {
            $raw = trim((string) ($input[$k] ?? ''));
            if ($raw === '' || $raw === '0') {
                continue;
            }
            $n = filter_var($raw, FILTER_VALIDATE_INT);
            if ($n === false || $n < $lo || $n > $hi) {
                $errors[$k] = "{$label}: enter a whole number from {$lo} to {$hi}, or leave empty.";
                continue;
            }
            if ($k === 'fastcgi_buffer_kb' && $kind !== 'php') {
                $errors[$k] = 'Only PHP websites have a PHP response buffer.';
                continue;
            }
            $out[$k] = $n;
        }
        $gzip = (string) ($input['gzip'] ?? '');
        if ($gzip === 'on' || $gzip === 'off') {
            $out['gzip'] = $gzip === 'on';
        }
        if (isset($out['gzip_level']) && ($out['gzip'] ?? true) === false) {
            $errors['gzip_level'] = 'Turn compression on, or leave the level empty.';
        }
        $extra = [];
        $lines = preg_split('/\R/', (string) ($input['extra'] ?? '')) ?: [];
        foreach ($lines as $line) {
            $line = trim(rtrim(trim($line), ';'));
            if ($line === '') {
                continue;
            }
            [$name, $value] = array_pad(explode(' ', $line, 2), 2, '');
            $value = trim($value);
            if (!in_array($name, self::EXTRA, true)) {
                $errors['extra'] = "“{$name}” is not an allowed directive. Allowed: " . implode(', ', self::EXTRA) . '.';
                break;
            }
            if ($value === '' || preg_match('/^[A-Za-z0-9._:\/*+-]+( [A-Za-z0-9._:\/*+-]+){0,9}$/', $value) !== 1) {
                $errors['extra'] = "“{$name}” needs a value made of letters, digits and . _ : / * + - only.";
                break;
            }
            if (isset($extra[$name])) {
                $errors['extra'] = "“{$name}” is given twice.";
                break;
            }
            $extra[$name] = "{$name} {$value}";
        }
        if (count($extra) > 20) {
            $errors['extra'] = 'At most 20 custom directives.';
        }
        if ($extra !== []) {
            $out['extra'] = array_values($extra);
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return $out;
    }

    /**
     * Stores and applies the tuning; the website is put back as it was when the agent refuses it.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $input
     */
    public function save(array $site, array $input): void
    {
        $settings = self::normalize($input, (string) $site['kind']);
        $this->websites->patch($site, ['nginx_settings' => $settings], 'website.nginx');
    }
}
