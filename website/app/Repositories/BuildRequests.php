<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

final class BuildRequests
{
    public const STATUSES = ['new', 'quoted', 'in_progress', 'done', 'closed'];
    public const SERVICES = [
        'migration' => 'Migrate a server to NimVor',
        'install' => 'Install and harden a NimVor server',
        'custom' => 'Custom feature or plugin',
        'review' => 'Security and configuration review',
        'support' => 'Ongoing support',
    ];

    public function __construct(private readonly Db $db)
    {
    }

    public function create(?int $userId, string $name, string $email, string $service, string $details): int
    {
        $this->db->run('INSERT INTO build_requests (user_id, name, email, service, details, created_at, updated_at) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP(), UTC_TIMESTAMP())', [$userId, $name, strtolower($email), $service, $details]);
        return $this->db->lastId();
    }

    /** @return list<array<string, mixed>> */
    public function all(int $limit = 200): array
    {
        return $this->db->all('SELECT * FROM build_requests ORDER BY id DESC LIMIT ' . (int) $limit);
    }

    /** @return list<array<string, mixed>> */
    public function forUser(int $userId): array
    {
        return $this->db->all('SELECT * FROM build_requests WHERE user_id = ? ORDER BY id DESC LIMIT 100', [$userId]);
    }

    public function setStatus(int $id, string $status): void
    {
        if (in_array($status, self::STATUSES, true)) {
            $this->db->run('UPDATE build_requests SET status = ?, updated_at = UTC_TIMESTAMP() WHERE id = ?', [$status, $id]);
        }
    }
}
