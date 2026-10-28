<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

final class Sponsors
{
    public const TIERS = ['gold', 'silver', 'supporter'];

    public function __construct(private readonly Db $db)
    {
    }

    /** @return list<array<string, mixed>> */
    public function all(): array
    {
        return $this->db->all("SELECT * FROM sponsors ORDER BY FIELD(tier, 'gold', 'silver', 'supporter'), name");
    }

    public function create(string $name, string $url, string $tier): void
    {
        $this->db->run('INSERT INTO sponsors (name, url, tier, created_at) VALUES (?, ?, ?, UTC_TIMESTAMP())', [$name, $url, $tier]);
    }

    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM sponsors WHERE id = ?', [$id]);
    }
}
