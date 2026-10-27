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
 *
 * Websites that already use HTTPS are checked too (auto_ssl_replace, every
 * few hours, or "Run AutoSSL for all websites"): one whose certificate has
 * expired, expires within REPLACE_DAYS without being renewed, does not
 * cover its domain or is self-signed is queued ('renew') for a new Let's
 * Encrypt certificate. Every step is written to autossl_log.
 */
final class AutoSslService
{
    public const WAIT_DAYS = 3;
    public const MAX_TRIES = 6;
    private const PER_PASS = 5;
    public const REPLACE_DAYS = 7;
    /** How often the scheduler looks for certificates to replace. */
    private const REPLACE_EVERY = 6 * 3600;
    private const LAST_REPLACE_KEY = '_autossl_last_replace_check';

    public function __construct(
        private readonly Connection $db,
        private readonly WebsiteRepository $sites,
        private readonly SslService $ssl,
        private readonly DnsService $dns,
        private readonly SiteOverview $overview,
        private readonly SettingsService $settings,
        private readonly ServerContext $server,
        private readonly DnsLookup $lookup,
        private readonly \WhaCP\Agent\AgentClient $agent,
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

    /** Write one line of the AutoSSL log (kept: the newest 2000 per server). */
    public function log(?int $websiteId, string $domain, string $result, string $message): void
    {
        $this->db->run('INSERT INTO autossl_log (server_id, website_id, domain, result, message, created_at) VALUES (?, ?, ?, ?, ?, ?)', [$this->server->id(), $websiteId, $domain, $result, mb_substr($message, 0, 1000), gmdate('c')]);
        $this->db->run('DELETE FROM autossl_log WHERE server_id = ? AND id <= (SELECT id FROM autossl_log WHERE server_id = ? ORDER BY id DESC LIMIT 1 OFFSET 2000)', [$this->server->id(), $this->server->id()]);
    }

    /** @return list<array<string, mixed>> the newest log lines */
    public function recentLog(int $limit = 200): array
    {
        return $this->db->fetchAll('SELECT * FROM autossl_log WHERE server_id = ? ORDER BY id DESC LIMIT ' . max(1, min(1000, $limit)), [$this->server->id()]);
    }

    /**
     * Why a website's certificate should be replaced ('' when it is fine).
     *
     * @param array<string, mixed>|null $host the website's entry of ssl.store's hosts
     */
    public static function replaceReason(?array $host): string
    {
        $c = $host['certificate'] ?? null;
        if ($host === null || empty($host['https']) || !is_array($c)) {
            return '';
        }
        return match (true) {
            !empty($c['error']) => 'the certificate cannot be read',
            !empty($c['expired']) => 'the certificate has expired',
            empty($host['covered']) => 'the certificate does not cover ' . $host['name'],
            !empty($c['self_signed']) => 'the certificate is self-signed (browsers warn)',
            (int) ($c['days_remaining'] ?? 0) <= self::REPLACE_DAYS => 'the certificate expires in ' . (int) $c['days_remaining'] . ' days and was not renewed',
            default => '',
        };
    }

    /**
     * Queue websites for a certificate: those without HTTPS ($all) and those
     * whose certificate should be replaced. Returns how many were queued.
     */
    public function queue(bool $all): int
    {
        $store = (array) $this->agent->call('ssl.store', [], 60);
        $hosts = [];
        foreach ((array) ($store['hosts'] ?? []) as $h) {
            $hosts[strtolower((string) $h['name'])] = $h;
        }
        $n = 0;
        foreach ($this->db->fetchAll('SELECT id FROM websites WHERE server_id = ? ORDER BY id', [$this->server->id()]) as $row) {
            $site = $this->sites->find($this->server->id(), (int) $row['id'], null);
            if ($site === null || !$site['enabled'] || in_array($site['auto_ssl'], ['pending', 'renew'], true)) {
                continue;
            }
            $id = (int) $site['id'];
            if (!$site['https']) {
                if ($all) {
                    $this->note($id, 'pending', 0, gmdate('c'), 'Queued by "Run AutoSSL for all websites".');
                    $this->log($id, (string) $site['domain'], 'queued', 'No HTTPS yet; queued for a Let\'s Encrypt certificate.');
                    $n++;
                }
                continue;
            }
            $why = self::replaceReason($hosts[strtolower((string) $site['domain'])] ?? null);
            if ($why !== '') {
                $this->note($id, 'renew', 0, gmdate('c'), 'Queued for a new certificate: ' . $why . '.');
                $this->log($id, (string) $site['domain'], 'queued', ucfirst($why) . '; queued for a new Let\'s Encrypt certificate.');
                $n++;
            }
        }
        return $n;
    }

    /** @return list<array<string, mixed>> websites waiting for AutoSSL */
    public function queued(): array
    {
        return $this->db->fetchAll("SELECT id, domain, auto_ssl, auto_ssl_tries, auto_ssl_next, auto_ssl_note FROM websites WHERE server_id = ? AND auto_ssl IN ('pending', 'renew') ORDER BY auto_ssl_next", [$this->server->id()]);
    }

    /** When the scheduler last ran (for "next check"), or null. */
    public function lastSchedulerRun(): ?int
    {
        $v = json_decode((string) ($this->db->scalar('SELECT value FROM settings WHERE key = ?', [Doctor::SCHEDULER_KEY]) ?? 'null'), true);
        $t = is_string($v) ? strtotime($v) : false;
        return $t === false ? null : $t;
    }

    /** @return list<string> what happened, for the scheduler's output */
    public function run(): array
    {
        $out = [];
        // Certificates to replace, every few hours.
        if ($this->settings->bool('auto_ssl_replace')) {
            $last = (int) json_decode((string) ($this->db->scalar('SELECT value FROM settings WHERE key = ?', [self::LAST_REPLACE_KEY]) ?? '0'), true);
            if ($last < time() - self::REPLACE_EVERY) {
                (new \WhaCP\Repositories\SettingRepository($this->db))->set(self::LAST_REPLACE_KEY, time(), null);
                try {
                    if (($n = $this->queue(false)) > 0) {
                        $out[] = "auto SSL: {$n} certificate(s) queued for replacement";
                    }
                } catch (AgentException $e) {
                    $out[] = 'auto SSL: certificates not checked: ' . $e->getMessage();
                }
            }
        }
        return array_merge($out, $this->process());
    }

    /** @return list<string> */
    public function process(int $limit = self::PER_PASS): array
    {
        $now = gmdate('c');
        $due = $this->db->fetchAll(
            "SELECT id, auto_ssl FROM websites WHERE server_id = ? AND auto_ssl IN ('pending', 'renew') AND (auto_ssl_next IS NULL OR auto_ssl_next <= ?) ORDER BY auto_ssl_next LIMIT " . max(1, $limit),
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
            $renew = $row['auto_ssl'] === 'renew';
            if ((!$renew && $site['https']) || !$site['enabled']) {
                $this->note($id, '', 0, null, $site['https'] ? 'HTTPS was set up.' : '');
                continue;
            }
            $ip ??= $this->overview->serverIp();
            if (!$this->pointsHere((string) $site['domain'], $ip)) {
                if ($renew) {
                    $this->note($id, 'gave_up', 0, null, 'The domain does not point to this server, so Let\'s Encrypt cannot check it; the certificate was not replaced.');
                    $this->log($id, (string) $site['domain'], 'skipped', 'The domain does not point to this server; the certificate was not replaced.');
                    $out[] = "auto SSL: {$site['domain']} does not point here; not replaced";
                } elseif (strtotime((string) $site['created_at']) < time() - self::WAIT_DAYS * 86400) {
                    $this->note($id, 'gave_up', (int) $site['auto_ssl_tries'], null, 'The domain did not point to this server within ' . self::WAIT_DAYS . ' days. Issue the certificate on this page once it does.');
                    $this->log($id, (string) $site['domain'], 'skipped', 'The domain did not point to this server within ' . self::WAIT_DAYS . ' days; gave up.');
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
                $this->note($id, 'done', 0, null, ($renew ? 'Certificate replaced' : 'Certificate issued') . ' automatically on ' . gmdate('Y-m-d H:i') . ' UTC.');
                $this->log($id, (string) $site['domain'], 'issued', ($renew ? 'Replaced with a new' : 'Issued a') . ' Let\'s Encrypt certificate for ' . implode(', ', array_merge([$site['domain']], $aliases)) . '.');
                $out[] = "auto SSL: issued for {$site['domain']}";
            } catch (AgentException|UserFacingException $e) {
                $tries = (int) $site['auto_ssl_tries'] + 1;
                $this->log($id, (string) $site['domain'], 'failed', 'Attempt ' . $tries . ' of ' . self::MAX_TRIES . ': ' . $e->getMessage());
                if ($tries >= self::MAX_TRIES) {
                    $this->note($id, 'failed', $tries, null, 'Let\'s Encrypt refused the certificate ' . $tries . ' times: ' . $e->getMessage());
                } else {
                    // 30 minutes, then doubling up to 6 hours.
                    $this->note($id, $renew ? 'renew' : 'pending', $tries, gmdate('c', time() + min(21600, 1800 * 2 ** ($tries - 1))), 'Let\'s Encrypt refused the certificate; trying again later: ' . $e->getMessage());
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
