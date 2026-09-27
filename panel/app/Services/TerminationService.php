<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Repositories\DatabaseRepository;
use WhaCP\Repositories\UserRepository;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Terminating hosting accounts (administrators, several at a time): each
 * account's websites (files and system account), email, databases and DNS
 * zones are removed through the same code paths as removing them one by
 * one, then the account itself. Accounts are processed one after another;
 * an account whose removal fails part-way is kept (with what is left) and
 * reported, and the others continue.
 */
final class TerminationService
{
    public const CONFIRM_WORD = 'TERMINATE';

    public function __construct(
        private readonly Connection $db,
        private readonly UserRepository $users,
        private readonly WebsiteRepository $sites,
        private readonly WebsiteService $websites,
        private readonly MailService $mail,
        private readonly DatabaseService $databases,
        private readonly DatabaseRepository $dbRepo,
        private readonly DnsService $dns,
        private readonly UserService $userService,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * What terminating the accounts would remove (for the confirmation).
     *
     * @param list<int> $ids
     * @param array<string, mixed> $actor
     * @return list<array<string, mixed>>
     */
    public function preview(array $ids, array $actor): array
    {
        $this->assertAdmin($actor);
        $out = [];
        foreach (array_values(array_unique(array_map('intval', $ids))) as $id) {
            $u = $this->users->find($id);
            if ($u === null) {
                continue;
            }
            $out[] = [
                'id' => $id, 'username' => $u['username'], 'role' => $u['role_name'], 'scope' => $u['role_scope'],
                'websites' => array_column($this->db->fetchAll('SELECT domain FROM websites WHERE owner_id = ? ORDER BY domain', [$id]), 'domain'),
                'mail_domains' => (int) $this->db->scalar('SELECT COUNT(*) FROM mail_domains WHERE owner_id = ?', [$id]),
                'mailboxes' => (int) $this->db->scalar('SELECT COUNT(*) FROM mail_mailboxes m JOIN mail_domains d ON d.id = m.domain_id WHERE d.owner_id = ?', [$id]),
                'databases' => (int) $this->db->scalar('SELECT COUNT(*) FROM databases WHERE owner_id = ?', [$id]),
                'zones' => (int) $this->db->scalar('SELECT COUNT(*) FROM dns_zones WHERE owner_id = ?', [$id]),
                'customers' => array_map('intval', array_column($this->db->fetchAll('SELECT id FROM users WHERE parent_id = ?', [$id]), 'id')),
            ];
        }
        return $out;
    }

    /** @param array<string, mixed> $actor */
    private function assertAdmin(array $actor): void
    {
        if (!Scope::isAll($actor)) {
            throw new UserFacingException('Only administrators can terminate accounts.', status: 403);
        }
    }

    /**
     * @param list<int> $ids
     * @param array<string, mixed> $actor
     * @return list<array{username: string, ok: bool, message: string}>
     */
    public function terminate(array $ids, string $confirm, int $expected, array $actor): array
    {
        $this->assertAdmin($actor);
        $accounts = $this->preview($ids, $actor);
        if ($confirm !== self::CONFIRM_WORD || $expected !== count($accounts) || $accounts === []) {
            throw new UserFacingException('Type ' . self::CONFIRM_WORD . ' to confirm terminating the selected accounts.');
        }
        $selected = array_column($accounts, 'id');
        // Customers before their reseller.
        usort($accounts, static fn (array $a, array $b): int => ($a['customers'] !== []) <=> ($b['customers'] !== []));
        $results = [];
        foreach ($accounts as $a) {
            $label = (string) $a['username'];
            if ($a['scope'] !== 'own') {
                $results[] = ['username' => $label, 'ok' => false, 'message' => 'Not a hosting account (staff accounts are removed under Users).'];
                continue;
            }
            $left = array_diff($a['customers'], $selected);
            if ($left !== []) {
                $results[] = ['username' => $label, 'ok' => false, 'message' => 'The reseller still has ' . count($left) . ' customer(s) that were not selected. Terminate or move them first.'];
                continue;
            }
            try {
                $this->one((int) $a['id'], $actor);
                $results[] = ['username' => $label, 'ok' => true, 'message' => 'Terminated: ' . count($a['websites']) . ' website(s), ' . $a['mailboxes'] . ' mailbox(es), ' . $a['databases'] . ' database(s), ' . $a['zones'] . ' DNS zone(s).'];
                $this->audit->record('account.terminate', 'user', (string) $a['id'], AuditLogger::SUCCESS, ['username' => $label, 'websites' => $a['websites']], (int) $actor['id']);
            } catch (UserFacingException | ValidationException $e) {
                $msg = $e instanceof ValidationException ? implode(' ', $e->errors()) : $e->getMessage();
                $results[] = ['username' => $label, 'ok' => false, 'message' => 'Stopped: ' . $msg . ' The account and what was not yet removed are kept; terminate it again to continue.'];
                $this->audit->record('account.terminate', 'user', (string) $a['id'], AuditLogger::FAILURE, ['username' => $label, 'error' => $msg], (int) $actor['id']);
            }
        }
        return $results;
    }

    /** @param array<string, mixed> $actor */
    private function one(int $id, array $actor): void
    {
        $before = $this->server->forced();
        try {
            // Subdomains and addon domains before their parent website.
            foreach ($this->db->fetchAll('SELECT id, server_id FROM websites WHERE owner_id = ? ORDER BY parent_id IS NULL, id', [$id]) as $row) {
                $this->server->force((int) $row['server_id']);
                $site = $this->sites->find((int) $row['server_id'], (int) $row['id'], null);
                if ($site !== null) {
                    $this->websites->delete($site, true, true);
                }
            }
            foreach ($this->db->fetchAll('SELECT * FROM mail_domains WHERE owner_id = ?', [$id]) as $domain) {
                $this->server->force((int) $domain['server_id']);
                foreach ($this->mail->aliases((int) $domain['id']) as $a) {
                    $this->mail->deleteAlias($domain, (int) $a['id']);
                }
                foreach ($this->mail->mailboxes((int) $domain['id']) as $m) {
                    $this->mail->deleteMailbox($domain, (int) $m['id'], $m['local'] . '@' . $domain['domain']);
                }
                $this->mail->removeDomain($domain, (string) $domain['domain']);
            }
            foreach ($this->db->fetchAll('SELECT id, server_id FROM databases WHERE owner_id = ?', [$id]) as $row) {
                $this->server->force((int) $row['server_id']);
                $database = $this->dbRepo->database((int) $row['server_id'], (int) $row['id'], null);
                if ($database !== null) {
                    $this->databases->drop($database, (string) $database['name']);
                }
            }
            foreach ($this->db->fetchAll('SELECT id, server_id FROM database_users WHERE owner_id = ?', [$id]) as $row) {
                $this->server->force((int) $row['server_id']);
                $dbUser = $this->dbRepo->user((int) $row['server_id'], (int) $row['id'], null);
                if ($dbUser !== null) {
                    $this->databases->dropUser($dbUser);
                }
            }
            foreach ($this->db->fetchAll('SELECT * FROM dns_zones WHERE owner_id = ?', [$id]) as $zone) {
                $this->server->force((int) $zone['server_id']);
                $this->dns->delete($zone, (string) $zone['name']);
            }
        } finally {
            $this->server->force($before);
        }
        $this->userService->delete($id, (int) $actor['id']);
    }
}
