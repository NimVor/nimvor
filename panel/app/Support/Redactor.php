<?php

declare(strict_types=1);

namespace NimVor\Support;

/**
 * Removes secrets from data before it is logged or stored in audit records.
 */
final class Redactor
{
    public const PLACEHOLDER = '[REDACTED]';

    private const SENSITIVE_KEY = '/pass(word|wd)?|secret|token|api[_-]?key|authorization|cookie|private[_-]?key|credential/i';

    /**
     * @param array<mixed> $data
     * @return array<mixed>
     */
    public static function redact(array $data, int $depth = 0): array
    {
        if ($depth > 8) {
            return ['…'];
        }
        $out = [];
        foreach ($data as $key => $value) {
            if (is_string($key) && preg_match(self::SENSITIVE_KEY, $key)) {
                $out[$key] = self::PLACEHOLDER;
                continue;
            }
            if (is_array($value)) {
                $out[$key] = self::redact($value, $depth + 1);
            } elseif ($value instanceof \Throwable) {
                $out[$key] = ['class' => $value::class, 'message' => self::redactString($value->getMessage())];
            } elseif (is_string($value)) {
                $out[$key] = self::redactString($value);
            } elseif (is_scalar($value) || $value === null) {
                $out[$key] = $value;
            } else {
                $out[$key] = get_debug_type($value);
            }
        }
        return $out;
    }

    public static function redactString(string $value): string
    {
        $patterns = [
            '/(\s-p)(\S+)/',
            '/(--?(?:password|passwd|pass|token|secret|api[-_]?key)(?:=|\s+))(\S+)/i',
            '/(\b[A-Z0-9_]*(?:PASSWORD|SECRET|TOKEN|API_KEY)[A-Z0-9_]*=)(\S+)/i',
            '#([a-z][a-z0-9+.-]*://[^\s:/@]+:)([^\s@]+)(@)#i',
            '/(bearer\s+)([A-Za-z0-9._~+\/=-]+)/i',
        ];
        foreach ($patterns as $p) {
            $value = (string) preg_replace_callback($p, static fn (array $m): string => $m[1] . self::PLACEHOLDER . ($m[3] ?? ''), $value);
        }
        return $value;
    }
}
