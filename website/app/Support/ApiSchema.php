<?php

declare(strict_types=1);

namespace NimVorSite\Support;

/** Turns OpenAPI schemas into table rows and requests into example commands, for the API reference. */
final class ApiSchema
{
    private const MAX_DEPTH = 5;

    /** @param array<string, mixed> $spec */
    public function __construct(private readonly array $spec)
    {
    }

    /**
     * @param array<string, mixed> $node
     * @return array<string, mixed>
     */
    public function resolve(array $node): array
    {
        for ($i = 0; $i < 10 && isset($node['$ref']) && is_string($node['$ref']); $i++) {
            $cur = $this->spec;
            foreach (explode('/', substr($node['$ref'], 2)) as $part) {
                $cur = is_array($cur) && isset($cur[$part]) ? $cur[$part] : [];
            }
            $name = basename($node['$ref']);
            $node = (array) $cur + ['x-name' => $name];
        }
        return $node;
    }

    /**
     * Resolves a schema and merges its `allOf` parts into one (properties and required fields).
     *
     * @param array<string, mixed> $schema
     * @return array<string, mixed>
     */
    private function flatten(array $schema): array
    {
        $schema = $this->resolve($schema);
        foreach ((array) ($schema['allOf'] ?? []) as $part) {
            $part = $this->flatten((array) $part);
            $schema['properties'] = (array) ($part['properties'] ?? []) + (array) ($schema['properties'] ?? []);
            $schema['required'] = array_merge((array) ($schema['required'] ?? []), (array) ($part['required'] ?? []));
        }
        unset($schema['allOf']);
        return $schema;
    }

    /** The schema's type in words: `string`, `integer[]`, `object`, `string|null`. */
    public function type(array $schema): string
    {
        $schema = $this->resolve($schema);
        if (isset($schema['const'])) {
            return var_export($schema['const'], true);
        }
        $t = $schema['type'] ?? null;
        $types = is_array($t) ? $t : ($t === null ? [] : [$t]);
        $out = [];
        foreach ($types as $x) {
            $out[] = $x === 'array' ? $this->type((array) ($schema['items'] ?? [])) . '[]' : (string) $x;
        }
        $s = $out === [] ? 'any' : implode('|', $out);
        if (isset($schema['format'])) {
            $s .= ' (' . $schema['format'] . ')';
        }
        return $s;
    }

    /**
     * One row per property, nested properties as dotted paths.
     *
     * @param array<string, mixed> $schema
     * @return list<array{name: string, type: string, required: bool, description: string, notes: string}>
     */
    public function rows(array $schema, string $prefix = '', int $depth = 0): array
    {
        $schema = $this->flatten($schema);
        if (($schema['type'] ?? '') === 'array' && isset($schema['items'])) {
            $schema = $this->resolve((array) $schema['items']);
            $prefix = $prefix === '' ? '[]' : $prefix . '[]';
        }
        $rows = [];
        $required = (array) ($schema['required'] ?? []);
        foreach ((array) ($schema['properties'] ?? []) as $name => $prop) {
            $prop = (array) $prop;
            $res = $this->resolve($prop);
            $rows[] = [
                'name' => $prefix === '' ? (string) $name : $prefix . '.' . $name,
                'type' => $this->type($prop),
                'required' => in_array($name, $required, true),
                'description' => (string) ($res['description'] ?? $prop['description'] ?? ''),
                'notes' => $this->notes($res),
            ];
            $inner = ($res['type'] ?? '') === 'array' ? $this->resolve((array) ($res['items'] ?? [])) : $res;
            if ($depth < self::MAX_DEPTH && isset($inner['properties'])) {
                $child = ($res['type'] ?? '') === 'array' ? ($prefix === '' ? (string) $name : $prefix . '.' . $name) . '[]' : ($prefix === '' ? (string) $name : $prefix . '.' . $name);
                array_push($rows, ...$this->rows($inner, $child, $depth + 1));
            }
        }
        return $rows;
    }

    /** @param array<string, mixed> $s */
    private function notes(array $s): string
    {
        $n = [];
        if (isset($s['enum'])) {
            $n[] = 'one of ' . implode(', ', array_map(static fn ($v): string => (string) $v, (array) $s['enum']));
        }
        foreach (['default' => 'default', 'minimum' => 'min', 'maximum' => 'max', 'pattern' => 'pattern'] as $k => $label) {
            if (isset($s[$k])) {
                $v = $s[$k];
                $n[] = $label . ' ' . (is_bool($v) ? ($v ? 'true' : 'false') : (string) $v);
            }
        }
        return implode('; ', $n);
    }

    /**
     * A curl command for an operation, using the request example when there is one.
     *
     * @param array<string, mixed> $op
     */
    public function curl(array $op, string $base = 'https://panel.example.com/api/v1'): string
    {
        $path = (string) $op['path'];
        $query = [];
        foreach ((array) ($op['parameters'] ?? []) as $p) {
            $p = $this->resolve((array) $p);
            if (($p['in'] ?? '') === 'path') {
                $path = str_replace('{' . $p['name'] . '}', ($p['schema']['type'] ?? '') === 'integer' ? '7' : ($p['name'] === 'id' ? '3f9a1c2b7d4e5a60' : 'example'), $path);
            }
        }
        $method = (string) $op['method'];
        $lines = ['curl -s' . ($method === 'GET' ? '' : ' -X ' . $method) . ' \\'];
        $lines[] = '  -H "Authorization: Bearer nimvor_…" \\';
        $body = $op['requestBody']['content']['application/json']['example'] ?? null;
        if ($body !== null) {
            $lines[] = '  -H "Content-Type: application/json" \\';
            $lines[] = "  -d '" . json_encode($body, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE) . "' \\";
        }
        $lines[] = '  "' . $base . $path . ($query === [] ? '' : '?' . implode('&', $query)) . '"';
        return implode("\n", $lines);
    }
}
