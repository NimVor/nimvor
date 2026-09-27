<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * DNS zones served by the server's BIND. The panel keeps zones and records;
 * every change sends the complete zone to the agent, which validates it,
 * loads it with a higher serial and checks BIND serves it. The database
 * change is made in a transaction that is rolled back if the agent refuses.
 */
final class DnsService
{
    public const TYPES = ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'SRV', 'CAA'];

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly SettingsService $settings,
        private readonly WebsiteRepository $sites,
    ) {
    }

    // ---- reading --------------------------------------------------------

    /**
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function zones(array $user): array
    {
        [$cond, $params] = Scope::withWebsite($user, 'z.owner_id', 'z.website_id');
        return $this->db->fetchAll(
            "SELECT z.*, w.domain AS website, (SELECT COUNT(*) FROM dns_records r WHERE r.zone_id = z.id) AS records
             FROM dns_zones z LEFT JOIN websites w ON w.id = z.website_id WHERE z.server_id = :server AND {$cond} ORDER BY z.name",
            ['server' => $this->server->id()] + $params
        );
    }

    /**
     * @param array<string, mixed> $user
     * @return array<string, mixed>|null
     */
    public function zone(int $id, array $user): ?array
    {
        [$cond, $params] = Scope::withWebsite($user, 'owner_id', 'website_id');
        return $this->db->fetch("SELECT * FROM dns_zones WHERE id = :id AND server_id = :server AND {$cond}", ['id' => $id, 'server' => $this->server->id()] + $params);
    }

    /** @return list<array<string, mixed>> */
    public function records(int $zoneId): array
    {
        return $this->db->fetchAll(
            "SELECT * FROM dns_records WHERE zone_id = ? ORDER BY CASE name WHEN '@' THEN 0 ELSE 1 END, name, CASE type WHEN 'NS' THEN 0 WHEN 'A' THEN 1 WHEN 'AAAA' THEN 2 WHEN 'CNAME' THEN 3 WHEN 'MX' THEN 4 ELSE 5 END, id",
            [$zoneId]
        );
    }

    public function forWebsite(int $websiteId): ?array
    {
        return $this->db->fetch('SELECT * FROM dns_zones WHERE website_id = ?', [$websiteId]);
    }

    /** @return list<string> the configured name servers */
    public function nameservers(): array
    {
        $out = [];
        foreach (preg_split('/[\s,]+/', $this->settings->string('dns_nameservers')) ?: [] as $ns) {
            $ns = strtolower(rtrim(trim($ns), '.'));
            if ($ns !== '' && preg_match('/^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/', $ns)) {
                $out[] = $ns;
            }
        }
        return array_values(array_unique($out));
    }

    // ---- writing ----------------------------------------------------------

    /**
     * Create a zone (optionally with the usual records) and load it.
     *
     * @param array<string, mixed> $input name, website_id, template
     * @param array<string, mixed> $actor
     */
    public function create(array $input, array $actor): int
    {
        $name = strtolower(rtrim(trim((string) ($input['name'] ?? '')), '.'));
        $websiteId = filter_var($input['website_id'] ?? null, FILTER_VALIDATE_INT) ?: null;
        $site = $websiteId !== null ? $this->sites->find($this->server->id(), $websiteId, $actor) : null;
        if ($websiteId !== null && $site === null) {
            throw new ValidationException(['website_id' => 'Choose one of your websites.']);
        }
        if ($name === '' && $site !== null) {
            $name = strtolower((string) $site['domain']);
        }
        if (!preg_match('/^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/', $name) || strlen($name) > 253) {
            throw new ValidationException(['name' => 'Enter a domain name such as example.com.']);
        }
        // A zone for a website's domain belongs to that website (and so to
        // its owner), whether or not the form named the website.
        $site ??= Scope::isAll($actor) ? $this->sites->findByDomain($this->server->id(), $name) : null;
        if (!Scope::isAll($actor)) {
            // Customers host zones for their own websites only.
            if ($site === null || strtolower((string) $site['domain']) !== $name) {
                throw new ValidationException(['website_id' => 'Choose the website this zone belongs to; the zone must be its domain.']);
            }
        }
        if ($this->db->scalar('SELECT COUNT(*) FROM dns_zones WHERE server_id = ? AND name = ?', [$this->server->id(), $name]) > 0) {
            throw new ValidationException(['name' => 'This zone exists already.']);
        }
        $ns = $this->nameservers();
        if ($ns === []) {
            throw new UserFacingException('No name servers are configured.', '', 'An administrator sets them under Settings → DNS name servers.');
        }
        $hostmaster = $this->settings->string('dns_hostmaster');
        if ($hostmaster === '' || filter_var($hostmaster, FILTER_VALIDATE_EMAIL) === false) {
            $hostmaster = 'hostmaster@' . $name;
        }
        $ownerId = $site !== null ? ($site['owner_id'] !== null ? (int) $site['owner_id'] : null) : (Scope::isAll($actor) ? null : (int) $actor['id']);
        $now = gmdate('c');
        return $this->db->transaction(function () use ($name, $site, $ownerId, $ns, $hostmaster, $now, $input): int {
            $this->db->run(
                'INSERT INTO dns_zones (server_id, name, website_id, owner_id, primary_ns, hostmaster, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
                [$this->server->id(), $name, $site['id'] ?? null, $ownerId, $ns[0], $hostmaster, $now, $now]
            );
            $id = $this->db->lastInsertId();
            $records = array_map(static fn (string $n): array => ['name' => '@', 'type' => 'NS', 'value' => $n . '.'], $ns);
            $ip = $this->settings->string('dns_default_ipv4');
            if (!empty($input['template'])) {
                if (filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false) {
                    $records[] = ['name' => '@', 'type' => 'A', 'value' => $ip];
                    $records[] = ['name' => 'www', 'type' => 'CNAME', 'value' => '@'];
                }
                $records[] = ['name' => '@', 'type' => 'TXT', 'value' => 'v=spf1 a mx ~all'];
                $records[] = ['name' => '@', 'type' => 'CAA', 'value' => 'issue letsencrypt.org'];
            }
            foreach ($records as $r) {
                $this->insertRecord($id, $r + ['ttl' => 0, 'priority' => 0, 'weight' => 0, 'port' => 0]);
            }
            $this->push($id);
            $this->audit->record('dns.zone_create', 'dns_zone', (string) $id, AuditLogger::SUCCESS, ['zone' => $name, 'website_id' => $site['id'] ?? null]);
            return $id;
        });
    }

    /**
     * Validate a record from the form.
     *
     * @param array<string, mixed> $in
     * @return array{name: string, type: string, ttl: int, value: string, priority: int, weight: int, port: int}
     */
    public function cleanRecord(array $in, string $zone): array
    {
        $str = static fn (string $k): string => is_scalar($in[$k] ?? null) ? trim((string) $in[$k]) : '';
        $int = static fn (string $k): int => (int) ($in[$k] ?? 0);
        $name = strtolower($str('name'));
        $name = $name === '' || $name === $zone || $name === $zone . '.' ? '@' : preg_replace('/\.' . preg_quote($zone, '/') . '\.?$/', '', $name);
        $type = strtoupper($str('type'));
        $errors = [];
        if (!in_array($type, self::TYPES, true)) {
            $errors['type'] = 'Choose a record type.';
        }
        if ($name !== '@' && !preg_match('/^(\*\.)?([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?\.)*[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$/', (string) $name) && $name !== '*') {
            $errors['name'] = 'Use @ for the domain itself, or a name such as www, mail or *.dev.';
        }
        $ttl = $int('ttl');
        if ($ttl !== 0 && ($ttl < 60 || $ttl > 604800)) {
            $errors['ttl'] = 'Use 60–604800 seconds, or 0 for the zone default.';
        }
        $value = $str('value');
        if ($value === '' || strlen($value) > 4000 || preg_match('/[\x00-\x1F]/', $value)) {
            $errors['value'] = 'Enter the value on one line.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['name' => (string) $name, 'type' => $type, 'ttl' => $ttl, 'value' => $value, 'priority' => max(0, $int('priority')), 'weight' => max(0, $int('weight')), 'port' => max(0, $int('port'))];
    }

    /** @param array<string, mixed> $r */
    private function insertRecord(int $zoneId, array $r): void
    {
        $this->db->run(
            'INSERT INTO dns_records (zone_id, name, type, ttl, value, priority, weight, port) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
            [$zoneId, $r['name'], $r['type'], $r['ttl'], $r['value'], $r['priority'], $r['weight'], $r['port']]
        );
    }

    /**
     * Add (recordId null), change or delete ($delete) a record and reload
     * the zone; nothing changes if the agent refuses the zone.
     *
     * @param array<string, mixed> $zone
     * @param array<string, mixed>|null $input
     */
    public function saveRecord(array $zone, ?int $recordId, ?array $input, bool $delete = false): void
    {
        $zoneId = (int) $zone['id'];
        $this->db->transaction(function () use ($zone, $zoneId, $recordId, $input, $delete): void {
            $before = null;
            if ($recordId !== null) {
                $before = $this->db->fetch('SELECT * FROM dns_records WHERE id = ? AND zone_id = ?', [$recordId, $zoneId]);
                if ($before === null) {
                    throw new UserFacingException('The record no longer exists.');
                }
            }
            if ($delete) {
                $this->db->run('DELETE FROM dns_records WHERE id = ?', [$recordId]);
                $after = null;
            } else {
                $after = $this->cleanRecord((array) $input, (string) $zone['name']);
                if ($recordId === null) {
                    if ($this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ?', [$zoneId]) >= 2000) {
                        throw new UserFacingException('A zone can hold at most 2000 records.');
                    }
                    $this->insertRecord($zoneId, $after);
                } else {
                    $this->db->run('UPDATE dns_records SET name = ?, type = ?, ttl = ?, value = ?, priority = ?, weight = ?, port = ? WHERE id = ?', [$after['name'], $after['type'], $after['ttl'], $after['value'], $after['priority'], $after['weight'], $after['port'], $recordId]);
                }
            }
            $this->push($zoneId);
            $action = $delete ? 'dns.record_delete' : ($recordId === null ? 'dns.record_create' : 'dns.record_update');
            $this->audit->record($action, 'dns_zone', (string) $zoneId, AuditLogger::SUCCESS, ['zone' => $zone['name'], 'before' => $before === null ? null : array_intersect_key($before, array_flip(['name', 'type', 'value'])), 'after' => $after === null ? null : array_intersect_key($after, array_flip(['name', 'type', 'value']))]);
        });
    }

    /**
     * Zone settings: default TTL and secondary servers allowed to transfer.
     *
     * @param array<string, mixed> $zone
     */
    public function settings(array $zone, int $ttl, string $secondaries): void
    {
        $ips = array_values(array_filter(array_map('trim', preg_split('/[\s,]+/', $secondaries) ?: [])));
        foreach ($ips as $ip) {
            if (filter_var($ip, FILTER_VALIDATE_IP) === false) {
                throw new ValidationException(['secondaries' => 'Enter IP addresses separated by commas.']);
            }
        }
        if ($ttl < 60 || $ttl > 604800) {
            throw new ValidationException(['ttl' => 'Use 60–604800 seconds.']);
        }
        $this->db->transaction(function () use ($zone, $ttl, $ips): void {
            $this->db->run('UPDATE dns_zones SET ttl = ?, secondaries = ?, updated_at = ? WHERE id = ?', [$ttl, implode(',', $ips), gmdate('c'), $zone['id']]);
            $this->push((int) $zone['id']);
            $this->audit->record('dns.zone_settings', 'dns_zone', (string) $zone['id'], AuditLogger::SUCCESS, ['zone' => $zone['name'], 'ttl' => $ttl, 'secondaries' => $ips]);
        });
    }

    /** Load the zone again (e.g. after the DNS server was reinstalled). */
    public function sync(array $zone): void
    {
        $this->push((int) $zone['id']);
    }

    /** @param array<string, mixed> $zone */
    public function delete(array $zone, string $confirm): void
    {
        if (strtolower(trim($confirm)) !== (string) $zone['name']) {
            throw new ValidationException(['confirm' => 'Type the zone name to confirm.']);
        }
        try {
            $this->agent->call('dns.zone_delete', ['zone' => $zone['name'], 'confirm' => $zone['name']]);
        } catch (\WhaCP\Agent\AgentException $e) {
            if ($e->status() !== 404) {
                throw $e;
            }
        }
        $this->db->run('DELETE FROM dns_zones WHERE id = ?', [$zone['id']]);
        $this->audit->record('dns.zone_delete', 'dns_zone', (string) $zone['id'], AuditLogger::SUCCESS, ['zone' => $zone['name']]);
    }

    /** Send the complete zone to the agent and remember the serial. */
    private function push(int $zoneId): void
    {
        $z = $this->db->fetch('SELECT * FROM dns_zones WHERE id = ?', [$zoneId]) ?? throw new UserFacingException('The zone no longer exists.');
        $records = array_map(static fn (array $r): array => [
            'name' => $r['name'], 'type' => $r['type'], 'ttl' => (int) $r['ttl'], 'value' => $r['value'],
            'priority' => (int) $r['priority'], 'weight' => (int) $r['weight'], 'port' => (int) $r['port'],
        ], $this->records($zoneId));
        $res = (array) $this->agent->call('dns.zone_apply', [
            'zone' => $z['name'], 'ttl' => (int) $z['ttl'],
            'soa' => ['primary_ns' => $z['primary_ns'] . '.', 'hostmaster' => $z['hostmaster']],
            'records' => $records,
            'secondaries' => $z['secondaries'] === '' ? [] : explode(',', (string) $z['secondaries']),
        ], 60);
        $this->db->run('UPDATE dns_zones SET serial = ?, applied_at = ?, updated_at = ? WHERE id = ?', [(int) ($res['serial'] ?? 0), gmdate('c'), gmdate('c'), $zoneId]);
    }
}
