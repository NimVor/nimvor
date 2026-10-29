<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

/** Small values the site remembers between requests (when the docs were last synced…). */
final class State
{
    public function __construct(private readonly Db $db)
    {
    }

    public function get(string $key): ?string
    {
        $v = $this->db->scalar('SELECT v FROM site_state WHERE k = ?', [$key]);
        return $v === null ? null : (string) $v;
    }

    public function set(string $key, string $value): void
    {
        $this->db->run('INSERT INTO site_state (k, v, updated_at) VALUES (?, ?, UTC_TIMESTAMP()) ON DUPLICATE KEY UPDATE v = VALUES(v), updated_at = VALUES(updated_at)', [$key, $value]);
    }

    /** Seconds since the value was last written, or null when it never was. */
    public function age(string $key): ?int
    {
        $v = $this->db->scalar('SELECT TIMESTAMPDIFF(SECOND, updated_at, UTC_TIMESTAMP()) FROM site_state WHERE k = ?', [$key]);
        return $v === null ? null : (int) $v;
    }
}
