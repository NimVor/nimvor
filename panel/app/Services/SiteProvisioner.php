<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Support\Logger;

/**
 * DNS for a new website, when this server hosts DNS:
 *
 * - a domain under a zone of the same account (blog.shop.test when
 *   shop.test is hosted here) gets an A record in that zone;
 * - otherwise the domain gets its own zone: A, www, SPF, DMARC and CAA
 *   (following the account's options), and, when this server hosts email,
 *   the domain's email with its DKIM key and MX record.
 *
 * Nothing happens when the account uses its registrar's name servers.
 * Failures are logged and never undo the website.
 */
final class SiteProvisioner
{
    /** Acts on the website's behalf (the website is already access-checked). */
    private const SYSTEM = ['id' => 0, 'username' => 'system', 'role_scope' => 'all'];

    public function __construct(
        private readonly Connection $db,
        private readonly DnsService $dns,
        private readonly MailService $mail,
        private readonly AccountOptions $options,
        private readonly SettingsService $settings,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly Logger $log,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @return list<string> what was done, for the audit log
     */
    public function provision(array $site): array
    {
        $domain = strtolower((string) $site['domain']);
        $owner = $site['owner_id'] === null ? null : (int) $site['owner_id'];
        $opts = $this->options->get($owner);
        if (!empty($site['parent_id'])) {
            $parent = (string) $this->db->scalar('SELECT domain FROM websites WHERE id = ?', [(int) $site['parent_id']]);
            if ($parent !== '' && str_ends_with($domain, '.' . $parent)) {
                return []; // subdomains: the parent's zone, from the Subdomains page
            }
        }
        if ($opts['dns_registrar_ns'] || !$this->dnsReady()) {
            return [];
        }
        $done = [];
        try {
            $zone = $this->db->fetch('SELECT * FROM dns_zones WHERE server_id = ? AND name = ?', [(int) $site['server_id'], $domain]);
            if ($zone !== null) {
                if ($opts['dns_overwrite'] && $this->sameOwner($zone, $owner)) {
                    $this->dns->delete($zone, $domain);
                    $zone = null;
                    $done[] = 'replaced the existing zone';
                } else {
                    return []; // kept as it is (linked to the website when created)
                }
            }
            [$parentZone, $mine] = $this->parentZone((int) $site['server_id'], $domain, $owner);
            if ($parentZone !== null && !$mine) {
                return []; // under another account's zone: never touched
            }
            if ($parentZone !== null) {
                $label = substr($domain, 0, -strlen('.' . $parentZone['name']));
                if ($this->addA($parentZone, $label)) {
                    $done[] = "A record {$label} in {$parentZone['name']}";
                }
                return $done;
            }
            $this->dns->create([
                'name' => $domain, 'website_id' => (int) $site['id'], 'template' => '1',
                'spf' => (bool) $opts['dns_spf'],
                // DMARC needs SPF and DKIM.
                'dmarc' => $opts['dns_dmarc'] && $opts['dns_spf'] && $opts['dns_dkim'],
            ], self::SYSTEM);
            $done[] = 'DNS zone';
            // Mail received elsewhere (backup or remote exchanger): no mailboxes here.
            if ($opts['dns_dkim'] && !in_array($opts['mail_routing'], ['remote', 'backup'], true)) {
                $done = array_merge($done, $this->email($site));
            }
        } catch (\Throwable $e) {
            $this->log->warning('website DNS set-up failed', ['domain' => $domain, 'error' => $e->getMessage()]);
            $done[] = 'DNS set-up failed: ' . $e->getMessage();
        }
        if ($done !== []) {
            $this->audit->record('website.provision', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $domain, 'done' => $done]);
        }
        return $done;
    }

    private function dnsReady(): bool
    {
        if ($this->dns->nameservers() === []) {
            return false;
        }
        try {
            $st = (array) $this->agent->call('dns.status');
        } catch (AgentException) {
            return false;
        }
        return !empty($st['configured']) && !empty($st['running']);
    }

    /** @param array<string, mixed> $zone */
    private function sameOwner(array $zone, ?int $owner): bool
    {
        return ($zone['owner_id'] === null ? null : (int) $zone['owner_id']) === $owner;
    }

    /**
     * The closest zone above the domain, and whether it belongs to the
     * same account.
     *
     * @return array{0: array<string, mixed>|null, 1: bool}
     */
    private function parentZone(int $serverId, string $domain, ?int $owner): array
    {
        $parts = explode('.', $domain);
        for ($i = 1; $i < count($parts) - 1; $i++) {
            $name = implode('.', array_slice($parts, $i));
            $zone = $this->db->fetch('SELECT * FROM dns_zones WHERE server_id = ? AND name = ?', [$serverId, $name]);
            if ($zone !== null) {
                return [$zone, $this->sameOwner($zone, $owner)];
            }
        }
        return [null, false];
    }

    /** @param array<string, mixed> $zone */
    private function addA(array $zone, string $label): bool
    {
        $ip = trim($this->settings->string('dns_default_ipv4'));
        if (filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false) {
            return false;
        }
        foreach ($this->dns->records((int) $zone['id']) as $r) {
            if ((string) $r['name'] === $label) {
                return false;
            }
        }
        $this->dns->saveRecord($zone, null, ['name' => $label, 'type' => 'A', 'value' => $ip, 'ttl' => 0]);
        return true;
    }

    /**
     * Email for the domain when this server hosts email: its DKIM key, and
     * MX and DKIM records in the zone.
     *
     * @param array<string, mixed> $site
     * @return list<string>
     */
    private function email(array $site): array
    {
        $status = $this->mail->status();
        if ($status === null || empty($status['configured']) || $this->mail->forWebsite((int) $site['id']) !== null) {
            return [];
        }
        $this->mail->addDomain(['website_id' => (int) $site['id']], self::SYSTEM);
        $domain = $this->mail->forWebsite((int) $site['id']);
        if ($domain === null) {
            return [];
        }
        $this->mail->publishDns($domain, (string) ($status['hostname'] ?? ''), self::SYSTEM);
        return ['email with DKIM'];
    }
}
