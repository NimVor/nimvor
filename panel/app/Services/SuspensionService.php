<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Hosting\Scope;
use NimVor\Repositories\UserRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Support\UserFacingException;

/**
 * Hosting account suspension. A suspended account (and every customer of a
 * suspended reseller) cannot sign in or use API tokens, and on each server:
 * its websites are off (their own on/off setting is kept), cron jobs and
 * workers are stopped, mailbox logins are refused (mail is still
 * delivered), database users are locked, and the website accounts expire,
 * which refuses SSH/SFTP and locks their FTP accounts. Nothing is deleted;
 * resuming turns everything back on. Only administrators suspend.
 */
final class SuspensionService
{
    public function __construct(
        private readonly Connection $db,
        private readonly UserRepository $users,
        private readonly WebsiteRepository $sites,
        private readonly WebsiteService $websites,
        private readonly CronService $cron,
        private readonly WorkerService $workers,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * @param array<string, mixed> $actor
     * @return array<string, mixed> the account
     */
    private function target(int $id, array $actor): array
    {
        if (!Scope::isAll($actor)) {
            throw new UserFacingException('Only administrators can suspend accounts.');
        }
        $target = $this->users->find($id);
        if ($target === null) {
            throw new UserFacingException('The account no longer exists.');
        }
        if (($target['role_scope'] ?? 'all') !== 'own') {
            throw new UserFacingException('Only hosting accounts (customers and resellers) can be suspended. Turn off staff accounts with "Active" instead.');
        }
        return $target;
    }

    /**
     * @param array<string, mixed> $actor
     * @return list<string> warnings (parts that could not be applied on a server)
     */
    public function suspend(int $id, string $reason, array $actor): array
    {
        $target = $this->target($id, $actor);
        $reason = mb_substr(trim(preg_replace('/\s+/', ' ', $reason) ?? ''), 0, 250);
        $this->db->run('UPDATE users SET suspended_at = COALESCE(suspended_at, ?), suspend_reason = ? WHERE id = ?', [gmdate('c'), $reason, $id]);
        $warnings = $this->enforce($this->affected($id));
        $this->audit->record('account.suspend', 'user', (string) $id, $warnings === [] ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['username' => $target['username'], 'reason' => $reason, 'warnings' => $warnings]);
        return $warnings;
    }

    /**
     * @param array<string, mixed> $actor
     * @return list<string> warnings
     */
    public function unsuspend(int $id, array $actor): array
    {
        $target = $this->target($id, $actor);
        $this->db->run("UPDATE users SET suspended_at = NULL, suspend_reason = '' WHERE id = ?", [$id]);
        $warnings = $this->enforce($this->affected($id));
        $this->audit->record('account.unsuspend', 'user', (string) $id, $warnings === [] ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['username' => $target['username'], 'warnings' => $warnings]);
        return $warnings;
    }

    /** @return list<int> the account and, for a reseller, its customers */
    private function affected(int $id): array
    {
        return array_merge([$id], array_map('intval', array_column($this->db->fetchAll('SELECT id FROM users WHERE parent_id = ?', [$id]), 'id')));
    }

    public function isSuspended(int $userId): bool
    {
        $u = $this->users->find($userId);
        return $u !== null && !empty($u['suspended']);
    }

    /**
     * Bring the servers in line with the accounts' suspension state
     * (idempotent: also repairs a partly applied suspension).
     *
     * @param list<int> $userIds
     * @return list<string> warnings
     */
    public function enforce(array $userIds): array
    {
        $warnings = [];
        $before = $this->server->forced();
        try {
            foreach ($userIds as $uid) {
                $suspended = $this->isSuspended($uid);
                foreach ($this->db->fetchAll('SELECT id, server_id FROM websites WHERE owner_id = ? ORDER BY id', [$uid]) as $row) {
                    $this->server->force((int) $row['server_id']);
                    $site = $this->sites->find((int) $row['server_id'], (int) $row['id'], null);
                    if ($site === null) {
                        continue;
                    }
                    $warnings = array_merge($warnings, $this->website($site, $suspended));
                }
                foreach ($this->db->fetchAll('SELECT DISTINCT system_user, server_id FROM websites WHERE owner_id = ?', [$uid]) as $row) {
                    $this->server->force((int) $row['server_id']);
                    $this->try($warnings, 'SSH/FTP for ' . $row['system_user'], fn () => $this->agent->call('account.suspend', ['user' => (string) $row['system_user'], 'suspended' => $suspended]));
                }
                foreach ($this->db->fetchAll('SELECT domain, server_id FROM mail_domains WHERE owner_id = ?', [$uid]) as $row) {
                    $this->server->force((int) $row['server_id']);
                    $this->try($warnings, 'email for ' . $row['domain'], fn () => $this->agent->call('mail.suspend', ['domain' => (string) $row['domain'], 'suspended' => $suspended]));
                }
                foreach ($this->db->fetchAll('SELECT username, host, server_id FROM database_users WHERE owner_id = ?', [$uid]) as $row) {
                    $this->server->force((int) $row['server_id']);
                    $this->try($warnings, 'database user ' . $row['username'], fn () => $this->agent->call('db.user_lock', ['user' => (string) $row['username'], 'host' => (string) $row['host'], 'locked' => $suspended]));
                }
            }
        } finally {
            $this->server->force($before);
        }
        return $warnings;
    }

    /**
     * @param array<string, mixed> $site
     * @return list<string>
     */
    private function website(array $site, bool $suspended): array
    {
        $warnings = [];
        $site['account_suspended'] = $suspended;
        $this->try($warnings, 'website ' . $site['domain'], fn () => $this->websites->applyState($site));
        $this->try($warnings, 'cron jobs of ' . $site['domain'], fn () => $this->cron->apply($site));
        foreach ($this->workers->forSite($site) as $w) {
            $this->try($warnings, 'worker ' . $w['name'], fn () => $this->workers->apply($site, $w));
        }
        return $warnings;
    }

    /** @param list<string> $warnings */
    private function try(array &$warnings, string $what, callable $fn): void
    {
        try {
            $fn();
        } catch (AgentException $e) {
            if (in_array($e->errorCode(), ['not_found', 'invalid_params'], true) && str_starts_with($what, 'SSH/FTP')) {
                // Not a NimVor website account (e.g. an imported website run by
                // www-data): it has no NimVor SSH/SFTP or FTP access to turn off,
                // and a shared system account must not be expired.
                return;
            }
            $warnings[] = $what . ': ' . $e->getMessage();
        } catch (UserFacingException $e) {
            $warnings[] = $what . ': ' . $e->getMessage();
        }
    }
}
