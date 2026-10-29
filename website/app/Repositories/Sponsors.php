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
        return $this->db->all("SELECT * FROM sponsors ORDER BY FIELD(tier, 'gold', 'silver', 'supporter'), monthly_usd DESC, name");
    }

    public function create(string $name, string $url, string $tier): void
    {
        $this->db->run('INSERT INTO sponsors (name, url, tier, created_at) VALUES (?, ?, ?, UTC_TIMESTAMP())', [$name, $url, $tier]);
    }

    /** @return list<string> */
    public function githubLogins(): array
    {
        return array_column($this->db->all("SELECT github_login FROM sponsors WHERE source = 'github'"), 'github_login');
    }

    public function upsertGithub(string $login, string $name, string $url, string $tier, int $monthlyUsd): void
    {
        $this->db->run(
            "INSERT INTO sponsors (name, url, tier, source, github_login, monthly_usd, created_at) VALUES (?, ?, ?, 'github', ?, ?, UTC_TIMESTAMP())
             ON DUPLICATE KEY UPDATE name = VALUES(name), url = VALUES(url), tier = VALUES(tier), monthly_usd = VALUES(monthly_usd)",
            [$name, $url, $tier, $login, $monthlyUsd]
        );
    }

    /** @param list<string> $keep GitHub logins that are still sponsors */
    public function removeGithubExcept(array $keep): int
    {
        if ($keep === []) {
            return $this->db->run("DELETE FROM sponsors WHERE source = 'github'");
        }
        return $this->db->run("DELETE FROM sponsors WHERE source = 'github' AND github_login NOT IN (" . implode(',', array_fill(0, count($keep), '?')) . ')', $keep);
    }

    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM sponsors WHERE id = ?', [$id]);
    }
}
