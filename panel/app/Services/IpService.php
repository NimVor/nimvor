<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Database\Connection;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * IP management: the pool of addresses websites may be bound to (shared,
 * dedicated or reserved), routed IPv6 ranges and their allocations, binding
 * a website to addresses (with its DNS records), the migration wizard and
 * rebuilding the pool from the server's interfaces.
 *
 * Addresses are never configured on network interfaces from here: an IPv4
 * address must already be on the server; routed IPv6 ranges are made local
 * by the agent (a local route on lo).
 */
final class IpService
{
    /** @var array<string, mixed>|null */
    private ?array $server = null;

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly WebsiteService $websites,
        private readonly WebsiteRepository $repo,
        private readonly DnsService $dns,
        private readonly SettingsService $settings,
        private readonly AuditLogger $audit,
        private readonly ServerContext $context,
    ) {
    }

    private function sid(): int
    {
        return $this->context->id();
    }

    /** The canonical form of an address, or null. */
    public static function canonical(string $ip): ?string
    {
        $bin = @inet_pton(trim($ip));
        return $bin === false ? null : (string) inet_ntop($bin);
    }

    public static function family(string $ip): int
    {
        return str_contains($ip, ':') ? 6 : 4;
    }

    /** Usable for websites: not loopback, unspecified, link-local or multicast. */
    public static function usable(string $ip): bool
    {
        return filter_var($ip, FILTER_VALIDATE_IP) !== false
            && !in_array($ip, ['0.0.0.0', '::', '::1'], true)
            && !str_starts_with($ip, '127.') && !preg_match('/^(fe[89ab][0-9a-f]:|ff[0-9a-f]{2}:)/i', $ip)
            && !preg_match('/^(22[4-9]|23\d)\./', $ip);
    }

    /** @return array<string, mixed> the agent's view of the server's addresses */
    public function serverAddresses(): array
    {
        return $this->server ??= (array) json_decode((string) json_encode($this->agent->call('net.addresses', [], 30)), true);
    }

    /** @return list<string> global addresses on the server's interfaces */
    public function onServer(): array
    {
        $out = [];
        foreach ((array) ($this->serverAddresses()['addresses'] ?? []) as $a) {
            $ip = self::canonical((string) ($a['address'] ?? ''));
            if ($ip !== null && self::usable($ip) && ($a['scope'] ?? '') !== 'link') {
                $out[] = $ip;
            }
        }
        return array_values(array_unique($out));
    }

    /** Whether an IPv6 address lies in a prefix. */
    public static function inPrefix(string $ip, string $prefix): bool
    {
        [$net, $len] = array_pad(explode('/', $prefix, 2), 2, '128');
        $a = @inet_pton($ip);
        $b = @inet_pton($net);
        if ($a === false || $b === false || strlen($a) !== strlen($b)) {
            return false;
        }
        $bits = (int) $len;
        $bytes = intdiv($bits, 8);
        if (substr($a, 0, $bytes) !== substr($b, 0, $bytes)) {
            return false;
        }
        $rest = $bits % 8;
        if ($rest === 0) {
            return true;
        }
        $mask = (0xff << (8 - $rest)) & 0xff;
        return (ord($a[$bytes]) & $mask) === (ord($b[$bytes]) & $mask);
    }

    /** @return list<array<string, mixed>> websites bound to an address */
    public function sitesUsing(string $ip): array
    {
        return $this->db->fetchAll(
            'SELECT w.id, w.domain, w.ipv4, w.ipv6, u.username AS owner FROM websites w LEFT JOIN users u ON u.id = w.owner_id WHERE w.server_id = ? AND (w.ipv4 = ? OR w.ipv6 = ?) ORDER BY w.domain',
            [$this->sid(), $ip, $ip]
        );
    }

    /**
     * The pool with each address's websites and whether the server has it.
     *
     * @return list<array<string, mixed>>
     */
    public function pool(): array
    {
        $server = [];
        $ranges = $this->ranges();
        try {
            $server = array_flip($this->onServer());
        } catch (\Throwable) {
            $server = null;
        }
        $rows = $this->db->fetchAll('SELECT * FROM ip_addresses WHERE server_id = ? ORDER BY family, address', [$this->sid()]);
        foreach ($rows as &$r) {
            $r['sites'] = $this->sitesUsing((string) $r['address']);
            $inRange = false;
            foreach ($ranges as $g) {
                $inRange = $inRange || self::inPrefix((string) $r['address'], (string) $g['prefix']);
            }
            $r['present'] = $server === null ? null : (isset($server[$r['address']]) || $inRange);
        }
        return $rows;
    }

    /** @return array<string, mixed>|null */
    public function find(int $id): ?array
    {
        return $this->db->fetch('SELECT * FROM ip_addresses WHERE id = ? AND server_id = ?', [$id, $this->sid()]);
    }

    /** @return array<string, mixed>|null */
    public function findAddress(string $ip): ?array
    {
        return $this->db->fetch('SELECT * FROM ip_addresses WHERE server_id = ? AND address = ?', [$this->sid(), $ip]);
    }

    /** Whether the server can listen on an address (interface or range). */
    private function available(string $ip): bool
    {
        if (in_array($ip, $this->onServer(), true)) {
            return true;
        }
        foreach ($this->ranges() as $g) {
            if (self::family($ip) === 6 && self::inPrefix($ip, (string) $g['prefix'])) {
                return true;
            }
        }
        return false;
    }

    public function add(string $address, string $kind, string $reason): int
    {
        $ip = self::canonical($address);
        if ($ip === null || !self::usable($ip)) {
            throw new ValidationException(['address' => 'Enter a public or private IPv4 or IPv6 address of this server (not loopback, link-local or multicast).']);
        }
        if (!in_array($kind, ['shared', 'dedicated'], true)) {
            throw new ValidationException(['kind' => 'Choose shared or dedicated.']);
        }
        if ($this->findAddress($ip) !== null) {
            throw new ValidationException(['address' => $ip . ' is already in the pool.']);
        }
        if (!$this->available($ip)) {
            throw new ValidationException(['address' => $ip . ' is not configured on this server. Add it to a network interface (your provider\'s panel or the system\'s network configuration) first, or add its IPv6 range.']);
        }
        $this->db->run('INSERT INTO ip_addresses (server_id, address, family, kind, reserved, reason, created_at) VALUES (?, ?, ?, ?, 0, ?, ?)', [$this->sid(), $ip, self::family($ip), $kind, mb_substr(trim($reason), 0, 300), gmdate('c')]);
        $id = $this->db->lastInsertId();
        $this->audit->record('ip.add', 'server', 'ip', AuditLogger::SUCCESS, ['address' => $ip, 'kind' => $kind]);
        return $id;
    }

    public function update(int $id, string $kind, bool $reserved, string $reason): void
    {
        $row = $this->find($id) ?? throw new UserFacingException('That address is not in the pool.');
        if (!in_array($kind, ['shared', 'dedicated'], true)) {
            throw new ValidationException(['kind' => 'Choose shared or dedicated.']);
        }
        $sites = $this->sitesUsing((string) $row['address']);
        if ($reserved && $sites !== []) {
            throw new UserFacingException($row['address'] . ' is used by ' . implode(', ', array_column($sites, 'domain')) . '; move those websites first.');
        }
        if ($kind === 'dedicated' && count($sites) > 1) {
            throw new UserFacingException($row['address'] . ' is used by ' . count($sites) . ' websites; a dedicated address belongs to one.');
        }
        if ($reserved && trim($reason) === '') {
            throw new ValidationException(['reason' => 'Say why the address is reserved.']);
        }
        $this->db->run('UPDATE ip_addresses SET kind = ?, reserved = ?, reason = ? WHERE id = ?', [$kind, (int) $reserved, mb_substr(trim($reason), 0, 300), $id]);
        $this->audit->record('ip.update', 'server', 'ip', AuditLogger::SUCCESS, ['address' => $row['address'], 'kind' => $kind, 'reserved' => $reserved, 'reason' => $reason]);
    }

    public function delete(int $id): void
    {
        $row = $this->find($id) ?? throw new UserFacingException('That address is not in the pool.');
        if (($sites = $this->sitesUsing((string) $row['address'])) !== []) {
            throw new UserFacingException($row['address'] . ' is used by ' . implode(', ', array_column($sites, 'domain')) . '; it cannot be removed.');
        }
        $this->db->run('DELETE FROM ip_addresses WHERE id = ?', [$id]);
        $this->audit->record('ip.delete', 'server', 'ip', AuditLogger::SUCCESS, ['address' => $row['address']]);
    }

    // ---- IPv6 ranges -------------------------------------------------------------

    /** @return list<array<string, mixed>> */
    public function ranges(): array
    {
        $rows = $this->db->fetchAll('SELECT * FROM ipv6_ranges WHERE server_id = ? ORDER BY prefix', [$this->sid()]);
        foreach ($rows as &$r) {
            $r['allocated'] = (int) $this->db->scalar('SELECT COUNT(*) FROM ip_addresses WHERE range_id = ?', [$r['id']]);
            $r['used'] = (int) $this->db->scalar('SELECT COUNT(*) FROM ip_addresses a WHERE a.range_id = ? AND EXISTS (SELECT 1 FROM websites w WHERE w.ipv6 = a.address)', [$r['id']]);
        }
        return $rows;
    }

    /** @param list<string> $prefixes */
    private function pushRanges(array $prefixes): void
    {
        $this->agent->call('net.ipv6_ranges', ['ranges' => array_values($prefixes)], 60);
        $this->server = null;
    }

    public function addRange(string $prefix, string $note): int
    {
        $prefix = strtolower(trim($prefix));
        [$net, $len] = array_pad(explode('/', $prefix, 2), 2, '');
        $canon = self::canonical($net);
        if ($canon === null || self::family($canon) !== 6 || !ctype_digit($len)) {
            throw new ValidationException(['prefix' => 'Enter an IPv6 range such as 2001:db8:1::/64.']);
        }
        $prefix = $canon . '/' . (int) $len;
        if ($this->db->scalar('SELECT COUNT(*) FROM ipv6_ranges WHERE server_id = ? AND prefix = ?', [$this->sid(), $prefix]) > 0) {
            throw new ValidationException(['prefix' => 'That range is already added.']);
        }
        $all = array_merge(array_column($this->ranges(), 'prefix'), [$prefix]);
        // The agent checks the range (routed, not on-link) and adds the route.
        $this->pushRanges($all);
        $this->db->run('INSERT INTO ipv6_ranges (server_id, prefix, note, created_at) VALUES (?, ?, ?, ?)', [$this->sid(), $prefix, mb_substr(trim($note), 0, 300), gmdate('c')]);
        $id = $this->db->lastInsertId();
        $this->audit->record('ip.range_add', 'server', 'ip', AuditLogger::SUCCESS, ['prefix' => $prefix]);
        return $id;
    }

    public function updateRange(int $id, string $note): void
    {
        $this->db->run('UPDATE ipv6_ranges SET note = ? WHERE id = ? AND server_id = ?', [mb_substr(trim($note), 0, 300), $id, $this->sid()]);
    }

    public function removeRange(int $id): void
    {
        $range = $this->db->fetch('SELECT * FROM ipv6_ranges WHERE id = ? AND server_id = ?', [$id, $this->sid()]) ?? throw new UserFacingException('That range does not exist.');
        $used = $this->db->fetchAll('SELECT w.domain FROM ip_addresses a JOIN websites w ON w.ipv6 = a.address WHERE a.range_id = ?', [$id]);
        if ($used !== []) {
            throw new UserFacingException('Addresses of this range are used by ' . implode(', ', array_column($used, 'domain')) . '; move those websites first.');
        }
        $rest = array_values(array_filter(array_column($this->ranges(), 'prefix'), static fn (string $p): bool => $p !== $range['prefix']));
        $this->pushRanges($rest);
        $this->db->transaction(function () use ($id): void {
            $this->db->run('DELETE FROM ip_addresses WHERE range_id = ?', [$id]);
            $this->db->run('DELETE FROM ipv6_ranges WHERE id = ?', [$id]);
        });
        $this->audit->record('ip.range_remove', 'server', 'ip', AuditLogger::SUCCESS, ['prefix' => $range['prefix']]);
    }

    /** Take an address of a range into the pool (the next free one, or the one given). */
    public function allocate(int $rangeId, string $address = '', string $kind = 'dedicated'): string
    {
        $range = $this->db->fetch('SELECT * FROM ipv6_ranges WHERE id = ? AND server_id = ?', [$rangeId, $this->sid()]) ?? throw new UserFacingException('That range does not exist.');
        $prefix = (string) $range['prefix'];
        if ($address !== '') {
            $ip = self::canonical($address);
            if ($ip === null || self::family($ip) !== 6 || !self::inPrefix($ip, $prefix)) {
                throw new ValidationException(['address' => 'Enter an address inside ' . $prefix . '.']);
            }
            if ($this->findAddress($ip) !== null) {
                throw new ValidationException(['address' => $ip . ' is already in the pool.']);
            }
        } else {
            [$net] = explode('/', $prefix);
            $bin = (string) inet_pton($net);
            $ip = null;
            // From ::10 upwards (the first addresses are often the router's).
            for ($n = 0x10; $n < 0x10 + 100000; $n++) {
                $cand = substr($bin, 0, 12) . pack('N', unpack('N', substr($bin, 12))[1] + $n);
                $c = (string) inet_ntop($cand);
                if (!self::inPrefix($c, $prefix)) {
                    break;
                }
                if ($this->findAddress($c) === null) {
                    $ip = $c;
                    break;
                }
            }
            if ($ip === null) {
                throw new UserFacingException('No free address was found in ' . $prefix . '.');
            }
        }
        $this->db->run('INSERT INTO ip_addresses (server_id, address, family, kind, reserved, reason, range_id, created_at) VALUES (?, ?, 6, ?, 0, \'\', ?, ?)', [$this->sid(), $ip, $kind === 'shared' ? 'shared' : 'dedicated', $rangeId, gmdate('c')]);
        $this->audit->record('ip.allocate', 'server', 'ip', AuditLogger::SUCCESS, ['address' => $ip, 'prefix' => $prefix]);
        return $ip;
    }

    // ---- websites -------------------------------------------------------------------

    /** Checks that a website may use an address from the pool ('' = every address). */
    private function assignable(string $ip, int $family, int $siteId): ?string
    {
        if ($ip === '') {
            return null;
        }
        $canon = self::canonical($ip);
        if ($canon === null || self::family($canon) !== $family) {
            throw new ValidationException(['ipv' . $family => 'Choose an IPv' . $family . ' address from the pool.']);
        }
        $row = $this->findAddress($canon) ?? throw new ValidationException(['ipv' . $family => $canon . ' is not in the IP pool.']);
        if ((int) $row['reserved'] === 1) {
            throw new ValidationException(['ipv' . $family => $canon . ' is reserved' . ($row['reason'] !== '' ? ' (' . $row['reason'] . ')' : '') . '.']);
        }
        if ($row['kind'] === 'dedicated') {
            $others = array_filter($this->sitesUsing($canon), static fn (array $s): bool => (int) $s['id'] !== $siteId);
            if ($others !== []) {
                throw new ValidationException(['ipv' . $family => $canon . ' is dedicated to ' . implode(', ', array_column($others, 'domain')) . '.']);
            }
        }
        return $canon;
    }

    /** @param array<string, mixed> $site @return list<string> names of the site within a zone */
    private function namesIn(array $site, int $zoneId): array
    {
        $out = [];
        foreach (array_merge([(string) $site['domain']], (array) $site['aliases']) as $host) {
            $z = $this->dns->zoneForName($host);
            if ($z !== null && (int) $z['zone']['id'] === $zoneId) {
                $out[] = $z['name'];
            }
        }
        return array_values(array_unique($out));
    }

    /**
     * Bind a website to addresses ('' = every address). With $dns its A and
     * AAAA records follow; if the DNS server refuses, the website goes back
     * to its previous addresses.
     *
     * @param array<string, mixed> $site
     * @return array{dns_records: int}
     */
    public function assign(array $site, string $ipv4, string $ipv6, bool $dns): array
    {
        $v4 = $this->assignable($ipv4, 4, (int) $site['id']);
        $v6 = $this->assignable($ipv6, 6, (int) $site['id']);
        $old4 = (string) ($site['ipv4'] ?? '');
        $old6 = (string) ($site['ipv6'] ?? '');
        if ($old4 === (string) $v4 && $old6 === (string) $v6) {
            return ['dns_records' => 0];
        }
        $this->websites->patch($site, ['ipv4' => $v4, 'ipv6' => $v6], 'website.ip');
        $changed = 0;
        if ($dns) {
            try {
                $z = $this->dns->zoneForName((string) $site['domain']);
                if ($z !== null) {
                    $zid = (int) $z['zone']['id'];
                    $names = $this->namesIn($site, $zid);
                    $default = (string) $this->settings->string('dns_default_ipv4');
                    $eff4Old = $old4 !== '' ? $old4 : $default;
                    $eff4New = $v4 ?? $default;
                    if ($eff4Old !== '' && $eff4New !== '' && $eff4Old !== $eff4New) {
                        $changed += $this->dns->replaceAddress($zid, 'A', $eff4Old, $eff4New, $names);
                    }
                    if ($old6 !== '' && $old6 !== (string) $v6) {
                        $changed += $this->dns->replaceAddress($zid, 'AAAA', $old6, (string) $v6, $names);
                    } elseif ($old6 === '' && $v6 !== null) {
                        $changed += $this->dns->replaceAddress($zid, 'AAAA', '', $v6, $names);
                    }
                }
            } catch (\Throwable $e) {
                $fresh = $this->repo->find((int) $site['server_id'], (int) $site['id'], null) ?? $site;
                $this->websites->patch($fresh, ['ipv4' => $old4 !== '' ? $old4 : null, 'ipv6' => $old6 !== '' ? $old6 : null], 'website.ip_rollback');
                throw new UserFacingException('The DNS records could not be updated, so the website was moved back to its previous address.', $e->getMessage());
            }
        }
        $this->audit->record('ip.assign', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'ipv4' => [$old4, $v4], 'ipv6' => [$old6, $v6], 'dns_records' => $changed]);
        return ['dns_records' => $changed];
    }

    // ---- migration wizard -----------------------------------------------------------

    /**
     * What moving from one address to another changes.
     *
     * @return array<string, mixed>
     */
    public function migrationPlan(string $source, string $dest, bool $allZones): array
    {
        $s = self::canonical($source);
        $d = self::canonical($dest);
        $errors = [];
        $row = $d === null ? null : $this->findAddress($d);
        if ($s === null || $this->findAddress($s) === null) {
            $errors['source'] = 'Choose the address to move from (from the pool).';
        }
        if ($row === null) {
            $errors['dest'] = 'Choose the address to move to (from the pool).';
        } elseif ((int) $row['reserved'] === 1) {
            $errors['dest'] = $d . ' is reserved.';
        } elseif ($s !== null && self::family($s) !== self::family($d)) {
            $errors['dest'] = 'Both addresses must be IPv4, or both IPv6.';
        } elseif ($s === $d) {
            $errors['dest'] = 'Choose a different address.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $sites = $this->sitesUsing((string) $s);
        if ($row !== null && $row['kind'] === 'dedicated' && count($sites) + count($this->sitesUsing((string) $d)) > 1) {
            throw new ValidationException(['dest' => $d . ' is dedicated; it can take only one website.']);
        }
        $zones = $allZones ? $this->dns->zonesUsingAddress((string) $s) : [];
        return [
            'source' => $s, 'dest' => $d, 'family' => self::family((string) $s), 'sites' => $sites, 'zones' => $zones, 'all_zones' => $allZones,
            'default_ipv4' => $allZones && $this->settings->string('dns_default_ipv4') === $s,
        ];
    }

    /**
     * Move every website (and, with $allZones, every DNS record) from one
     * address to another; each website is moved on its own, so one failure
     * does not stop the others.
     *
     * @return list<array{item: string, ok: bool, message: string}>
     */
    public function migrate(string $source, string $dest, bool $allZones): array
    {
        $plan = $this->migrationPlan($source, $dest, $allZones);
        $results = [];
        $key = $plan['family'] === 4 ? 'ipv4' : 'ipv6';
        foreach ($plan['sites'] as $s) {
            $site = $this->repo->find($this->sid(), (int) $s['id'], null);
            if ($site === null) {
                continue;
            }
            try {
                $res = $this->assign($site, $key === 'ipv4' ? $plan['dest'] : (string) ($site['ipv4'] ?? ''), $key === 'ipv6' ? $plan['dest'] : (string) ($site['ipv6'] ?? ''), true);
                $results[] = ['item' => (string) $site['domain'], 'ok' => true, 'message' => 'Moved; ' . $res['dns_records'] . ' DNS record(s) updated.'];
            } catch (\Throwable $e) {
                $results[] = ['item' => (string) $site['domain'], 'ok' => false, 'message' => $e->getMessage()];
            }
        }
        if ($allZones) {
            foreach ($this->dns->zonesUsingAddress($plan['source']) as $z) {
                try {
                    $n = $this->dns->replaceAddress($z['id'], $plan['family'] === 4 ? 'A' : 'AAAA', $plan['source'], $plan['dest']);
                    $results[] = ['item' => 'DNS zone ' . $z['name'], 'ok' => true, 'message' => $n . ' record(s) updated.'];
                } catch (\Throwable $e) {
                    $results[] = ['item' => 'DNS zone ' . $z['name'], 'ok' => false, 'message' => $e->getMessage()];
                }
            }
            if ($plan['default_ipv4']) {
                $this->settings->update(['dns_default_ipv4' => $plan['dest']], null);
                $results[] = ['item' => 'Default IPv4 for new zones', 'ok' => true, 'message' => 'Now ' . $plan['dest'] . '.'];
            }
        }
        $this->audit->record('ip.migrate', 'server', 'ip', in_array(false, array_column($results, 'ok'), true) ? AuditLogger::FAILURE : AuditLogger::SUCCESS, ['source' => $plan['source'], 'dest' => $plan['dest'], 'results' => $results]);
        return $results;
    }

    // ---- rebuild ------------------------------------------------------------------------

    /**
     * Reconcile the pool with the server: add the server's addresses and the
     * addresses websites use that are missing; re-apply the IPv6 ranges.
     * Nothing is removed (addresses the server no longer has are reported).
     *
     * @return array{added: list<string>, missing: list<string>}
     */
    public function rebuild(): array
    {
        $added = [];
        $ranges = array_column($this->ranges(), 'prefix');
        if ($ranges !== []) {
            $this->pushRanges($ranges);
        }
        $want = $this->onServer();
        foreach ($this->db->fetchAll('SELECT ipv4, ipv6 FROM websites WHERE server_id = ?', [$this->sid()]) as $w) {
            foreach (['ipv4', 'ipv6'] as $k) {
                if (!empty($w[$k])) {
                    $want[] = (string) $w[$k];
                }
            }
        }
        foreach (array_unique($want) as $ip) {
            if ($this->findAddress($ip) === null) {
                $rangeId = null;
                foreach ($this->ranges() as $g) {
                    if (self::family($ip) === 6 && self::inPrefix($ip, (string) $g['prefix'])) {
                        $rangeId = (int) $g['id'];
                    }
                }
                $this->db->run('INSERT INTO ip_addresses (server_id, address, family, kind, reserved, reason, range_id, created_at) VALUES (?, ?, ?, \'shared\', 0, \'\', ?, ?)', [$this->sid(), $ip, self::family($ip), $rangeId, gmdate('c')]);
                $added[] = $ip;
            }
        }
        $missing = [];
        foreach ($this->pool() as $r) {
            if ($r['present'] === false) {
                $missing[] = (string) $r['address'];
            }
        }
        $this->audit->record('ip.rebuild', 'server', 'ip', AuditLogger::SUCCESS, ['added' => $added, 'missing' => $missing]);
        return ['added' => $added, 'missing' => $missing];
    }
}
