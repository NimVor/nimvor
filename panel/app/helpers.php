<?php

declare(strict_types=1);

/**
 * Global template helpers. Kept deliberately small.
 */

if (!function_exists('e')) {
    /** HTML-escape any scalar for output. */
    function e(mixed $value): string
    {
        if ($value === null || is_array($value) || is_object($value)) {
            return '';
        }
        if (is_bool($value)) {
            return $value ? 'true' : 'false';
        }
        return htmlspecialchars((string) $value, ENT_QUOTES | ENT_SUBSTITUTE | ENT_HTML5, 'UTF-8');
    }
}

if (!function_exists('format_bytes')) {
    function format_bytes(int|float|null $bytes, int $precision = 1): string
    {
        if ($bytes === null) {
            return '—';
        }
        $units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
        $i = 0;
        $value = (float) $bytes;
        while ($value >= 1024 && $i < count($units) - 1) {
            $value /= 1024;
            $i++;
        }
        return round($value, $i === 0 ? 0 : $precision) . ' ' . $units[$i];
    }
}

if (!function_exists('format_duration')) {
    function format_duration(int|float $seconds): string
    {
        $seconds = (int) $seconds;
        $d = intdiv($seconds, 86400);
        $h = intdiv($seconds % 86400, 3600);
        $m = intdiv($seconds % 3600, 60);
        if ($d > 0) {
            return "{$d}d {$h}h";
        }
        if ($h > 0) {
            return "{$h}h {$m}m";
        }
        return "{$m}m";
    }
}

if (!function_exists('time_ago')) {
    function time_ago(?string $iso, ?int $now = null): string
    {
        if ($iso === null || $iso === '') {
            return 'never';
        }
        $ts = strtotime($iso);
        if ($ts === false) {
            return '—';
        }
        $diff = ($now ?? time()) - $ts;
        return match (true) {
            $diff < 0 => 'in the future',
            $diff < 60 => 'just now',
            $diff < 3600 => intdiv($diff, 60) . ' min ago',
            $diff < 86400 => intdiv($diff, 3600) . ' h ago',
            default => intdiv($diff, 86400) . ' d ago',
        };
    }
}
