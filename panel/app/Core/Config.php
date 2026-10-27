<?php

declare(strict_types=1);

namespace WhaCP\Core;

/**
 * Read-only configuration with dot-notation access.
 */
final class Config
{
    /** @param array<string, mixed> $items */
    public function __construct(private readonly array $items)
    {
    }

    /**
     * Load defaults from $defaultsFile and overlay an optional local file.
     */
    public static function load(string $defaultsFile, ?string $overrideFile = null): self
    {
        /** @var array<string, mixed> $items */
        $items = require $defaultsFile;
        if ($overrideFile !== null && $overrideFile !== '' && is_file($overrideFile)) {
            $override = require $overrideFile;
            if (is_array($override)) {
                $items = self::merge($items, $override);
            }
        }
        return new self($items);
    }

    public function get(string $key, mixed $default = null): mixed
    {
        $value = $this->items;
        foreach (explode('.', $key) as $segment) {
            if (!is_array($value) || !array_key_exists($segment, $value)) {
                return $default;
            }
            $value = $value[$segment];
        }
        return $value;
    }

    public function string(string $key, string $default = ''): string
    {
        $v = $this->get($key, $default);
        return is_scalar($v) ? (string) $v : $default;
    }

    public function int(string $key, int $default = 0): int
    {
        $v = $this->get($key, $default);
        return is_numeric($v) ? (int) $v : $default;
    }

    public function bool(string $key, bool $default = false): bool
    {
        $v = $this->get($key, $default);
        return is_bool($v) ? $v : filter_var($v, FILTER_VALIDATE_BOOL);
    }

    /** @return array<string, mixed> */
    public function all(): array
    {
        return $this->items;
    }

    /**
     * @param array<string, mixed> $base
     * @param array<string, mixed> $override
     * @return array<string, mixed>
     */
    private static function merge(array $base, array $override): array
    {
        foreach ($override as $key => $value) {
            if (is_array($value) && isset($base[$key]) && is_array($base[$key]) && !array_is_list($value)) {
                $base[$key] = self::merge($base[$key], $value);
            } else {
                $base[$key] = $value;
            }
        }
        return $base;
    }
}
