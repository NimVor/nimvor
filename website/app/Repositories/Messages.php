<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

final class Messages
{
    public const STATUSES = ['new', 'read', 'closed'];

    public function __construct(private readonly Db $db)
    {
    }

    public function create(?int $userId, string $name, string $email, string $subject, string $message): int
    {
        $this->db->run('INSERT INTO contact_messages (user_id, name, email, subject, message, created_at) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP())', [$userId, $name, strtolower($email), $subject, $message]);
        return $this->db->lastId();
    }

    /** @return list<array<string, mixed>> */
    public function all(int $limit = 200): array
    {
        return $this->db->all('SELECT * FROM contact_messages ORDER BY id DESC LIMIT ' . (int) $limit);
    }

    /** @return list<array<string, mixed>> */
    public function forUser(int $userId): array
    {
        return $this->db->all('SELECT id, subject, status, created_at FROM contact_messages WHERE user_id = ? ORDER BY id DESC LIMIT 100', [$userId]);
    }

    public function setStatus(int $id, string $status): void
    {
        if (in_array($status, self::STATUSES, true)) {
            $this->db->run('UPDATE contact_messages SET status = ? WHERE id = ?', [$status, $id]);
        }
    }
}
