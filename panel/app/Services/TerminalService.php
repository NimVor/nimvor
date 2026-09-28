<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Security\PasswordHasher;
use WhaCP\Security\TwoFactor;
use WhaCP\Support\UserFacingException;

/**
 * Web terminal. The agent runs the shell (as the website account, in its
 * shell access mode and resource limits) and serves the WebSocket; the
 * panel decides who may open which account, after the user confirms its
 * password (and authenticator code) again, and hands the browser a
 * single-use ticket bound to its address.
 *
 * Every sign-in gets an owner token; signing out ends that sign-in's
 * terminals.
 */
final class TerminalService
{
    /** How long a password confirmation unlocks the terminal. */
    public const UNLOCK_SECONDS = 900;
    private const FAIL_LIMIT = 5;
    private const LOCK_SECONDS = 900;

    public function __construct(
        private readonly AgentClient $agent,
        private readonly SessionInterface $session,
        private readonly PasswordHasher $hasher,
        private readonly TwoFactor $twoFactor,
        private readonly \WhaCP\Database\Connection $db,
        private readonly AuditLogger $audit,
        private readonly SettingsService $settings,
    ) {
    }

    /** @return array<string, mixed> */
    public function status(): array
    {
        return (array) json_decode((string) json_encode($this->agent->call('terminal.status')), true);
    }

    public function idleMinutes(): int
    {
        return max(1, min(240, $this->settings->int('terminal_idle_minutes')));
    }

    public function unlocked(): bool
    {
        return (int) $this->session->get('terminal_unlocked_until', 0) > time();
    }

    public function needsCode(int $userId): bool
    {
        return $this->twoFactor->enabled($userId);
    }

    /**
     * Confirm the password (and authenticator code) again.
     *
     * @param array<string, mixed> $user
     */
    public function unlock(array $user, string $password, string $code): void
    {
        $lockedUntil = (int) $this->session->get('terminal_locked_until', 0);
        if ($lockedUntil > time()) {
            throw new UserFacingException('Too many wrong attempts. Try again in ' . (int) ceil(($lockedUntil - time()) / 60) . ' minute(s).', status: 429);
        }
        $hash = (string) $this->db->scalar('SELECT password_hash FROM users WHERE id = ?', [(int) $user['id']]);
        $ok = $password !== '' && $hash !== '' && $this->hasher->verify($password, $hash);
        if ($ok && $this->needsCode((int) $user['id'])) {
            $ok = $code !== '' && $this->twoFactor->verify((int) $user['id'], $code) !== null;
        }
        if (!$ok) {
            $fails = (int) $this->session->get('terminal_unlock_fails', 0) + 1;
            $this->session->set('terminal_unlock_fails', $fails);
            $this->audit->record('terminal.unlock', 'user', (string) $user['id'], AuditLogger::FAILURE, ['attempt' => $fails], (int) $user['id']);
            if ($fails >= self::FAIL_LIMIT) {
                $this->session->set('terminal_locked_until', time() + self::LOCK_SECONDS);
                $this->session->set('terminal_unlock_fails', 0);
            }
            throw new UserFacingException($this->needsCode((int) $user['id']) ? 'The password or the authenticator code is wrong.' : 'The password is wrong.', status: 403);
        }
        $this->session->set('terminal_unlock_fails', 0);
        $this->session->set('terminal_unlocked_until', time() + self::UNLOCK_SECONDS);
        $this->audit->record('terminal.unlock', 'user', (string) $user['id'], AuditLogger::SUCCESS, [], (int) $user['id']);
    }

    /** This sign-in's owner token (created on first use). */
    public function owner(): string
    {
        $o = (string) $this->session->get('terminal_owner', '');
        if (preg_match('/^[a-f0-9]{32}$/', $o) !== 1) {
            $o = bin2hex(random_bytes(16));
            $this->session->set('terminal_owner', $o);
        }
        return $o;
    }

    /**
     * A ticket for a website account's shell.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $user
     * @return array<string, mixed>
     */
    public function openForSite(array $site, array $user, string $clientIp, int $cols, int $rows): array
    {
        $account = (string) ($site['system_user'] ?? '');
        if ($account === '') {
            throw new UserFacingException('This website has no system account.');
        }
        if (!empty($site['account_suspended'])) {
            throw new UserFacingException('The hosting account is suspended.', status: 403);
        }
        return $this->open(['user' => $account], $user, $clientIp, $cols, $rows, 'website ' . $site['domain']);
    }

    /**
     * A ticket for a root shell (administrators, when the agent allows it).
     *
     * @param array<string, mixed> $user
     * @return array<string, mixed>
     */
    public function openRoot(array $user, string $clientIp, int $cols, int $rows): array
    {
        if (($user['role_name'] ?? '') !== 'admin') {
            throw new UserFacingException('Only administrators can open a root shell.', status: 403);
        }
        return $this->open(['root' => true], $user, $clientIp, $cols, $rows, 'root');
    }

    /**
     * @param array<string, mixed> $target
     * @param array<string, mixed> $user
     * @return array<string, mixed>
     */
    private function open(array $target, array $user, string $clientIp, int $cols, int $rows, string $what): array
    {
        if (!$this->unlocked()) {
            throw new UserFacingException('Confirm your password to open the terminal.', status: 428);
        }
        $label = preg_replace('/[^\pL\pN @._()#:-]/u', '', (string) $user['username'] . ' (#' . (int) $user['id'] . ')') ?? '';
        $res = (array) $this->agent->call('terminal.open', $target + [
            'cols' => $cols, 'rows' => $rows, 'client_ip' => filter_var($clientIp, FILTER_VALIDATE_IP) !== false ? $clientIp : '',
            'label' => mb_substr($label, 0, 100), 'owner' => $this->owner(),
            'idle_minutes' => $this->idleMinutes(),
            'max_minutes' => max(15, min(1440, $this->settings->int('terminal_max_minutes'))),
        ]);
        $this->audit->record('terminal.open', 'terminal', (string) ($res['session_id'] ?? ''), AuditLogger::SUCCESS,
            ['target' => $what, 'account' => $res['user'] ?? null, 'mode' => $res['mode'] ?? null, 'ip' => $clientIp], (int) $user['id']);
        return ['ticket' => (string) $res['ticket'], 'session_id' => (string) ($res['session_id'] ?? ''), 'mode' => (string) ($res['mode'] ?? ''), 'user' => (string) ($res['user'] ?? '')];
    }

    /** @param array<string, mixed> $actor */
    public function close(string $id, array $actor): int
    {
        if (preg_match('/^[a-f0-9]{16}$/', $id) !== 1) {
            throw new UserFacingException('Unknown session.');
        }
        $n = (int) (((array) $this->agent->call('terminal.kill', ['id' => $id]))['closed'] ?? 0);
        $this->audit->record('terminal.close', 'terminal', $id, AuditLogger::SUCCESS, [], (int) $actor['id']);
        return $n;
    }

    /** Sign-out: end this sign-in's terminals (best effort). */
    public function closeOwn(): void
    {
        $o = (string) $this->session->get('terminal_owner', '');
        if (preg_match('/^[a-f0-9]{32}$/', $o) !== 1) {
            return;
        }
        try {
            $this->agent->call('terminal.kill', ['owner' => $o], 10);
        } catch (\Throwable) {
            // The agent ends idle sessions on its own.
        }
    }
}
