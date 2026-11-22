<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

final class Sponsors
{
    public const TIERS = ['gold', 'silver', 'supporter'];
    public const SOURCES = ['manual', 'github', 'patreon'];

    public function __construct(private readonly Db $db)
    {
    }

    /** @return list<array<string, mixed>> */
    public function all(): array
    {
        return $this->db->all("SELECT * FROM sponsors ORDER BY FIELD(tier, 'gold', 'silver', 'supporter'), monthly_usd DESC, name");
    }

    /**
     * What the public Sponsors page shows: the sponsors of the sources that are switched on and that are not hidden.
     *
     * @param list<string> $sources any of manual, github, patreon
     * @return list<array<string, mixed>>
     */
    public function shown(array $sources): array
    {
        $sources = array_values(array_intersect($sources, self::SOURCES));
        if ($sources === []) {
            return [];
        }
        return $this->db->all(
            "SELECT * FROM sponsors WHERE visible = 1 AND source IN (" . implode(',', array_fill(0, count($sources), '?')) . ") ORDER BY FIELD(tier, 'gold', 'silver', 'supporter'), monthly_usd DESC, name",
            $sources
        );
    }

    public function create(string $name, string $url, string $tier): void
    {
        $this->db->run('INSERT INTO sponsors (name, url, tier, created_at) VALUES (?, ?, ?, UTC_TIMESTAMP())', [$name, $url, $tier]);
    }

    /** Changes a sponsor added by hand (synced ones are overwritten by the next sync, so they can only be hidden). */
    public function updateManual(int $id, string $name, string $url, string $tier, bool $visible): bool
    {
        return $this->db->run("UPDATE sponsors SET name = ?, url = ?, tier = ?, visible = ? WHERE id = ? AND source = 'manual'", [$name, $url, $tier, $visible ? 1 : 0, $id]) > 0;
    }

    public function setVisible(int $id, bool $visible): void
    {
        $this->db->run('UPDATE sponsors SET visible = ? WHERE id = ?', [$visible ? 1 : 0, $id]);
    }

    /** @return list<string> */
    public function patreonIds(): array
    {
        return array_column($this->db->all("SELECT patreon_id FROM sponsors WHERE source = 'patreon'"), 'patreon_id');
    }

    public function upsertPatreon(string $id, string $name, string $url, string $tier, int $monthlyUsd): void
    {
        // A sponsor hidden by an administrator stays hidden when the next sync updates the rest.
        $this->db->run(
            "INSERT INTO sponsors (name, url, tier, source, patreon_id, monthly_usd, created_at) VALUES (?, ?, ?, 'patreon', ?, ?, UTC_TIMESTAMP())
             ON DUPLICATE KEY UPDATE name = VALUES(name), url = VALUES(url), tier = VALUES(tier), monthly_usd = VALUES(monthly_usd)",
            [$name, $url, $tier, $id, $monthlyUsd]
        );
    }

    /** @param list<string> $keep Patreon member ids that are still patrons */
    public function removePatreonExcept(array $keep): int
    {
        if ($keep === []) {
            return $this->db->run("DELETE FROM sponsors WHERE source = 'patreon'");
        }
        return $this->db->run("DELETE FROM sponsors WHERE source = 'patreon' AND patreon_id NOT IN (" . implode(',', array_fill(0, count($keep), '?')) . ')', $keep);
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
