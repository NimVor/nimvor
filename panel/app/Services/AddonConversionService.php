<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Repositories\UserRepository;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Converts an addon domain into a hosting account of its own: a new panel
 * login (a hosting customer with a plan) and a new system account that
 * runs the website. Nothing is copied: the website folder is re-owned in
 * place, its databases, DNS zone and email domain (with the mail data)
 * change owner, and its cron jobs, workers and FTP accounts move to the new
 * system account. Subdomains of the addon domain go with it. If a step on
 * the server fails, everything done so far is undone.
 */
final class AddonConversionService
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly AccountService $accounts,
        private readonly UserRepository $users,
        private readonly WebsiteRepository $sites,
        private readonly WebsiteService $websites,
        private readonly CronService $cron,
        private readonly WorkerService $workers,
        private readonly PlanLimits $limits,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
    ) {
    }

    /** @param array<string, mixed> $site */
    public static function isAddon(array $site, ?array $parent): bool
    {
        return $parent !== null && !str_ends_with((string) $site['domain'], '.' . $parent['domain']);
    }

    /**
     * What moves with the addon domain (for the confirmation).
     *
     * @param array<string, mixed> $site
     * @return array{websites: list<array<string, mixed>>, databases: list<string>, dns: bool, mail: bool, cron: int, workers: int}
     */
    public function preview(array $site): array
    {
        $ids = array_map(static fn (array $w): int => (int) $w['id'], $this->family($site));
        $in = implode(',', $ids);
        return [
            'websites' => $this->family($site),
            'databases' => array_map('strval', array_column($this->db->fetchAll("SELECT name FROM databases WHERE website_id IN ({$in}) ORDER BY name"), 'name')),
            'dns' => (int) $this->db->scalar("SELECT COUNT(*) FROM dns_zones WHERE website_id IN ({$in})") > 0,
            'mail' => (int) $this->db->scalar("SELECT COUNT(*) FROM mail_domains WHERE website_id IN ({$in})") > 0,
            'cron' => (int) $this->db->scalar("SELECT COUNT(*) FROM cron_jobs WHERE website_id IN ({$in})"),
            'workers' => (int) $this->db->scalar("SELECT COUNT(*) FROM workers WHERE website_id IN ({$in})"),
        ];
    }

    /**
     * The addon website and its own subdomains.
     *
     * @param array<string, mixed> $site
     * @return list<array<string, mixed>>
     */
    private function family(array $site): array
    {
        $out = [$site];
        foreach ($this->sites->children($this->server->id(), (int) $site['id']) as $c) {
            $out[] = $c;
        }
        return $out;
    }

    /**
     * @param array<string, mixed> $site the addon website (access-checked)
     * @param array<string, mixed> $input username, email, plan_id, password(+confirmation) or generate
     * @param array<string, mixed> $actor
     * @return array{id: int, username: string, password: string|null}
     */
    public function convert(array $site, array $input, array $actor): array
    {
        $parent = !empty($site['parent_id']) ? $this->sites->find($this->server->id(), (int) $site['parent_id'], null) : null;
        if (!self::isAddon($site, $parent)) {
            throw new UserFacingException('Only an addon domain can become an account of its own.');
        }
        if (!empty($site['account_suspended'])) {
            throw new UserFacingException('The hosting account is suspended; unsuspend it first.');
        }
        $username = strtolower(trim((string) ($input['username'] ?? '')));
        // The domain's folder becomes the new account's home: it must be the
        // standard <sites_root>/<domain> (what the agent re-owns).
        if (basename((string) $site['root']) !== $site['domain']) {
            throw new UserFacingException('The website folder is not the standard one for ' . $site['domain'] . '; it cannot be converted.');
        }
        if ($this->sites->usingSystemUser($this->server->id(), $username) !== []) {
            throw new ValidationException(['username' => 'The system user ' . $username . ' is in use. Choose another name.']);
        }
        // A reseller converts only for their own customers, into their own customers.
        $ownerIsVisible = $site['owner_id'] === null ? Scope::isAll($actor) : ($this->sites->find($this->server->id(), (int) $site['id'], $actor) !== null);
        if (!$ownerIsVisible) {
            throw new UserFacingException('That website does not exist.', status: 404);
        }

        // 1. The panel login (checks username, email, password and plan).
        $account = $this->accounts->create([
            'username' => $username, 'email' => $input['email'] ?? '', 'display_name' => $input['display_name'] ?? '',
            'password' => $input['password'] ?? '', 'password_confirmation' => $input['password_confirmation'] ?? '',
            'generate' => $input['generate'] ?? false, 'plan_id' => $input['plan_id'] ?? '', 'domain' => '',
        ] + AccountOptions::DEFAULTS, $actor);
        $newId = (int) $account['id'];
        $newUser = (array) $this->users->find($newId);
        $family = $this->family($site);
        $done = [];
        $ftp = [];
        try {
            $this->limits->assertCanCreate('websites', $newId);
            // 2. The system account, the folders re-owned, the files inside.
            foreach ($family as $i => $w) {
                $this->websites->moveToAccount($w, $username, $newId, $i === 0, $i === 0);
                $done[] = $w;
                // A folder of its own (<sites root>/<domain>): its files and FTP
                // accounts; one inside another website's folder went with it.
                if (basename((string) $w['root']) === $w['domain'] && ($i === 0 || !str_starts_with((string) $w['root'], rtrim((string) $site['root'], '/') . '/'))) {
                    $moved = $this->sites->find($this->server->id(), (int) $w['id'], null);
                    $this->websites->permissions((array) $moved, true);
                    $this->agent->call('ftp.move_site', ['domain' => $w['domain'], 'user' => $username], 120);
                    $ftp[] = $w;
                }
            }
            // 3. What runs as the account: cron jobs and workers.
            foreach ($family as $w) {
                $moved = (array) $this->sites->find($this->server->id(), (int) $w['id'], null);
                $this->cron->apply($moved);
                foreach ($this->workers->forSite($moved) as $worker) {
                    $this->workers->apply($moved, $worker);
                }
            }
        } catch (\Throwable $e) {
            $this->undo($done, $ftp, $newId);
            $this->audit->record('account.convert_addon', 'website', (string) $site['id'], AuditLogger::FAILURE, ['domain' => $site['domain'], 'username' => $username, 'error' => $e->getMessage()], (int) $actor['id']);
            throw $e;
        }
        // 4. Ownership in the panel: databases (and users only they use), DNS, email.
        $ids = implode(',', array_map(static fn (array $w): int => (int) $w['id'], $family));
        $this->db->transaction(function () use ($ids, $newId): void {
            $this->db->run("UPDATE database_users SET owner_id = ? WHERE id IN (SELECT g.user_id FROM database_grants g JOIN databases d ON d.id = g.database_id WHERE d.website_id IN ({$ids}))
                AND id NOT IN (SELECT g.user_id FROM database_grants g JOIN databases d ON d.id = g.database_id WHERE d.website_id IS NULL OR d.website_id NOT IN ({$ids}))", [$newId]);
            $this->db->run("UPDATE databases SET owner_id = ? WHERE website_id IN ({$ids})", [$newId]);
            $this->db->run("UPDATE dns_zones SET owner_id = ? WHERE website_id IN ({$ids})", [$newId]);
            $this->db->run("UPDATE mail_domains SET owner_id = ? WHERE website_id IN ({$ids})", [$newId]);
        });
        $this->audit->record('account.convert_addon', 'website', (string) $site['id'], AuditLogger::SUCCESS, [
            'domain' => $site['domain'], 'from' => $site['system_user'], 'username' => $username, 'user_id' => $newId,
            'websites' => array_column($family, 'domain'),
        ], (int) $actor['id']);
        return ['id' => $newId, 'username' => (string) $newUser['username'], 'password' => $account['password']];
    }

    /**
     * Puts the websites back on their previous account and removes the new login.
     *
     * @param list<array<string, mixed>> $done websites moved
     * @param list<array<string, mixed>> $ftp websites whose FTP accounts moved
     */
    private function undo(array $done, array $ftp, int $newId): void
    {
        foreach (array_reverse($done) as $w) {
            try {
                $this->websites->moveToAccount($w, (string) $w['system_user'], $w['owner_id'] === null ? null : (int) $w['owner_id'], false, false);
                $this->db->run('UPDATE websites SET parent_id = ? WHERE id = ?', [$w['parent_id'], (int) $w['id']]);
                if (in_array($w, $ftp, true)) {
                    $this->websites->permissions((array) $this->sites->find($this->server->id(), (int) $w['id'], null), true);
                    $this->agent->call('ftp.move_site', ['domain' => $w['domain'], 'user' => $w['system_user']], 120);
                }
                $this->cron->apply((array) $this->sites->find($this->server->id(), (int) $w['id'], null));
            } catch (\Throwable) {
                // Best effort; the failure itself is reported to the caller.
            }
        }
        $this->users->delete($newId);
    }
}
