<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Support\ValidationException;

/**
 * Personal API tokens. Only a SHA-256 hash is stored; the token is shown
 * once when created. A token acts as its user (same permissions and
 * ownership scope); "read" tokens may only make GET requests.
 */
final class ApiTokenService
{
    public const RATE_PER_MINUTE = 120;

    public function __construct(
        private readonly Connection $db,
        private readonly UserRepository $users,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return list<array<string, mixed>> */
    public function forUser(int $userId): array
    {
        return $this->db->fetchAll('SELECT id, name, prefix, ability, expires_at, last_used_at, last_used_ip, created_at FROM api_tokens WHERE user_id = ? ORDER BY id DESC', [$userId]);
    }

    /**
     * @param array<string, mixed> $input
     * @return string the new token (shown once)
     */
    public function create(int $userId, array $input): string
    {
        $errors = [];
        $name = trim((string) ($input['name'] ?? ''));
        if ($name === '' || mb_strlen($name) > 60) {
            $errors['name'] = 'Name the token (e.g. the tool that uses it).';
        }
        $ability = (string) ($input['ability'] ?? 'read');
        if (!in_array($ability, ['read', 'write'], true)) {
            $errors['ability'] = 'Choose read-only or read and write.';
        }
        $days = (int) ($input['expires_days'] ?? 90);
        if ($days < 0 || $days > 3650) {
            $errors['expires_days'] = 'Choose 0 (never) to 3650 days.';
        }
        if (count($this->forUser($userId)) >= 20) {
            $errors['name'] = 'You have 20 tokens; revoke one first.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $token = 'whacp_' . bin2hex(random_bytes(20));
        $this->db->run(
            'INSERT INTO api_tokens (user_id, name, token_hash, prefix, ability, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)',
            [$userId, $name, hash('sha256', $token), substr($token, 0, 12), $ability, $days > 0 ? gmdate('c', time() + $days * 86400) : null, gmdate('c')]
        );
        $this->audit->record('api.token_create', 'user', (string) $userId, AuditLogger::SUCCESS, ['name' => $name, 'ability' => $ability, 'expires_days' => $days]);
        return $token;
    }

    public function revoke(int $userId, int $tokenId): void
    {
        $this->db->run('DELETE FROM api_tokens WHERE id = ? AND user_id = ?', [$tokenId, $userId]);
        $this->audit->record('api.token_revoke', 'user', (string) $userId, AuditLogger::SUCCESS, ['token' => $tokenId]);
    }

    /**
     * Resolve a bearer token to its (active) user and token record.
     *
     * @return array{user: array<string, mixed>, token: array<string, mixed>}|null
     */
    public function authenticate(string $token, string $ip): ?array
    {
        if (!preg_match('/^whacp_[a-f0-9]{40}$/', $token)) {
            return null;
        }
        $row = $this->db->fetch('SELECT * FROM api_tokens WHERE token_hash = ?', [hash('sha256', $token)]);
        if ($row === null || ($row['expires_at'] !== null && strtotime((string) $row['expires_at']) < time())) {
            return null;
        }
        $user = $this->users->find((int) $row['user_id']);
        if (!\WhaCP\Repositories\UserRepository::canAct($user)) {
            return null;
        }
        if ($row['last_used_at'] === null || strtotime((string) $row['last_used_at']) < time() - 60) {
            $this->db->run('UPDATE api_tokens SET last_used_at = ?, last_used_ip = ? WHERE id = ?', [gmdate('c'), $ip, (int) $row['id']]);
        }
        return ['user' => $user, 'token' => $row];
    }

    /** Count a request; false when the token exceeded its per-minute budget. */
    public function hit(int $tokenId, ?int $now = null): bool
    {
        $window = intdiv($now ?? time(), 60);
        $this->db->run('INSERT INTO api_rate (token_id, window, hits) VALUES (?, ?, 1) ON CONFLICT (token_id, window) DO UPDATE SET hits = hits + 1', [$tokenId, $window]);
        $hits = (int) $this->db->scalar('SELECT hits FROM api_rate WHERE token_id = ? AND window = ?', [$tokenId, $window]);
        if (random_int(1, 50) === 1) {
            $this->db->run('DELETE FROM api_rate WHERE window < ?', [$window - 5]);
        }
        return $hits <= self::RATE_PER_MINUTE;
    }
}
