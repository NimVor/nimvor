<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Database\Connection;
use NimVor\Hosting\Scope;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * DNS zones served by the server's BIND. The panel keeps zones and records;
 * every change sends the complete zone to the agent, which validates it,
 * loads it with a higher serial and checks BIND serves it. The database
 * change is made in a transaction that is rolled back if the agent refuses.
 */
final class DnsService
{
    public const TYPES = ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'SRV', 'CAA', 'PTR'];
    public const CAA_TAGS = ['issue' => 'issue (may issue certificates)', 'issuewild' => 'issuewild (may issue wildcard certificates)', 'iodef' => 'iodef (report violations to)'];

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
        if (!Scope::isAll($actor)) {
            // This server answers for its zones: a zone under another
            // account's zone (x.their-domain.test) would take names from it.
            $owner = $site !== null && $site['owner_id'] !== null ? (int) $site['owner_id'] : (int) $actor['id'];
            $parts = explode('.', $name);
            for ($i = 1; $i < count($parts) - 1; $i++) {
                $above = $this->db->fetch('SELECT owner_id FROM dns_zones WHERE server_id = ? AND name = ?', [$this->server->id(), implode('.', array_slice($parts, $i))]);
                if ($above !== null && (int) ($above['owner_id'] ?? 0) !== $owner) {
                    throw new ValidationException(['name' => 'This name belongs to a domain of another account.']);
                }
            }
        }
        $ns = $this->nameservers();
        if ($ns === []) {
            throw new UserFacingException('No name servers are configured.', '', 'An administrator sets them under Settings → DNS name servers.');
        }
        $hostmaster = $this->settings->string('dns_hostmaster');
        if ($hostmaster === '' || filter_var($hostmaster, FILTER_VALIDATE_EMAIL) === false) {
            $hostmaster = 'hostmaster@' . $name;
        }
        $glue = $this->glueRecords($name, $ns);
        $ownerId = $site !== null ? ($site['owner_id'] !== null ? (int) $site['owner_id'] : null) : (Scope::isAll($actor) ? null : (int) $actor['id']);
        $now = gmdate('c');
        return $this->db->transaction(function () use ($name, $site, $ownerId, $ns, $hostmaster, $now, $input, $glue): int {
            $this->db->run(
                'INSERT INTO dns_zones (server_id, name, website_id, owner_id, primary_ns, hostmaster, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
                [$this->server->id(), $name, $site['id'] ?? null, $ownerId, $ns[0], $hostmaster, $now, $now]
            );
            $id = $this->db->lastInsertId();
            $records = array_map(static fn (string $n): array => ['name' => '@', 'type' => 'NS', 'value' => $n . '.'], $ns);
            $ip = $this->settings->string('dns_default_ipv4');
            // A name server inside the zone itself (ns1.example.com for example.com) needs an address in it
            // (glue), or the zone does not load: "NS has no address records".
            foreach ($glue as $label => $address) {
                $records[] = ['name' => $label, 'type' => 'A', 'value' => $address];
            }
            if (!empty($input['template'])) {
                $hasIp = filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false;
                if ($hasIp && ($glue['@'] ?? null) !== $ip) {
                    $records[] = ['name' => '@', 'type' => 'A', 'value' => $ip];
                    $records[] = ['name' => 'www', 'type' => 'CNAME', 'value' => '@'];
                }
                if ($input['spf'] ?? true) {
                    $records[] = ['name' => '@', 'type' => 'TXT', 'value' => self::spf($hasIp ? $ip : null)];
                }
                if ($input['dmarc'] ?? false) {
                    $records[] = ['name' => '_dmarc', 'type' => 'TXT', 'value' => 'v=DMARC1; p=none;'];
                }
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
     * Addresses for the configured name servers that lie inside the zone: label => IPv4. The address is the
     * "Default IPv4" setting, or else the server's own first public address.
     *
     * @param list<string> $nameservers
     * @return array<string, string>
     */
    private function glueRecords(string $zone, array $nameservers): array
    {
        $inside = [];
        foreach ($nameservers as $n) {
            if ($n === $zone) {
                $inside['@'] = true;
            } elseif (str_ends_with($n, '.' . $zone)) {
                $inside[substr($n, 0, -strlen('.' . $zone))] = true;
            }
        }
        if ($inside === []) {
            return [];
        }
        $ip = trim($this->settings->string('dns_default_ipv4'));
        if (filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false) {
            $ip = $this->serverIpv4();
        }
        if ($ip === null) {
            throw new UserFacingException(
                'The zone cannot be created yet: its name servers are inside it.',
                'The name servers ' . implode(', ', $nameservers) . ' are names in ' . $zone . ', so the zone needs an address for them, and this server\'s address is not known.',
                'An administrator sets the public address under Settings → DNS (Default IPv4 for new zones), then creates the zone again.'
            );
        }
        return array_fill_keys(array_keys($inside), $ip);
    }

    /** The server's first global IPv4 address, or null when it cannot be read. */
    private function serverIpv4(): ?string
    {
        try {
            foreach ((array) ($this->agent->call('net.addresses')['addresses'] ?? []) as $a) {
                $ip = (string) ($a['address'] ?? '');
                if ((int) ($a['family'] ?? 0) === 4 && ($a['scope'] ?? '') === 'global' && filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false) {
                    return $ip;
                }
            }
        } catch (\Throwable) {
            // not reachable: the caller explains what to set
        }
        return null;
    }

    /** SPF: the domain's web and mail servers, and this server's address. */
    public static function spf(?string $ipv4): string
    {
        return 'v=spf1 +a +mx' . ($ipv4 !== null ? ' +ip4:' . $ipv4 : '') . ' ~all';
    }

    private const HOST_RE = '/^(@|\.|([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?\.)*[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?\.?)$/';

    /**
     * Validate a record from the form: each type has its own fields and
     * checks (IPv4, IPv6, host names, MX priority, TXT, CAA, SRV, TTL).
     *
     * @param array<string, mixed> $in
     * @return array{name: string, type: string, ttl: int, value: string, priority: int, weight: int, port: int}
     */
    public function cleanRecord(array $in, string $zone): array
    {
        $str = static fn (string $k): string => is_scalar($in[$k] ?? null) ? trim((string) $in[$k]) : '';
        $num = static fn (string $k, int $d = 0): ?int => ($v = $str($k)) === '' ? $d : (preg_match('/^\d{1,6}$/', $v) ? (int) $v : null);
        $type = strtoupper($str('type'));
        $name = strtolower($str('name'));
        // SRV: _service._protocol[.name]
        if ($type === 'SRV' && ($str('srv_service') !== '' || $str('srv_protocol') !== '')) {
            $svc = ltrim(strtolower($str('srv_service')), '_');
            $proto = ltrim(strtolower($str('srv_protocol')), '_');
            $name = '_' . $svc . '._' . $proto . ($name !== '' && $name !== '@' ? '.' . $name : '');
        }
        $name = $name === '' || $name === $zone || $name === $zone . '.' ? '@' : (string) preg_replace('/\.' . preg_quote($zone, '/') . '\.?$/', '', $name);
        $errors = [];
        if (!in_array($type, self::TYPES, true)) {
            $errors['type'] = 'Choose a record type.';
        }
        if ($name !== '@' && $name !== '*' && !preg_match('/^(\*\.)?([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?\.)*[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$/', $name)) {
            $errors['name'] = 'Use @ for the domain itself, or a name such as www, mail or *.dev.';
        }
        $ttl = $num('ttl');
        if ($ttl === null || ($ttl !== 0 && ($ttl < 60 || $ttl > 604800))) {
            $errors['ttl'] = 'Use 60–604800 seconds, or 0 for the zone default.';
            $ttl = 0;
        }
        $value = $str('value');
        $priority = $num('priority');
        $weight = $num('weight');
        $port = $num('port');
        $host = static fn (string $h): bool => strlen($h) <= 253 && preg_match(self::HOST_RE, strtolower($h)) === 1;
        switch ($type) {
            case 'A':
                if (filter_var($value, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false) {
                    $errors['value'] = 'Enter an IPv4 address such as 192.0.2.10.';
                }
                break;
            case 'AAAA':
                if (filter_var($value, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6) === false) {
                    $errors['value'] = 'Enter an IPv6 address such as 2001:db8::10.';
                }
                break;
            case 'CNAME':
            case 'NS':
            case 'PTR':
                $value = strtolower($value);
                if ($value === '' || $value === '.' || !$host($value) || filter_var(rtrim($value, '.'), FILTER_VALIDATE_IP) !== false) {
                    $errors['value'] = 'Enter a host name (not an address), e.g. ' . ($type === 'NS' ? 'ns1.example.com.' : 'www.example.com.') . ' A name ending in a dot is absolute.';
                }
                if ($type === 'PTR' && !str_ends_with($zone, '.arpa')) {
                    $errors['type'] = 'PTR records belong in reverse zones (in-addr.arpa or ip6.arpa).';
                }
                break;
            case 'MX':
                $value = strtolower($value);
                if ($value === '' || !$host($value) || filter_var(rtrim($value, '.'), FILTER_VALIDATE_IP) !== false) {
                    $errors['value'] = 'Enter the mail server\'s host name, not an IP address.';
                }
                if ($priority === null || $priority > 65535) {
                    $errors['priority'] = 'Priority: 0–65535 (lower is preferred).';
                }
                break;
            case 'SRV':
                $value = strtolower($value);
                if ($value === '' || !$host($value)) {
                    $errors['value'] = 'Enter the target host name (or . for “no service”).';
                }
                foreach (['priority' => $priority, 'weight' => $weight, 'port' => $port] as $k => $v) {
                    if ($v === null || $v > 65535) {
                        $errors[$k] = ucfirst($k) . ': 0–65535.';
                    }
                }
                if (!preg_match('/^_[a-z0-9-]{1,63}\._(tcp|udp|tls|sctp)(\.|$)/', $name)) {
                    $errors['name'] = 'An SRV name is _service._protocol, e.g. _sip._tcp (optionally followed by a name).';
                }
                break;
            case 'TXT':
                // Pasted with surrounding quotes: keep the text only.
                if (strlen($value) >= 2 && $value[0] === '"' && str_ends_with($value, '"') && substr_count($value, '"') === 2) {
                    $value = substr($value, 1, -1);
                }
                if ($value === '' || strlen($value) > 4000 || preg_match('/[\x00-\x1F]/', $value)) {
                    $errors['value'] = 'Enter the text on one line (up to 4000 characters).';
                }
                break;
            case 'CAA':
                $tag = $str('caa_tag') !== '' ? strtolower($str('caa_tag')) : strtolower(explode(' ', $value, 2)[0]);
                $cv = $str('caa_tag') !== '' ? $str('caa_value') : trim(explode(' ', $value, 2)[1] ?? '');
                $cv = trim($cv, '"');
                $flags = $str('caa_flags') !== '' ? $num('caa_flags') : $priority;
                if (!isset(self::CAA_TAGS[$tag])) {
                    $errors['caa_tag'] = 'Choose issue, issuewild or iodef.';
                } elseif ($tag === 'iodef' ? !preg_match('#^(mailto:[^@\s]+@[^@\s]+|https?://\S+)$#', $cv) : !preg_match('/^(;|[a-z0-9.-]+\.[a-z]{2,}(\s*;.*)?)$/i', $cv)) {
                    $errors['caa_value'] = $tag === 'iodef' ? 'Enter mailto:address or an https:// URL.' : 'Enter the certificate authority\'s domain, e.g. letsencrypt.org (or ; to allow none).';
                }
                if ($flags !== 0 && $flags !== 128) {
                    $errors['caa_flags'] = 'Flags: 0, or 128 (critical).';
                }
                $value = $tag . ' ' . $cv;
                $priority = $flags;
                break;
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $keepPrio = in_array($type, ['MX', 'SRV', 'CAA'], true);
        return ['name' => $name, 'type' => $type, 'ttl' => (int) $ttl, 'value' => $value, 'priority' => $keepPrio ? (int) $priority : 0, 'weight' => $type === 'SRV' ? (int) $weight : 0, 'port' => $type === 'SRV' ? (int) $port : 0];
    }

    /**
     * Conflicts with the zone's other (enabled) records.
     *
     * @param array{name: string, type: string, value: string, priority: int} $r
     * @param list<array<string, mixed>> $others
     */
    public static function conflicts(array $r, array $others): ?string
    {
        foreach ($others as $o) {
            if (!empty($o['disabled']) || $o['name'] !== $r['name']) {
                continue;
            }
            if ($r['type'] === 'CNAME' || $o['type'] === 'CNAME') {
                return $r['name'] . ' already has ' . ($o['type'] === 'CNAME' ? 'a CNAME record' : 'other records') . '; a CNAME must be the only record of its name.';
            }
            if ($o['type'] === $r['type'] && strtolower((string) $o['value']) === strtolower($r['value']) && (int) $o['priority'] === $r['priority']) {
                return 'The same record already exists.';
            }
        }
        if ($r['type'] === 'CNAME' && $r['name'] === '@') {
            return 'The domain itself (@) cannot be a CNAME; use an A/AAAA record.';
        }
        return null;
    }

    /**
     * Common mistakes in the zone (warnings, not errors).
     *
     * @param list<array<string, mixed>> $records
     * @return list<string>
     */
    public static function warnings(string $zone, array $records): array
    {
        $on = array_values(array_filter($records, static fn (array $r): bool => empty($r['disabled'])));
        $by = static fn (string $name, string $type): array => array_values(array_filter($on, static fn (array $r): bool => $r['name'] === $name && $r['type'] === $type));
        $rel = static function (string $host) use ($zone): ?string {
            $h = strtolower(rtrim($host, '.'));
            if (!str_ends_with($host, '.')) {
                return $host === '@' ? '@' : $h;
            }
            return $h === $zone ? '@' : (str_ends_with($h, '.' . $zone) ? substr($h, 0, -strlen('.' . $zone)) : null);
        };
        $w = [];
        if (count($by('@', 'NS')) < 2) {
            $w[] = 'The zone has fewer than two NS records at @; most registries require at least two name servers.';
        }
        foreach ($on as $r) {
            $name = $r['name'] === '@' ? $zone : $r['name'] . '.' . $zone;
            if (in_array($r['type'], ['MX', 'NS'], true) && ($t = $rel((string) $r['value'])) !== null && $by($t, 'CNAME') !== []) {
                $w[] = $r['type'] . ' ' . $name . ' points to ' . $r['value'] . ', which is a CNAME; ' . $r['type'] . ' targets must have A/AAAA records.';
            }
            if (in_array($r['type'], ['MX', 'NS', 'CNAME'], true) && ($t = $rel((string) $r['value'])) !== null && $t !== '@' && !str_contains($t, '*') && $by($t, 'A') === [] && $by($t, 'AAAA') === [] && $by($t, 'CNAME') === [] && !str_ends_with((string) $r['value'], '.')) {
                $w[] = $r['type'] . ' ' . $name . ' points to ' . $r['value'] . '.' . $zone . ', which has no record in this zone. For a host elsewhere, end the name with a dot.';
            }
            if ($r['type'] === 'A' && filter_var((string) $r['value'], FILTER_VALIDATE_IP, FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE) === false) {
                $w[] = 'A ' . $name . ' is a private or reserved address (' . $r['value'] . '); visitors on the internet cannot reach it.';
            }
            if ((int) $r['ttl'] > 0 && (int) $r['ttl'] < 300) {
                $w[] = $r['type'] . ' ' . $name . ' has a TTL under 5 minutes, which increases DNS traffic; keep short TTLs for planned changes only.';
            }
        }
        $spf = array_filter($by('@', 'TXT'), static fn (array $r): bool => str_starts_with(strtolower((string) $r['value']), 'v=spf1'));
        if (count($spf) > 1) {
            $w[] = 'There are ' . count($spf) . ' SPF records at @; receivers treat several as an error. Merge them into one.';
        }
        if ($by('@', 'MX') !== [] && $spf === []) {
            $w[] = 'The domain receives mail (MX) but has no SPF record; mail from it is more likely to be marked as spam.';
        }
        foreach ($spf as $r) {
            if (preg_match('/\+all\b/i', (string) $r['value'])) {
                $w[] = 'The SPF record ends in +all, which lets anyone send mail as this domain.';
            }
        }
        return array_values(array_unique($w));
    }

    /** @param array<string, mixed> $r */
    private function insertRecord(int $zoneId, array $r): void
    {
        $this->db->run(
            'INSERT INTO dns_records (zone_id, name, type, ttl, value, priority, weight, port, disabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [$zoneId, $r['name'], $r['type'], $r['ttl'], $r['value'], $r['priority'], $r['weight'], $r['port'], (int) !empty($r['disabled'])]
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
                $others = array_values(array_filter($this->records($zoneId), static fn (array $o): bool => (int) $o['id'] !== (int) $recordId));
                if (($conflict = self::conflicts($after, $others)) !== null) {
                    throw new ValidationException(['name' => $conflict]);
                }
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
        } catch (\NimVor\Agent\AgentException $e) {
            if ($e->status() !== 404) {
                throw $e;
            }
        }
        $this->db->run('DELETE FROM dns_zones WHERE id = ?', [$zone['id']]);
        $this->audit->record('dns.zone_delete', 'dns_zone', (string) $zone['id'], AuditLogger::SUCCESS, ['zone' => $zone['name']]);
    }

    /** Send the complete zone to the agent and remember the serial. */
    /**
     * Point a zone's A or AAAA records from one address to another in one
     * reload: records with value $old (and one of $names, when given) get
     * $new ($new '' deletes them). With no $old, records are added for
     * $names that have an A record but none of $type yet. Nothing changes
     * if the DNS server refuses.
     *
     * @param list<string> $names
     * @return int records changed
     */
    public function replaceAddress(int $zoneId, string $type, string $old, string $new, array $names = []): int
    {
        if (!in_array($type, ['A', 'AAAA'], true)) {
            throw new UserFacingException('Only A and AAAA records hold addresses.');
        }
        $zone = $this->db->fetch('SELECT * FROM dns_zones WHERE id = ?', [$zoneId]) ?? throw new UserFacingException('The zone no longer exists.');
        $n = 0;
        $this->db->transaction(function () use ($zone, $zoneId, $type, $old, $new, $names, &$n): void {
            if ($old !== '') {
                $rows = $this->db->fetchAll('SELECT id, name FROM dns_records WHERE zone_id = ? AND type = ? AND value = ?', [$zoneId, $type, $old]);
                // With names given, only those records change (a website's own names in a shared zone).
                $rows = $names === [] ? $rows : array_filter($rows, static fn (array $r): bool => in_array((string) $r['name'], $names, true));
                $ids = array_map('intval', array_column($rows, 'id'));
                $n = count($ids);
                foreach ($ids as $id) {
                    $new === ''
                        ? $this->db->run('DELETE FROM dns_records WHERE id = ?', [$id])
                        : $this->db->run('UPDATE dns_records SET value = ? WHERE id = ?', [$new, $id]);
                }
            } elseif ($new !== '') {
                foreach ($names as $name) {
                    $hasA = (int) $this->db->scalar("SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND name = ? AND type = 'A' AND disabled = 0", [$zoneId, $name]);
                    $has = (int) $this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND name = ? AND type = ?', [$zoneId, $name, $type]);
                    if ($hasA > 0 && $has === 0) {
                        $this->insertRecord($zoneId, ['name' => $name, 'type' => $type, 'ttl' => 0, 'value' => $new, 'priority' => 0, 'weight' => 0, 'port' => 0]);
                        $n++;
                    }
                }
            }
            if ($n > 0) {
                $this->push($zoneId);
                $this->audit->record('dns.address_replace', 'dns_zone', (string) $zoneId, AuditLogger::SUCCESS, ['zone' => $zone['name'], 'type' => $type, 'old' => $old, 'new' => $new, 'records' => $n]);
            }
        });
        return $n;
    }

    /**
     * The zone that holds a host name (its own zone, or the closest parent
     * zone) and the name relative to it.
     *
     * @return array{zone: array<string, mixed>, name: string}|null
     */
    public function zoneForName(string $host): ?array
    {
        $host = strtolower(rtrim($host, '.'));
        $labels = explode('.', $host);
        for ($i = 0; $i < count($labels) - 1; $i++) {
            $zoneName = implode('.', array_slice($labels, $i));
            $z = $this->db->fetch('SELECT * FROM dns_zones WHERE name = ?', [$zoneName]);
            if ($z !== null) {
                return ['zone' => $z, 'name' => $i === 0 ? '@' : implode('.', array_slice($labels, 0, $i))];
            }
        }
        return null;
    }

    /**
     * Zones with A or AAAA records holding an address.
     *
     * @return list<array{id: int, name: string, records: int}>
     */
    public function zonesUsingAddress(string $address): array
    {
        return array_map(static fn (array $r): array => ['id' => (int) $r['id'], 'name' => (string) $r['name'], 'records' => (int) $r['n']], $this->db->fetchAll(
            "SELECT z.id, z.name, COUNT(*) AS n FROM dns_records r JOIN dns_zones z ON z.id = r.zone_id WHERE r.type IN ('A', 'AAAA') AND r.value = ? GROUP BY z.id, z.name ORDER BY z.name",
            [$address]
        ));
    }

    private function push(int $zoneId): void
    {
        $z = $this->db->fetch('SELECT * FROM dns_zones WHERE id = ?', [$zoneId]) ?? throw new UserFacingException('The zone no longer exists.');
        $res = (array) $this->agent->call('dns.zone_apply', $this->zonePayload($z), 60);
        $this->db->run('UPDATE dns_zones SET serial = ?, applied_at = ?, updated_at = ? WHERE id = ?', [(int) ($res['serial'] ?? 0), gmdate('c'), gmdate('c'), $zoneId]);
    }

    /**
     * The zone as the agent takes it (enabled records only).
     *
     * @param array<string, mixed> $z
     * @return array<string, mixed>
     */
    private function zonePayload(array $z): array
    {
        $records = [];
        foreach ($this->records((int) $z['id']) as $r) {
            if (empty($r['disabled'])) {
                $records[] = [
                    'name' => $r['name'], 'type' => $r['type'], 'ttl' => (int) $r['ttl'], 'value' => $r['value'],
                    'priority' => (int) $r['priority'], 'weight' => (int) $r['weight'], 'port' => (int) $r['port'],
                ];
            }
        }
        return [
            'zone' => $z['name'], 'ttl' => (int) $z['ttl'],
            'soa' => ['primary_ns' => $z['primary_ns'] . '.', 'hostmaster' => $z['hostmaster']],
            'records' => $records,
            'secondaries' => $z['secondaries'] === '' ? [] : explode(',', (string) $z['secondaries']),
        ];
    }

    /**
     * Enable, disable or delete several records at once (one zone reload;
     * nothing changes if the DNS server refuses the result).
     *
     * @param array<string, mixed> $zone
     * @param list<int> $ids
     */
    public function bulk(array $zone, array $ids, string $op): int
    {
        if (!in_array($op, ['enable', 'disable', 'delete'], true)) {
            throw new UserFacingException('Unknown operation.');
        }
        $zoneId = (int) $zone['id'];
        $ids = array_values(array_unique(array_filter(array_map('intval', $ids), static fn (int $i): bool => $i > 0)));
        if ($ids === []) {
            throw new UserFacingException('Select records first.');
        }
        $n = 0;
        $this->db->transaction(function () use ($zone, $zoneId, $ids, $op, &$n): void {
            $in = implode(',', array_fill(0, count($ids), '?'));
            $args = array_merge([$zoneId], $ids);
            $n = count($this->db->fetchAll("SELECT id FROM dns_records WHERE zone_id = ? AND id IN ({$in})", $args));
            if ($n === 0) {
                throw new UserFacingException('The selected records are not in this zone.');
            }
            match ($op) {
                'delete' => $this->db->run("DELETE FROM dns_records WHERE zone_id = ? AND id IN ({$in})", $args),
                default => $this->db->run("UPDATE dns_records SET disabled = ? WHERE zone_id = ? AND id IN ({$in})", array_merge([$op === 'disable' ? 1 : 0], $args)),
            };
            $this->push($zoneId);
            $this->audit->record('dns.records_' . $op, 'dns_zone', (string) $zoneId, AuditLogger::SUCCESS, ['zone' => $zone['name'], 'count' => $n]);
        });
        return $n;
    }

    /**
     * Import a zone file: its records that are not in the zone yet are
     * added (SOA and unsupported lines are skipped, and reported).
     *
     * @param array<string, mixed> $zone
     * @return array{added: int, duplicates: int, skipped: list<array<string, mixed>>}
     */
    public function import(array $zone, string $text): array
    {
        if (trim($text) === '' || strlen($text) > 1048576) {
            throw new ValidationException(['zonefile' => 'Paste or upload a zone file of up to 1 MB.']);
        }
        $parsed = (array) json_decode((string) json_encode($this->agent->call('dns.zone_parse', ['zone' => $zone['name'], 'text' => $text], 60)), true);
        $zoneId = (int) $zone['id'];
        $added = $dups = 0;
        $skipped = array_values((array) ($parsed['skipped'] ?? []));
        $this->db->transaction(function () use ($zone, $zoneId, $parsed, &$added, &$dups, &$skipped): void {
            $existing = $this->records($zoneId);
            $key = static fn (array $r): string => strtolower($r['name'] . '|' . $r['type'] . '|' . $r['value'] . '|' . (int) ($r['priority'] ?? 0));
            $have = array_flip(array_map($key, $existing));
            foreach ((array) ($parsed['records'] ?? []) as $r) {
                $r = ['name' => (string) $r['name'], 'type' => (string) $r['type'], 'ttl' => (int) ($r['ttl'] ?? 0), 'value' => (string) $r['value'], 'priority' => (int) ($r['priority'] ?? 0), 'weight' => (int) ($r['weight'] ?? 0), 'port' => (int) ($r['port'] ?? 0)];
                if (isset($have[$key($r)]) || ($r['type'] === 'NS' && $r['name'] === '@')) {
                    $dups++; // the zone's own name servers stay
                    continue;
                }
                // What the zone could not hold (a CNAME next to other records) is listed, not added.
                if (($why = self::conflicts($r, $existing)) !== null) {
                    $skipped[] = ['line' => 0, 'text' => $r['name'] . ' ' . $r['type'] . ' ' . $r['value'], 'reason' => $why];
                    continue;
                }
                if (count($existing) >= 2000) {
                    throw new UserFacingException('A zone can hold at most 2000 records.');
                }
                $this->insertRecord($zoneId, $r);
                $existing[] = $r + ['disabled' => 0];
                $have[$key($r)] = true;
                $added++;
            }
            if ($added > 0) {
                $this->push($zoneId);
            }
            $this->audit->record('dns.zone_import', 'dns_zone', (string) $zoneId, AuditLogger::SUCCESS, ['zone' => $zone['name'], 'added' => $added]);
        });
        return ['added' => $added, 'duplicates' => $dups, 'skipped' => $skipped];
    }

    /**
     * The zone file as served (BIND format).
     *
     * @param array<string, mixed> $zone
     */
    public function export(array $zone): string
    {
        $z = $this->db->fetch('SELECT * FROM dns_zones WHERE id = ?', [$zone['id']]) ?? throw new UserFacingException('The zone no longer exists.');
        $res = (array) $this->agent->call('dns.zone_render', $this->zonePayload($z) + ['serial' => (int) $z['serial']], 60);
        return (string) ($res['text'] ?? '');
    }
}
