<?php

declare(strict_types=1);

namespace WhaCP\Repositories;

use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;

final class WebsiteRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    /**
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function all(int $serverId, array $user): array
    {
        [$cond, $params] = Scope::condition($user, 'w.owner_id');
        $rows = $this->db->fetchAll(
            "SELECT w.*, u.username AS owner_name, " . UserRepository::SUSPENDED . " AS account_suspended, " . UserRepository::BANDWIDTH_BLOCKED . " AS bandwidth_blocked FROM websites w LEFT JOIN users u ON u.id = w.owner_id
             WHERE w.server_id = :server AND {$cond} ORDER BY w.domain",
            ['server' => $serverId] + $params
        );
        return array_map([$this, 'hydrate'], $rows);
    }

    /**
     * @param array<string, mixed>|null $user null = no scoping (system use)
     * @return array<string, mixed>|null
     */
    public function find(int $serverId, int $id, ?array $user): ?array
    {
        [$cond, $params] = $user === null ? ['1 = 1', []] : Scope::condition($user, 'w.owner_id');
        $row = $this->db->fetch(
            "SELECT w.*, u.username AS owner_name, " . UserRepository::SUSPENDED . " AS account_suspended, " . UserRepository::BANDWIDTH_BLOCKED . " AS bandwidth_blocked FROM websites w LEFT JOIN users u ON u.id = w.owner_id
             WHERE w.server_id = :server AND w.id = :id AND {$cond}",
            ['server' => $serverId, 'id' => $id] + $params
        );
        return $row === null ? null : $this->hydrate($row);
    }

    /** @return list<array{id: int, owner_id: int|null}> websites run by a system account */
    public function usingSystemUser(int $serverId, string $user): array
    {
        return $this->db->fetchAll('SELECT id, owner_id FROM websites WHERE server_id = ? AND system_user = ?', [$serverId, $user]);
    }

    /**
     * Websites created from a website (subdomains, addon domains).
     *
     * @return list<array<string, mixed>>
     */
    public function children(int $serverId, int $parentId): array
    {
        $rows = $this->db->fetchAll("SELECT w.*, u.username AS owner_name, " . UserRepository::SUSPENDED . " AS account_suspended, " . UserRepository::BANDWIDTH_BLOCKED . " AS bandwidth_blocked FROM websites w LEFT JOIN users u ON u.id = w.owner_id WHERE w.server_id = ? AND w.parent_id = ? ORDER BY w.domain", [$serverId, $parentId]);
        return array_map([$this, 'hydrate'], $rows);
    }

    /** @return array<string, mixed>|null */
    public function findByDomain(int $serverId, string $domain): ?array
    {
        $row = $this->db->fetch('SELECT * FROM websites WHERE server_id = ? AND domain = ?', [$serverId, $domain]);
        return $row === null ? null : $this->hydrate($row);
    }

    /** Returns the website id owning a hostname, or null. */
    public function domainOwner(int $serverId, string $name): ?int
    {
        $v = $this->db->scalar('SELECT website_id FROM domains WHERE server_id = ? AND name = ?', [$serverId, $name]);
        return $v === null ? null : (int) $v;
    }

    /** @param array<string, mixed> $data */
    public function insert(array $data): int
    {
        $data['created_at'] = $data['updated_at'] = gmdate('c');
        $cols = array_keys($data);
        $this->db->run(
            'INSERT INTO websites (' . implode(', ', $cols) . ') VALUES (:' . implode(', :', $cols) . ')',
            $this->encode($data)
        );
        $id = $this->db->lastInsertId();
        $this->adopt($id);
        return $id;
    }

    /**
     * A DNS zone or email domain created earlier for exactly this website's
     * domain, not linked to a website and owned by nobody else, belongs to
     * the website (as if it had been chosen when creating it).
     */
    private function adopt(int $id): void
    {
        $w = $this->db->fetch('SELECT server_id, domain, owner_id FROM websites WHERE id = ?', [$id]);
        if ($w === null) {
            return;
        }
        foreach (['dns_zones' => 'name', 'mail_domains' => 'domain'] as $table => $col) {
            $this->db->run(
                "UPDATE {$table} SET website_id = :id, owner_id = COALESCE(owner_id, :owner)
                 WHERE server_id = :server AND {$col} = :domain AND website_id IS NULL AND (owner_id IS NULL OR owner_id IS :owner)",
                ['id' => $id, 'owner' => $w['owner_id'], 'server' => $w['server_id'], 'domain' => $w['domain']]
            );
        }
    }

    /** @param array<string, mixed> $data */
    public function update(int $id, array $data): void
    {
        $data['updated_at'] = gmdate('c');
        unset($data['id'], $data['created_at'], $data['owner_name'], $data['aliases']);
        $sets = implode(', ', array_map(static fn (string $c): string => "{$c} = :{$c}", array_keys($data)));
        $this->db->run("UPDATE websites SET {$sets} WHERE id = :id", $this->encode($data) + ['id' => $id]);
    }

    /** Records whether the server has the website answering 509 (bandwidth). */
    public function setBandwidthApplied(int $id, bool $blocked): void
    {
        $this->db->run('UPDATE websites SET bandwidth_applied = ? WHERE id = ?', [$blocked ? 1 : 0, $id]);
    }

    public function setStatus(int $id, string $status, string $error = ''): void
    {
        $this->db->run(
            'UPDATE websites SET status = ?, last_error = ?, applied_at = CASE WHEN ? = \'\' THEN ? ELSE applied_at END, updated_at = ? WHERE id = ?',
            [$status, mb_substr($error, 0, 2000), $error, gmdate('c'), gmdate('c'), $id]
        );
    }

    /** @param list<string> $names primary first */
    public function replaceDomains(int $serverId, int $websiteId, array $names): void
    {
        $this->db->transaction(function (Connection $db) use ($serverId, $websiteId, $names): void {
            $db->run('DELETE FROM domains WHERE website_id = ?', [$websiteId]);
            foreach ($names as $i => $n) {
                $db->run('INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (?, ?, ?, ?)', [$serverId, $websiteId, $n, $i === 0 ? 1 : 0]);
            }
        });
    }

    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM websites WHERE id = ?', [$id]);
    }

    public function countByOwner(int $ownerId): int
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE owner_id = ?', [$ownerId]);
    }

    /** @return list<string> */
    public function domainsOnServer(int $serverId): array
    {
        return array_map(static fn (array $r): string => (string) $r['name'], $this->db->fetchAll('SELECT name FROM domains WHERE server_id = ?', [$serverId]));
    }

    /**
     * @param array<string, mixed> $row
     * @return array<string, mixed>
     */
    private function hydrate(array $row): array
    {
        foreach (['php_settings' => [], 'redirects' => []] as $k => $default) {
            $v = json_decode((string) ($row[$k] ?? ''), true);
            $row[$k] = is_array($v) ? $v : $default;
        }
        $row['aliases'] = array_map(
            static fn (array $r): string => (string) $r['name'],
            $this->db->fetchAll('SELECT name FROM domains WHERE website_id = ? AND is_primary = 0 ORDER BY id', [(int) $row['id']])
        );
        foreach (['https', 'force_https', 'hsts', 'enabled'] as $b) {
            $row[$b] = (int) $row[$b] === 1;
        }
        $row['account_suspended'] = !empty($row['account_suspended']);
        $row['bandwidth_blocked'] = !empty($row['bandwidth_blocked']);
        return $row;
    }

    /**
     * @param array<string, mixed> $data
     * @return array<string, mixed>
     */
    private function encode(array $data): array
    {
        foreach ($data as $k => $v) {
            if (is_array($v)) {
                $data[$k] = json_encode($v, JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR);
            } elseif (is_bool($v)) {
                $data[$k] = $v ? 1 : 0;
            }
        }
        return $data;
    }
}
