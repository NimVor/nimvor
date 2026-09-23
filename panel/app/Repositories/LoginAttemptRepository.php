<?php

declare(strict_types=1);

namespace WhaCP\Repositories;

use WhaCP\Database\Connection;

final class LoginAttemptRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    public function record(string $username, string $ip, bool $succeeded, int $time): void
    {
        $this->db->run(
            'INSERT INTO login_attempts (username, ip, succeeded, attempted_at) VALUES (?, ?, ?, ?)',
            [mb_substr($username, 0, 190), $ip, $succeeded ? 1 : 0, $time]
        );
    }

    public function failuresForIp(string $ip, int $since): int
    {
        return (int) $this->db->scalar(
            'SELECT COUNT(*) FROM login_attempts WHERE ip = ? AND succeeded = 0 AND attempted_at >= ?',
            [$ip, $since]
        );
    }

    public function failuresForUser(string $username, int $since): int
    {
        return (int) $this->db->scalar(
            'SELECT COUNT(*) FROM login_attempts WHERE username = ? AND succeeded = 0 AND attempted_at >= ?',
            [$username, $since]
        );
    }

    public function oldestFailureForIp(string $ip, int $since): ?int
    {
        $v = $this->db->scalar('SELECT MIN(attempted_at) FROM login_attempts WHERE ip = ? AND succeeded = 0 AND attempted_at >= ?', [$ip, $since]);
        return $v === null ? null : (int) $v;
    }

    public function clearFailures(string $username, string $ip): void
    {
        $this->db->run('DELETE FROM login_attempts WHERE succeeded = 0 AND (username = ? OR ip = ?)', [$username, $ip]);
    }

    public function prune(int $before): void
    {
        $this->db->run('DELETE FROM login_attempts WHERE attempted_at < ?', [$before]);
    }
}
