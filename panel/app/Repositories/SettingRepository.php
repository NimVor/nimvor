<?php

declare(strict_types=1);

namespace NimVor\Repositories;

use NimVor\Database\Connection;

final class SettingRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    /** @return array<string, mixed> */
    public function all(): array
    {
        $out = [];
        foreach ($this->db->fetchAll('SELECT key, value FROM settings') as $row) {
            $out[(string) $row['key']] = json_decode((string) $row['value'], true);
        }
        return $out;
    }

    public function set(string $key, mixed $value, ?int $userId): void
    {
        $this->db->run(
            'INSERT INTO settings (key, value, updated_at, updated_by) VALUES (?, ?, ?, ?)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at, updated_by = excluded.updated_by',
            [$key, json_encode($value, JSON_THROW_ON_ERROR), gmdate('c'), $userId]
        );
    }
}
