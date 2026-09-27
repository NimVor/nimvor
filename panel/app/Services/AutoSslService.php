<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Support\DnsLookup;
use WhaCP\Support\UserFacingException;

/**
 * Automatic SSL (like AutoSSL): a new website waits until its domain points
 * to this server, then gets a Let's Encrypt certificate and HTTPS.
 *
 * The domain points here when its name servers are all this panel's, or
 * when its address (A record) is this server's. The scheduler checks every
 * few minutes; a website still not pointing here after 3 days, or refused
 * by Let's Encrypt 6 times, is left for the SSL page.
 */
final class AutoSslService
{
    public const WAIT_DAYS = 3;
    public const MAX_TRIES = 6;
    private const PER_PASS = 5;

    public function __construct(
        private readonly Connection $db,
        private readonly WebsiteRepository $sites,
        private readonly SslService $ssl,
        private readonly DnsService $dns,
        private readonly SiteOverview $overview,
        private readonly SettingsService $settings,
        private readonly ServerContext $server,
        private readonly DnsLookup $lookup,
    ) {
    }

    /** @param array<string, mixed> $site a new website */
    public function mark(array $site): void
    {
        if (!$this->settings->bool('auto_ssl') || $site['https'] || !$site['enabled']) {
            return;
        }
        $this->note((int) $site['id'], 'pending', 0, gmdate('c'), 'Waiting for the domain to point to this server.');
    }

    private function note(int $id, string $state, int $tries, ?string $next, string $note): void
    {
        $this->db->run('UPDATE websites SET auto_ssl = ?, auto_ssl_tries = ?, auto_ssl_next = ?, auto_ssl_note = ? WHERE id = ?', [$state, $tries, $next, $note, $id]);
    }

    /** @return list<string> what happened, for the scheduler's output */
    public function run(): array
    {
        $now = gmdate('c');
        $due = $this->db->fetchAll(
            "SELECT id FROM websites WHERE server_id = ? AND auto_ssl = 'pending' AND (auto_ssl_next IS NULL OR auto_ssl_next <= ?) ORDER BY auto_ssl_next LIMIT " . self::PER_PASS,
            [$this->server->id(), $now]
        );
        $out = [];
        $ip = null;
        foreach ($due as $row) {
            $site = $this->sites->find($this->server->id(), (int) $row['id'], null);
            if ($site === null) {
                continue;
            }
            $id = (int) $site['id'];
            if ($site['https'] || !$site['enabled']) {
                $this->note($id, '', 0, null, $site['https'] ? 'HTTPS was set up.' : '');
                continue;
            }
            $ip ??= $this->overview->serverIp();
            if (!$this->pointsHere((string) $site['domain'], $ip)) {
                if (strtotime((string) $site['created_at']) < time() - self::WAIT_DAYS * 86400) {
                    $this->note($id, 'gave_up', (int) $site['auto_ssl_tries'], null, 'The domain did not point to this server within ' . self::WAIT_DAYS . ' days. Issue the certificate on this page once it does.');
                    $out[] = "auto SSL: {$site['domain']} does not point here; gave up";
                } else {
                    $this->note($id, 'pending', (int) $site['auto_ssl_tries'], gmdate('c', time() + 900), 'Waiting for the domain to point to this server (last checked ' . gmdate('Y-m-d H:i') . ' UTC).');
                }
                continue;
            }
            $aliases = array_values(array_filter($site['aliases'], fn (string $a): bool => $this->pointsHere($a, $ip)));
            $site['aliases'] = $aliases;
            try {
                $this->ssl->issue($site, $this->email($site), $aliases !== [], false);
                $this->note($id, 'done', 0, null, 'Certificate issued automatically on ' . gmdate('Y-m-d H:i') . ' UTC.');
                $out[] = "auto SSL: issued for {$site['domain']}";
            } catch (AgentException|UserFacingException $e) {
                $tries = (int) $site['auto_ssl_tries'] + 1;
                if ($tries >= self::MAX_TRIES) {
                    $this->note($id, 'failed', $tries, null, 'Let\'s Encrypt refused the certificate ' . $tries . ' times: ' . $e->getMessage());
                } else {
                    // 30 minutes, then doubling up to 6 hours.
                    $this->note($id, 'pending', $tries, gmdate('c', time() + min(21600, 1800 * 2 ** ($tries - 1))), 'Let\'s Encrypt refused the certificate; trying again later: ' . $e->getMessage());
                }
                $out[] = "auto SSL: {$site['domain']} failed ({$tries}): " . $e->getMessage();
            }
        }
        return $out;
    }

    /** Whether the name resolves to this server. */
    public function pointsHere(string $name, ?string $ip): bool
    {
        if ($ip !== null && in_array($ip, $this->lookup->a($name), true)) {
            return true;
        }
        // Delegated to this panel's name servers (the zone of the name or a parent).
        $ours = array_map(static fn (string $n): string => rtrim(strtolower($n), '.'), $this->dns->nameservers());
        if ($ours === []) {
            return false;
        }
        $parts = explode('.', strtolower($name));
        for ($i = 0; $i < count($parts) - 1; $i++) {
            $ns = $this->lookup->ns(implode('.', array_slice($parts, $i)));
            if ($ns !== []) {
                return array_diff($ns, $ours) === [];
            }
        }
        return false;
    }

    /** @param array<string, mixed> $site the account's email, else an administrator's */
    private function email(array $site): string
    {
        $email = $site['owner_id'] !== null ? $this->db->scalar('SELECT email FROM users WHERE id = ?', [(int) $site['owner_id']]) : null;
        $email ??= $this->db->scalar("SELECT u.email FROM users u JOIN roles r ON r.id = u.role_id WHERE r.name = 'admin' AND u.is_active = 1 ORDER BY u.id LIMIT 1");
        return is_string($email) && filter_var($email, FILTER_VALIDATE_EMAIL) ? $email : '';
    }
}
