<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Database\Connection;
use WhaCP\Support\UserFacingException;

/**
 * Shell access of website accounts (administrators only): disabled (SFTP
 * only, the default), jailed (a sandbox with only the account's website
 * writable) or normal (a shell on the host). A mode takes effect while
 * the account's SSH/SFTP login is on.
 */
final class ShellService
{
    public const MODES = ['disabled', 'jailed', 'normal'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly Connection $db,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
        private readonly FeatureService $features,
        private readonly \WhaCP\Repositories\UserRepository $users,
    ) {
    }

    /**
     * The website accounts with their mode, websites and owner.
     *
     * @return array{accounts: list<array<string, mixed>>, jail_problem: string}
     */
    public function status(): array
    {
        $res = (array) $this->agent->call('shell.status');
        $sites = [];
        foreach ($this->db->fetchAll('SELECT w.system_user, w.domain, u.username AS owner FROM websites w LEFT JOIN users u ON u.id = w.owner_id WHERE w.server_id = ? ORDER BY w.domain', [$this->server->id()]) as $w) {
            $sites[$w['system_user']]['domains'][] = $w['domain'];
            $sites[$w['system_user']]['owner'] = $w['owner'];
        }
        $accounts = [];
        foreach ((array) ($res['accounts'] ?? []) as $a) {
            $a = (array) $a;
            $name = (string) $a['user'];
            $accounts[] = $a + ['domains' => $sites[$name]['domains'] ?? [], 'owner' => $sites[$name]['owner'] ?? null];
        }
        return ['accounts' => $accounts, 'jail_problem' => (string) ($res['jail_problem'] ?? '')];
    }

    /**
     * @param list<string> $users
     * @param array<string, mixed> $actor
     * @return list<array<string, mixed>>
     */
    public function set(array $users, string $mode, array $actor): array
    {
        if (($actor['role_name'] ?? '') !== 'admin') {
            throw new UserFacingException('Only administrators can change shell access.', status: 403);
        }
        if (!in_array($mode, self::MODES, true)) {
            throw new UserFacingException('Choose disabled, jailed or normal.');
        }
        $users = array_values(array_unique(array_filter(array_map('strval', $users), static fn (string $u): bool => preg_match('/^[a-z_][a-z0-9_.-]{0,31}$/', $u) === 1)));
        if ($users === []) {
            throw new UserFacingException('Select at least one account.');
        }
        // A shell only for accounts whose plan includes SSH.
        $skipped = [];
        if ($mode !== 'disabled') {
            foreach ($users as $i => $u) {
                $owner = $this->db->scalar('SELECT owner_id FROM websites WHERE server_id = ? AND system_user = ? AND owner_id IS NOT NULL LIMIT 1', [$this->server->id(), $u]);
                $account = $owner !== null ? $this->users->find((int) $owner) : null;
                if ($account !== null && in_array('ssh', $this->features->disabledFor($account), true)) {
                    $skipped[] = $u;
                    unset($users[$i]);
                }
            }
            $users = array_values($users);
            if ($users === []) {
                throw new UserFacingException('The hosting plan of ' . implode(', ', $skipped) . ' does not include SSH (Feature Manager).');
            }
        }
        $res = (array) $this->agent->call('shell.set', ['users' => $users, 'mode' => $mode], 120);
        $this->audit->record('shell.set', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['mode' => $mode, 'users' => $users], (int) $actor['id']);
        $out = array_map(static fn ($a): array => (array) $a, (array) ($res['accounts'] ?? []));
        foreach ($skipped as $u) {
            $out[] = ['user' => $u, 'mode' => 'disabled', 'effective' => 'disabled', 'skipped' => true];
        }
        return $out;
    }

    /**
     * Scheduler pass: take shells away from accounts whose plan no longer
     * includes SSH.
     *
     * @return list<string> log lines
     */
    public function reconcile(): array
    {
        $status = (array) $this->agent->call('shell.status');
        $revoke = [];
        foreach ((array) ($status['accounts'] ?? []) as $a) {
            $a = (array) $a;
            if (($a['mode'] ?? 'disabled') === 'disabled') {
                continue;
            }
            $owner = $this->db->scalar('SELECT owner_id FROM websites WHERE server_id = ? AND system_user = ? AND owner_id IS NOT NULL LIMIT 1', [$this->server->id(), (string) $a['user']]);
            $account = $owner !== null ? $this->users->find((int) $owner) : null;
            if ($account !== null && in_array('ssh', $this->features->disabledFor($account), true)) {
                $revoke[] = (string) $a['user'];
            }
        }
        if ($revoke === []) {
            return [];
        }
        $this->agent->call('shell.set', ['users' => $revoke, 'mode' => 'disabled'], 120);
        $this->audit->record('shell.set', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['mode' => 'disabled', 'users' => $revoke, 'reason' => 'plan without SSH']);
        return ['Shell access removed (plan without SSH): ' . implode(', ', $revoke) . '.'];
    }
}
