<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

/** Support tickets, their replies and the canned replies. */
final class Tickets
{
    public const STATUSES = ['open', 'answered', 'on_hold', 'resolved', 'closed'];
    public const PRIORITIES = ['low', 'normal', 'high', 'urgent'];
    public const CATEGORIES = ['general', 'support', 'sales', 'sponsorship', 'press', 'security', 'other'];
    /** What staff still has to look at or wait for: everything that is not finished. */
    public const ACTIVE = ['open', 'answered', 'on_hold'];
    public const PER_PAGE = 25;
    private const REF_ALPHABET = 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789';

    public function __construct(private readonly Db $db)
    {
    }

    /** A reference people can read out over the phone: NV-7K3Q9XPA. */
    public static function newRef(): string
    {
        $s = '';
        for ($i = 0; $i < 8; $i++) {
            $s .= self::REF_ALPHABET[random_int(0, strlen(self::REF_ALPHABET) - 1)];
        }
        return 'NV-' . $s;
    }

    /** @return array{id: int, ref: string, token: string} */
    public function create(?int $userId, string $name, string $email, string $subject, string $category, string $priority): array
    {
        $token = bin2hex(random_bytes(16));
        for ($try = 0; $try < 5; $try++) {
            $ref = self::newRef();
            if ($this->db->scalar('SELECT 1 FROM tickets WHERE ref = ?', [$ref]) === null) {
                break;
            }
        }
        $this->db->run(
            'INSERT INTO tickets (ref, access_token, user_id, name, email, subject, category, priority, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, \'open\', UTC_TIMESTAMP(), UTC_TIMESTAMP())',
            [$ref, $token, $userId, $name, strtolower($email), $subject, $category, $priority]
        );
        return ['id' => $this->db->lastId(), 'ref' => $ref, 'token' => $token];
    }

    /** @return array<string, mixed>|null */
    public function find(int $id): ?array
    {
        return $this->db->one('SELECT t.*, a.name AS assignee_name FROM tickets t LEFT JOIN users a ON a.id = t.assigned_to WHERE t.id = ?', [$id]);
    }

    /** @return array<string, mixed>|null */
    public function byRef(string $ref): ?array
    {
        return $this->db->one('SELECT t.*, a.name AS assignee_name FROM tickets t LEFT JOIN users a ON a.id = t.assigned_to WHERE t.ref = ?', [strtoupper($ref)]);
    }

    public function addReply(int $ticketId, ?int $userId, string $author, string $body, bool $internal = false): int
    {
        $this->db->run('INSERT INTO ticket_replies (ticket_id, user_id, author, body, internal, created_at) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP())', [$ticketId, $userId, $author, $body, $internal ? 1 : 0]);
        $this->db->run('UPDATE tickets SET updated_at = UTC_TIMESTAMP() WHERE id = ?', [$ticketId]);
        return $this->db->lastId();
    }

    /**
     * The thread, oldest first. The customer never gets the internal notes and system events.
     *
     * @return list<array<string, mixed>>
     */
    public function replies(int $ticketId, bool $withInternal): array
    {
        return $this->db->all(
            'SELECT r.*, u.name AS author_name FROM ticket_replies r LEFT JOIN users u ON u.id = r.user_id WHERE r.ticket_id = ?' . ($withInternal ? '' : ' AND r.internal = 0') . ' ORDER BY r.id',
            [$ticketId]
        );
    }

    public function setStatus(int $id, string $status): void
    {
        if (!in_array($status, self::STATUSES, true)) {
            return;
        }
        $this->db->run(
            'UPDATE tickets SET status = ?, closed_at = IF(? = \'closed\', COALESCE(closed_at, UTC_TIMESTAMP()), NULL), updated_at = UTC_TIMESTAMP() WHERE id = ?',
            [$status, $status, $id]
        );
    }

