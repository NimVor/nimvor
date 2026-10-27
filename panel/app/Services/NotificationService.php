<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Mail\MailException;
use WhaCP\Mail\Mailer;
use WhaCP\Support\Logger;
use WhaCP\Support\Redactor;

/**
 * Notifications shown in the panel: results of background jobs and
 * monitoring alerts.
 *
 * A notification belongs to a website, to a user (their resources, e.g. a
 * quota), or to nobody (server matters: administrators only). Who may see it
 * is decided when it is read, with the same scope rules as the rest of the
 * panel: "all"-scope roles see everything, resellers their own and their
 * customers', customers their own. Users who opted in are emailed about
 * failures.
 */
final class NotificationService
{
    public const RETENTION_DAYS = 90;
    public const PER_WEBSITE_LIMIT = 1000;
    public const SEVERITIES = ['success', 'info', 'warning', 'error'];

    public function __construct(
        private readonly Connection $db,
        private readonly ServerContext $server,
        private readonly SettingsService $settings,
        private readonly Logger $log,
        private readonly ?Mailer $mailer = null,
    ) {
    }

    /**
     * Record a notification once (by its dedupe key). Returns its id, or
     * null when it was already recorded.
     *
     * @param array{website_id?: int|null, owner_id?: int|null, body?: string, link?: string, job_id?: string, server_id?: int} $opts
     */
    public function notify(string $dedupeKey, string $kind, string $severity, string $title, array $opts = []): ?int
    {
        if (!in_array($severity, self::SEVERITIES, true)) {
            $severity = 'info';
        }
        $body = Redactor::redactString(mb_substr((string) ($opts['body'] ?? ''), 0, 1000));
        $inserted = $this->db->run(
            'INSERT OR IGNORE INTO notifications (server_id, website_id, owner_id, kind, severity, title, body, link, job_id, dedupe_key, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [
                (int) ($opts['server_id'] ?? $this->server->id()), $opts['website_id'] ?? null, $opts['owner_id'] ?? null,
                $kind, $severity, Redactor::redactString(mb_substr($title, 0, 200)), $body,
                (string) ($opts['link'] ?? ''), (string) ($opts['job_id'] ?? ''), $dedupeKey, gmdate('c'),
            ]
        )->rowCount();
        if ($inserted === 0) {
            return null;
        }
        $id = $this->db->lastInsertId();
        if ($severity === 'error' || $severity === 'warning') {
            $this->email($id);
        }
        return $id;
    }

    /**
     * SQL condition (on alias n) selecting what the user may see.
     *
     * @param array<string, mixed> $user
     * @return array{0: string, 1: array<string, mixed>}
     */
    public function visibility(array $user): array
    {
        if (Scope::isAll($user)) {
            return ['1 = 1', []];
        }
        [$sites, $p] = Scope::condition($user, 'w.owner_id');
        [$owners, $p2] = Scope::condition($user, 'n.owner_id');
        return [
            "((n.website_id IS NOT NULL AND n.website_id IN (SELECT w.id FROM websites w WHERE {$sites}))"
            . " OR (n.website_id IS NULL AND n.owner_id IS NOT NULL AND {$owners}))",
            $p + $p2,
        ];
    }

    /**
     * @param array<string, mixed> $user
     * @param array{unread?: bool, severity?: string, website_id?: int, server_id?: int} $filter
     * @return list<array<string, mixed>>
     */
    public function list(array $user, array $filter = [], int $limit = 50, int $offset = 0): array
    {
        [$where, $params] = $this->filtered($user, $filter);
        $params['uid'] = (int) $user['id'];
        return $this->db->fetchAll(
            "SELECT n.*, w.domain, s.name AS server_name, (r.read_at IS NOT NULL) AS is_read
             FROM notifications n
             LEFT JOIN websites w ON w.id = n.website_id
             LEFT JOIN servers s ON s.id = n.server_id
             LEFT JOIN notification_reads r ON r.notification_id = n.id AND r.user_id = :uid
             WHERE {$where} ORDER BY n.id DESC LIMIT " . max(1, min(200, $limit)) . ' OFFSET ' . max(0, $offset),
            $params
        );
    }

    /**
     * @param array<string, mixed> $user
     * @param array{unread?: bool, severity?: string, website_id?: int, server_id?: int} $filter
     */
    public function count(array $user, array $filter = []): int
    {
        [$where, $params] = $this->filtered($user, $filter);
        $params['uid'] = (int) $user['id'];
        return (int) $this->db->scalar(
            "SELECT COUNT(*) FROM notifications n LEFT JOIN notification_reads r ON r.notification_id = n.id AND r.user_id = :uid WHERE {$where}",
            $params
        );
    }

    /** @param array<string, mixed> $user */
    public function unreadCount(array $user): int
    {
        return $this->count($user, ['unread' => true]);
    }

    /**
     * Mark notifications read: the given ids, or all the user can see.
     * Ids the user may not see are ignored.
     *
     * @param array<string, mixed> $user
     * @param list<int>|null $ids
     */
    public function markRead(array $user, ?array $ids): int
    {
        [$where, $params] = $this->visibility($user);
        if ($ids !== null) {
            $ids = array_values(array_unique(array_map('intval', $ids)));
            if ($ids === []) {
                return 0;
            }
            $in = [];
            foreach ($ids as $i => $id) {
                $in[] = ':id' . $i;
                $params['id' . $i] = $id;
            }
            $where .= ' AND n.id IN (' . implode(',', $in) . ')';
        }
        $params['uid'] = (int) $user['id'];
        $params['now'] = gmdate('c');
        return $this->db->run(
            "INSERT OR IGNORE INTO notification_reads (notification_id, user_id, read_at) SELECT n.id, :uid, :now FROM notifications n WHERE {$where}",
            $params
        )->rowCount();
    }

    /** @param array<string, mixed> $user */
    public function find(array $user, int $id): ?array
    {
        [$where, $params] = $this->visibility($user);
        $params['nid'] = $id;
        return $this->db->fetch("SELECT n.* FROM notifications n WHERE n.id = :nid AND {$where}", $params);
    }

    /** Delete old notifications and keep at most PER_WEBSITE_LIMIT per website. */
    public function prune(?int $now = null): int
    {
        $now ??= time();
        $n = $this->db->run('DELETE FROM notifications WHERE created_at < ?', [gmdate('c', $now - self::RETENTION_DAYS * 86400)])->rowCount();
        $n += $this->db->run(
            'DELETE FROM notifications WHERE website_id IS NOT NULL AND id NOT IN (
                SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY website_id ORDER BY id DESC) AS rn FROM notifications WHERE website_id IS NOT NULL) WHERE rn <= ?
            )',
            [self::PER_WEBSITE_LIMIT]
        )->rowCount();
        return $n;
    }

    /**
     * @param array<string, mixed> $user
     * @param array{unread?: bool, severity?: string, website_id?: int, server_id?: int} $filter
     * @return array{0: string, 1: array<string, mixed>}
     */
    private function filtered(array $user, array $filter): array
    {
        [$where, $params] = $this->visibility($user);
        if (!empty($filter['unread'])) {
            $where .= ' AND r.read_at IS NULL';
        }
        if (isset($filter['severity']) && in_array($filter['severity'], self::SEVERITIES, true)) {
            $where .= ' AND n.severity = :sev';
            $params['sev'] = $filter['severity'];
        }
        if (!empty($filter['website_id'])) {
            $where .= ' AND n.website_id = :wid';
            $params['wid'] = (int) $filter['website_id'];
        }
        if (!empty($filter['server_id'])) {
            $where .= ' AND n.server_id = :srv';
            $params['srv'] = (int) $filter['server_id'];
        }
        return [$where, $params];
    }

    /** Email a new failure to every active user who opted in and may see it. */
    private function email(int $id): void
    {
        if ($this->mailer === null || !$this->mailer->configured()) {
            return;
        }
        $n = $this->db->fetch('SELECT n.*, w.domain FROM notifications n LEFT JOIN websites w ON w.id = n.website_id WHERE n.id = ?', [$id]);
        if ($n === null) {
            return;
        }
        $candidates = $this->db->fetchAll(
            "SELECT u.id, u.email, r.scope AS role_scope FROM users u JOIN roles r ON r.id = u.role_id WHERE u.notify_email = 1 AND u.is_active = 1 AND u.email <> ''"
        );
        $to = [];
        foreach ($candidates as $u) {
            if ($this->find($u, $id) !== null) {
                $to[] = (string) $u['email'];
            }
        }
        if ($to === []) {
            return;
        }
        $base = rtrim($this->settings->string('panel_url'), '/');
        $text = $n['title'] . "\n\n" . ($n['body'] !== '' ? $n['body'] . "\n\n" : '')
            . ($base !== '' ? "Details: {$base}/notifications\n\n" : '')
            . "-- \nWhaCP. You receive this because \"Email me about failures\" is on in your profile.";
        foreach ($to as $address) {
            // One message per recipient: addresses are not disclosed to each other.
            try {
                $this->mailer->send([$address], '[NimVor] ' . mb_substr((string) $n['title'], 0, 120), $text);
            } catch (MailException $e) {
                $this->log->warning('notification email failed', ['notification' => $id]);
            }
        }
    }
}
