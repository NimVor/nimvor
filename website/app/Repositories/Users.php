<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

final class Users
{
    public function __construct(private readonly Db $db)
    {
    }

    /** @return array<string, mixed>|null */
    public function find(int $id): ?array
    {
        return $this->db->one('SELECT * FROM users WHERE id = ?', [$id]);
    }

    /** @return array<string, mixed>|null */
    public function byEmail(string $email): ?array
    {
        return $this->db->one('SELECT * FROM users WHERE email = ?', [strtolower($email)]);
    }

    public function create(string $email, string $name, string $hash, string $role = 'client'): int
    {
        $this->db->run('INSERT INTO users (email, name, password_hash, role, created_at) VALUES (?, ?, ?, ?, UTC_TIMESTAMP())', [strtolower($email), $name, $hash, $role]);
        return $this->db->lastId();
    }

    public function setPassword(int $id, string $hash): void
    {
        $this->db->run('UPDATE users SET password_hash = ? WHERE id = ?', [$hash, $id]);
    }

    public function setName(int $id, string $name): void
    {
        $this->db->run('UPDATE users SET name = ? WHERE id = ?', [$name, $id]);
    }

    public function setActive(int $id, bool $active): void
    {
        $this->db->run('UPDATE users SET is_active = ? WHERE id = ?', [$active ? 1 : 0, $id]);
    }

    public function touchLogin(int $id): void
    {
        $this->db->run('UPDATE users SET last_login_at = UTC_TIMESTAMP() WHERE id = ?', [$id]);
    }

    public function count(): int
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM users');
    }

    public function admins(): int
    {
        return (int) $this->db->scalar("SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = 1");
    }

    /** @return list<array<string, mixed>> */
    public function page(int $limit, int $offset): array
    {
        return $this->db->all('SELECT id, email, name, role, is_active, created_at, last_login_at FROM users ORDER BY id DESC LIMIT ' . (int) $limit . ' OFFSET ' . (int) $offset);
    }
}