    /** Removes a ticket and, with it, every message of it (the replies go through the foreign key). */
    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM tickets WHERE id = ?', [$id]);
    }

    public function setPriority(int $id, string $priority): void
    {
        if (in_array($priority, self::PRIORITIES, true)) {
            $this->db->run('UPDATE tickets SET priority = ?, updated_at = UTC_TIMESTAMP() WHERE id = ?', [$priority, $id]);
        }
    }

    public function setCategory(int $id, string $category): void
    {
        if (in_array($category, self::CATEGORIES, true)) {
            $this->db->run('UPDATE tickets SET category = ?, updated_at = UTC_TIMESTAMP() WHERE id = ?', [$category, $id]);
        }
    }

    public function setAssignee(int $id, ?int $userId): void
    {
        $this->db->run('UPDATE tickets SET assigned_to = ?, updated_at = UTC_TIMESTAMP() WHERE id = ?', [$userId, $id]);
    }

    /** The first answer a person gave: used for the response-time figure. */
    public function markFirstResponse(int $id): void
    {
        $this->db->run('UPDATE tickets SET first_response_at = COALESCE(first_response_at, UTC_TIMESTAMP()) WHERE id = ?', [$id]);
    }

    /** @return list<array<string, mixed>> */
    public function forUser(int $userId): array
    {
        return $this->db->all('SELECT id, ref, subject, status, priority, created_at, updated_at FROM tickets WHERE user_id = ? ORDER BY updated_at DESC, id DESC LIMIT 100', [$userId]);
    }

    /**
     * Filtered, paginated list for the staff.
     *
     * @param array{status?: string, priority?: string, category?: string, assignee?: string, q?: string, sort?: string} $f
     * @return array{rows: list<array<string, mixed>>, total: int, page: int, pages: int}
     */
    public function search(array $f, int $page): array
    {
        [$where, $params] = $this->conditions($f);
        $total = (int) $this->db->scalar('SELECT COUNT(*) FROM tickets t WHERE ' . $where, $params);
        $pages = max(1, (int) ceil($total / self::PER_PAGE));
        $page = max(1, min($page, $pages));
        $order = ($f['sort'] ?? '') === 'oldest'
            ? 't.updated_at ASC, t.id ASC'
            : "FIELD(t.priority, 'urgent', 'high', 'normal', 'low'), t.updated_at DESC, t.id DESC";
        $rows = $this->db->all(
            'SELECT t.id, t.ref, t.subject, t.name, t.email, t.status, t.priority, t.category, t.assigned_to, t.created_at, t.updated_at, a.name AS assignee_name,
                    (SELECT COUNT(*) FROM ticket_replies r WHERE r.ticket_id = t.id AND r.internal = 0) AS replies
             FROM tickets t LEFT JOIN users a ON a.id = t.assigned_to WHERE ' . $where . ' ORDER BY ' . $order . ' LIMIT ' . self::PER_PAGE . ' OFFSET ' . (($page - 1) * self::PER_PAGE),
            $params
        );
        return ['rows' => $rows, 'total' => $total, 'page' => $page, 'pages' => $pages];
    }

    /**
     * @param array<string, mixed> $f
     * @return array{0: string, 1: list<mixed>}
     */
    private function conditions(array $f): array
    {
        $where = ['1 = 1'];
        $params = [];
        $status = (string) ($f['status'] ?? 'active');
        if ($status === 'active') {
            $where[] = "t.status IN ('open', 'answered', 'on_hold')";
        } elseif (in_array($status, self::STATUSES, true)) {
            $where[] = 't.status = ?';
            $params[] = $status;
        }
        foreach (['priority' => self::PRIORITIES, 'category' => self::CATEGORIES] as $key => $allowed) {
            if (in_array((string) ($f[$key] ?? ''), $allowed, true)) {
                $where[] = "t.{$key} = ?";
                $params[] = $f[$key];
            }
        }
        $assignee = (string) ($f['assignee'] ?? '');
        if ($assignee === 'none') {
            $where[] = 't.assigned_to IS NULL';
        } elseif (ctype_digit($assignee) && $assignee !== '') {
            $where[] = 't.assigned_to = ?';
            $params[] = (int) $assignee;
        }
        $q = trim((string) ($f['q'] ?? ''));
        if ($q !== '') {
            $like = '%' . addcslashes(mb_substr($q, 0, 100), '%_\\') . '%';
            $where[] = '(t.ref = ? OR t.subject LIKE ? OR t.email LIKE ? OR t.name LIKE ?)';
            array_push($params, strtoupper($q), $like, $like, $like);
        }
        return [implode(' AND ', $where), $params];
    }

    /** @return array<string, int> tickets per status, plus 'active' */
    public function counts(): array
    {
        $out = array_fill_keys(self::STATUSES, 0);
        foreach ($this->db->all('SELECT status, COUNT(*) AS n FROM tickets GROUP BY status') as $r) {
            $out[(string) $r['status']] = (int) $r['n'];
        }
        $out['active'] = $out['open'] + $out['answered'] + $out['on_hold'];
        return $out;
    }

    /**
     * Figures for the overview: tickets waiting for staff, without an owner, and how fast the first answer came
     * over the last 30 days (null when no ticket was answered in that time).
     *
     * @return array{waiting: int, unassigned: int, first_response_seconds: ?int, answered_30d: int}
     */
    public function stats(): array
    {
        $row = $this->db->one(
            "SELECT AVG(TIMESTAMPDIFF(SECOND, created_at, first_response_at)) AS avg_s, COUNT(*) AS n FROM tickets WHERE first_response_at IS NOT NULL AND created_at >= UTC_TIMESTAMP() - INTERVAL 30 DAY"
        );
        return [
            'waiting' => (int) $this->db->scalar("SELECT COUNT(*) FROM tickets WHERE status = 'open'"),
            'unassigned' => (int) $this->db->scalar("SELECT COUNT(*) FROM tickets WHERE assigned_to IS NULL AND status IN ('open', 'answered', 'on_hold')"),
            'first_response_seconds' => $row !== null && $row['avg_s'] !== null ? (int) round((float) $row['avg_s']) : null,
            'answered_30d' => (int) ($row['n'] ?? 0),
        ];
    }

    /** @return list<array<string, mixed>> */
    public function staff(): array
    {
        return $this->db->all("SELECT id, name FROM users WHERE role = 'admin' AND is_active = 1 ORDER BY name");
    }

    /** Resolved tickets nobody wrote to for $days days are closed. @return int how many */
    public function autoClose(int $days): int
    {
        $ids = array_column($this->db->all("SELECT id FROM tickets WHERE status = 'resolved' AND updated_at < UTC_TIMESTAMP() - INTERVAL " . max(1, $days) . ' DAY'), 'id');
        foreach ($ids as $id) {
            $this->setStatus((int) $id, 'closed');
            $this->addReply((int) $id, null, 'system', 'Closed automatically: resolved for ' . max(1, $days) . ' days with no reply.', true);
        }
        return count($ids);
    }

    /** Deletes closed tickets (and their replies) that were closed more than $days days ago. */
    public function purgeClosed(int $days): int
    {
        return $this->db->run("DELETE FROM tickets WHERE status = 'closed' AND closed_at < UTC_TIMESTAMP() - INTERVAL " . max(1, $days) . ' DAY');
    }

    /** @return list<array<string, mixed>> */
    public function canned(): array
    {
        return $this->db->all('SELECT * FROM ticket_canned ORDER BY title');
    }

    public function addCanned(string $title, string $body): void
    {
        $this->db->run('INSERT INTO ticket_canned (title, body, created_at) VALUES (?, ?, UTC_TIMESTAMP())', [$title, $body]);
    }

    public function deleteCanned(int $id): void
    {
        $this->db->run('DELETE FROM ticket_canned WHERE id = ?', [$id]);
    }
}
