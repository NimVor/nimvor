<?php

declare(strict_types=1);

namespace WhaCP\Repositories;

use WhaCP\Database\Connection;

final class AuditLogRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    /** @param array<string, mixed> $row */
    public function insert(array $row): void
    {
        $this->db->run(
            'INSERT INTO audit_logs (user_id, username, action, target_type, target_id, result, ip, user_agent, details, created_at)
             VALUES (:user_id, :username, :action, :target_type, :target_id, :result, :ip, :user_agent, :details, :created_at)',
            $row
        );
    }

    /**
     * @param array{action?: string, result?: string, user?: string, q?: string} $filters
     * @return array{rows: list<array<string, mixed>>, total: int}
     */
    public function search(array $filters, int $page, int $perPage): array
    {
        $where = [];
        $params = [];
        if (($filters['action'] ?? '') !== '') {
            $where[] = "action LIKE :action ESCAPE '\\'";
            $params['action'] = str_replace(['%', '_'], ['\%', '\_'], (string) $filters['action']) . '%';
        }
        if (($filters['result'] ?? '') !== '') {
            $where[] = 'result = :result';
            $params['result'] = (string) $filters['result'];
        }
        if (($filters['user'] ?? '') !== '') {
            $where[] = 'username = :user';
            $params['user'] = (string) $filters['user'];
        }
        if (($filters['q'] ?? '') !== '') {
            $where[] = "(target_id LIKE :q ESCAPE '\\' OR details LIKE :q ESCAPE '\\' OR ip LIKE :q ESCAPE '\\')";
            $params['q'] = '%' . str_replace(['%', '_'], ['\%', '\_'], (string) $filters['q']) . '%';
        }
        $sqlWhere = $where === [] ? '' : 'WHERE ' . implode(' AND ', $where);

        $total = (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs {$sqlWhere}", $params);
        $params['limit'] = $perPage;
        $params['offset'] = max(0, ($page - 1) * $perPage);
        $rows = $this->db->fetchAll("SELECT * FROM audit_logs {$sqlWhere} ORDER BY id DESC LIMIT :limit OFFSET :offset", $params);
        return ['rows' => $rows, 'total' => $total];
    }

    /** @return list<array<string, mixed>> */
    public function recent(int $limit = 10): array
    {
        return $this->db->fetchAll('SELECT * FROM audit_logs ORDER BY id DESC LIMIT ?', [$limit]);
    }
}
