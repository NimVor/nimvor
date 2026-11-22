<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

/**
 * Opt-in telemetry storage (docs/telemetry/README.md). Nothing here can identify a person, a server's address,
 * a host name or a hosted domain: an installation is a random id, and every report is a fixed set of
 * numbers, versions and yes/no answers.
 */
final class Telemetry
{
    public function __construct(private readonly Db $db)
    {
    }

    // ------------------------------------------------------------------ installations

    /** @return array<string, mixed>|null */
    public function byUuid(string $uuid): ?array
    {
        return $this->db->one('SELECT * FROM telemetry_installations WHERE installation_uuid = ?', [$uuid]);
    }

    public function wasDeleted(string $uuid): bool
    {
        return $this->db->scalar('SELECT 1 FROM telemetry_deleted WHERE uuid_hash = ?', [hash('sha256', $uuid)]) !== null;
    }

    public function create(string $uuid, string $tokenHash, int $schema, string $version, string $agent): int
    {
        $this->db->run('INSERT INTO telemetry_installations (installation_uuid, token_hash, schema_version, nimvor_version, agent_version, registered_at, last_seen) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP(), UTC_TIMESTAMP())', [$uuid, $tokenHash, $schema, $version, $agent]);
        return $this->db->lastId();
    }

    public function replaceToken(int $id, string $tokenHash, string $version, string $agent): void
    {
        $this->db->run('UPDATE telemetry_installations SET token_hash = ?, nimvor_version = ?, agent_version = ?, last_seen = UTC_TIMESTAMP() WHERE id = ?', [$tokenHash, $version, $agent, $id]);
    }

    public function touch(int $id, ?string $version = null, ?string $agent = null): void
    {
        $this->db->run('UPDATE telemetry_installations SET token_confirmed = 1, last_seen = UTC_TIMESTAMP(), nimvor_version = COALESCE(?, nimvor_version), agent_version = COALESCE(?, agent_version) WHERE id = ?', [$version, $agent, $id]);
    }

    /** Stops the reports; what was sent stays (it is anonymous). */
    public function unregister(int $id): void
    {
        $this->db->run("UPDATE telemetry_installations SET status = 'unregistered', unregistered_at = UTC_TIMESTAMP() WHERE id = ?", [$id]);
    }

    /** Erases everything that was reported and leaves only a hash, so a late retry cannot register the id again. */
    public function erase(string $uuid): void
    {
        $this->db->transaction(function () use ($uuid): void {
            $this->db->run('DELETE FROM telemetry_installations WHERE installation_uuid = ?', [$uuid]);
            $this->db->run('INSERT IGNORE INTO telemetry_deleted (uuid_hash, deleted_at) VALUES (?, UTC_TIMESTAMP())', [hash('sha256', $uuid)]);
        });
    }

    // ------------------------------------------------------------------ reports (each returns false for a repeated event)

    public function addHeartbeat(int $id, string $event, string $version, string $agent, ?int $uptimeDays): bool
    {
        return $this->once('INSERT INTO telemetry_heartbeats (installation_id, event_id, received_at, nimvor_version, agent_version, uptime_days) VALUES (?, ?, UTC_TIMESTAMP(), ?, ?, ?)', [$id, $event, $version, $agent, $uptimeDays]);
    }

    /** @param array<string, mixed> $os */
    public function replaceOs(int $id, string $event, array $os): bool
    {
        $same = $this->db->scalar('SELECT 1 FROM telemetry_os_inventory WHERE installation_id = ? AND event_id = ?', [$id, $event]);
        if ($same !== null) {
            return false;
        }
        $this->db->run(
            'INSERT INTO telemetry_os_inventory (installation_id, event_id, captured_at, os_id, os_version, os_family, kernel_major, arch, virtualization, init_system, cpu_cores, memory_mb_bucket, disk_gb_bucket)
             VALUES (?, ?, UTC_TIMESTAMP(), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
             ON DUPLICATE KEY UPDATE event_id = VALUES(event_id), captured_at = VALUES(captured_at), os_id = VALUES(os_id), os_version = VALUES(os_version), os_family = VALUES(os_family), kernel_major = VALUES(kernel_major),
                 arch = VALUES(arch), virtualization = VALUES(virtualization), init_system = VALUES(init_system), cpu_cores = VALUES(cpu_cores), memory_mb_bucket = VALUES(memory_mb_bucket), disk_gb_bucket = VALUES(disk_gb_bucket)',
            [$id, $event, $os['id'], $os['version'], $os['family'], $os['kernel_major'], $os['arch'], $os['virtualization'], $os['init_system'], $os['cpu_cores'], $os['memory_mb_bucket'], $os['disk_gb_bucket']]
        );
        return true;
    }

    /** @param list<array{component: string, version: string, running: ?bool}> $software the full list: what is not in it is no longer installed */
    public function replaceSoftware(int $id, array $software): void
    {
        $this->db->transaction(function () use ($id, $software): void {
            $this->db->run('DELETE FROM telemetry_software_inventory WHERE installation_id = ?', [$id]);
            foreach ($software as $s) {
                $this->db->run('INSERT INTO telemetry_software_inventory (installation_id, component, version, running, captured_at) VALUES (?, ?, ?, ?, UTC_TIMESTAMP())', [$id, $s['component'], $s['version'], $s['running'] === null ? null : (int) $s['running']]);
            }
        });
    }

    /** @param array<string, int> $features */
    public function addFeatures(int $id, string $event, array $features): bool
    {
        if ($this->db->scalar('SELECT 1 FROM telemetry_feature_snapshots WHERE event_id = ? LIMIT 1', [$event]) !== null) {
            return false;
        }
        $this->db->transaction(function () use ($id, $event, $features): void {
            foreach ($features as $name => $n) {
                $this->db->run('INSERT INTO telemetry_feature_snapshots (installation_id, event_id, captured_at, feature, item_count) VALUES (?, ?, UTC_TIMESTAMP(), ?, ?)', [$id, $event, $name, $n]);
            }
        });
        return true;
    }

    /** @param array<string, int|null> $h */
    public function addHealth(int $id, string $event, array $h): bool
    {
        return $this->once(
            'INSERT INTO telemetry_health_snapshots (installation_id, event_id, captured_at, services_total, services_failed, disk_used_bucket, memory_used_bucket, updates_pending, last_backup_ok, ssl_expiring, failed_jobs) VALUES (?, ?, UTC_TIMESTAMP(), ?, ?, ?, ?, ?, ?, ?, ?)',
            [$id, $event, $h['services_total'], $h['services_failed'], $h['disk_used_bucket'], $h['memory_used_bucket'], $h['updates_pending'], $h['last_backup_ok'], $h['ssl_expiring'], $h['failed_jobs']]
        );
    }

    public function addRelease(int $id, string $event, string $kind, string $from, string $to, bool $ok, ?string $when): bool
    {
        return $this->once('INSERT INTO telemetry_release_events (installation_id, event_id, occurred_at, kind, from_version, to_version, success) VALUES (?, ?, COALESCE(?, UTC_TIMESTAMP()), ?, ?, ?, ?)', [$id, $event, $when, $kind, $from, $to, (int) $ok]);
    }

    public function logApi(string $endpoint, int $status, string $code, ?int $schema): void
    {
        $this->db->run('INSERT INTO telemetry_api_events (received_at, endpoint, status, code, schema_version) VALUES (UTC_TIMESTAMP(), ?, ?, ?, ?)', [$endpoint, $status, $code, $schema]);
    }

    /** @param list<mixed> $params */
    private function once(string $sql, array $params): bool
    {
        try {
            $this->db->run($sql, $params);
            return true;
        } catch (\PDOException $e) {
            if ((int) ($e->errorInfo[1] ?? 0) === 1062) {
                return false;
            }
            throw $e;
        }
    }

    // ------------------------------------------------------------------ retention

    /** @return array<string, int> rows removed per table */
    public function purge(int $heartbeatDays = 90, int $snapshotDays = 180, int $apiDays = 30, int $inactiveDays = 400): array
    {
        $out = [];
        $out['heartbeats'] = $this->db->run('DELETE FROM telemetry_heartbeats WHERE received_at < DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY)', [$heartbeatDays]);
        $out['features'] = $this->db->run('DELETE FROM telemetry_feature_snapshots WHERE captured_at < DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY)', [$snapshotDays]);
        $out['health'] = $this->db->run('DELETE FROM telemetry_health_snapshots WHERE captured_at < DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY)', [$snapshotDays]);
        $out['api_events'] = $this->db->run('DELETE FROM telemetry_api_events WHERE received_at < DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY)', [$apiDays]);
        $out['installations'] = $this->db->run('DELETE FROM telemetry_installations WHERE last_seen < DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY)', [$inactiveDays]);
        return $out;
    }

    // ------------------------------------------------------------------ the developer dashboard

    /**
     * Installations that reported in the last $days days, optionally of one version / one operating system.
     *
     * @param array{version?: string, os?: string} $f
     * @return array{0: string, 1: list<mixed>} a WHERE fragment on the alias "i" and its parameters
     */
    private function scope(int $days, array $f): array
    {
        $sql = "i.status = 'active' AND i.last_seen >= DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY)";
        $p = [$days];
        if (($f['version'] ?? '') !== '') {
            $sql .= ' AND i.nimvor_version = ?';
            $p[] = $f['version'];
        }
        if (($f['os'] ?? '') !== '') {
            $sql .= ' AND EXISTS (SELECT 1 FROM telemetry_os_inventory o WHERE o.installation_id = i.id AND CONCAT(o.os_id, \' \', o.os_version) = ?)';
            $p[] = $f['os'];
        }
        return [$sql, $p];
    }

    /** @return array{registered: int, active: int, active_7: int, unregistered: int, new_period: int, reporting_inventory: int, reporting_health: int, first: ?string} */
    public function totals(int $days, array $f = []): array
    {
        [$w, $p] = $this->scope($days, $f);
        $r = $this->db->one("SELECT COUNT(*) AS active, SUM(i.last_seen >= DATE_SUB(UTC_TIMESTAMP(), INTERVAL 7 DAY)) AS active7, SUM(i.registered_at >= DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY)) AS fresh FROM telemetry_installations i WHERE {$w}", [$days, ...$p]) ?? [];
        $all = $this->db->one("SELECT COUNT(*) AS n, SUM(status = 'unregistered') AS gone, MIN(registered_at) AS first FROM telemetry_installations") ?? [];
        $inv = (int) $this->db->scalar("SELECT COUNT(*) FROM telemetry_installations i JOIN telemetry_os_inventory o ON o.installation_id = i.id WHERE {$w}", $p);
        $hl = (int) $this->db->scalar("SELECT COUNT(DISTINCT i.id) FROM telemetry_installations i JOIN telemetry_health_snapshots h ON h.installation_id = i.id WHERE {$w}", $p);
        return [
            'registered' => (int) ($all['n'] ?? 0), 'active' => (int) ($r['active'] ?? 0), 'active_7' => (int) ($r['active7'] ?? 0), 'unregistered' => (int) ($all['gone'] ?? 0),
            'new_period' => (int) ($r['fresh'] ?? 0), 'reporting_inventory' => $inv, 'reporting_health' => $hl, 'first' => isset($all['first']) ? (string) $all['first'] : null,
        ];
    }

    /**
     * Installations per value of a column.
     *
     * @param 'version'|'agent'|'os'|'arch'|'virtualization'|'cpu'|'memory'|'disk'|'init' $by
     * @return array<string, int> value => installations, most first
     */
    public function breakdown(string $by, int $days, array $f = []): array
    {
        [$w, $p] = $this->scope($days, $f);
        $map = [
            'version' => ['i.nimvor_version', ''],
            'agent' => ["NULLIF(i.agent_version, '')", ''],
            'os' => ["CONCAT(o.os_id, ' ', o.os_version)", 'JOIN telemetry_os_inventory o ON o.installation_id = i.id'],
            'arch' => ['o.arch', 'JOIN telemetry_os_inventory o ON o.installation_id = i.id'],
            'virtualization' => ['o.virtualization', 'JOIN telemetry_os_inventory o ON o.installation_id = i.id'],
            'init' => ['o.init_system', 'JOIN telemetry_os_inventory o ON o.installation_id = i.id'],
            'cpu' => ['o.cpu_cores', 'JOIN telemetry_os_inventory o ON o.installation_id = i.id'],
            'memory' => ['o.memory_mb_bucket', 'JOIN telemetry_os_inventory o ON o.installation_id = i.id'],
            'disk' => ['o.disk_gb_bucket', 'JOIN telemetry_os_inventory o ON o.installation_id = i.id'],
        ];
        if (!isset($map[$by])) {
            throw new \InvalidArgumentException('unknown breakdown');
        }
        [$expr, $join] = $map[$by];
        $rows = $this->db->all("SELECT {$expr} AS k, COUNT(*) AS n FROM telemetry_installations i {$join} WHERE {$w} AND {$expr} IS NOT NULL GROUP BY k ORDER BY n DESC, k DESC LIMIT 20", $p);
        return array_map('intval', array_column($rows, 'n', 'k'));
    }

    /** @return array<string, array{installed: int, running: int}> component => installations (with the software list reported) */
    public function software(int $days, array $f = []): array
    {
        [$w, $p] = $this->scope($days, $f);
        $rows = $this->db->all("SELECT s.component AS k, COUNT(*) AS n, SUM(s.running = 1) AS up FROM telemetry_installations i JOIN telemetry_software_inventory s ON s.installation_id = i.id WHERE {$w} GROUP BY s.component ORDER BY n DESC, k LIMIT 40", $p);
        $out = [];
        foreach ($rows as $r) {
            $out[(string) $r['k']] = ['installed' => (int) $r['n'], 'running' => (int) $r['up']];
        }
        return $out;
    }

    /** @return array<string, int> version => installations, for one component */
    public function componentVersions(string $component, int $days, array $f = []): array
    {
        [$w, $p] = $this->scope($days, $f);
        $rows = $this->db->all("SELECT s.version AS k, COUNT(*) AS n FROM telemetry_installations i JOIN telemetry_software_inventory s ON s.installation_id = i.id WHERE {$w} AND s.component = ? GROUP BY s.version ORDER BY n DESC LIMIT 12", [...$p, $component]);
        return array_map('intval', array_column($rows, 'n', 'k'));
    }

    /**
     * For each feature, the installations whose latest snapshot has it in use, and the total and mean number of items.
     *
     * @return array<string, array{using: int, reporting: int, total: int}>
     */
    public function features(int $days, array $f = []): array
    {
        [$w, $p] = $this->scope($days, $f);
        $reporting = (int) $this->db->scalar("SELECT COUNT(DISTINCT s.installation_id) FROM telemetry_installations i JOIN telemetry_feature_snapshots s ON s.installation_id = i.id WHERE {$w}", $p);
        $rows = $this->db->all(
            "SELECT s.feature AS k, SUM(s.item_count > 0) AS using_it, SUM(s.item_count) AS total
             FROM telemetry_installations i JOIN telemetry_feature_snapshots s ON s.installation_id = i.id
             WHERE {$w} AND s.captured_at = (SELECT MAX(x.captured_at) FROM telemetry_feature_snapshots x WHERE x.installation_id = i.id)
             GROUP BY s.feature ORDER BY using_it DESC, k",
            $p
        );
        $out = [];
        foreach ($rows as $r) {
            $out[(string) $r['k']] = ['using' => (int) $r['using_it'], 'reporting' => $reporting, 'total' => (int) $r['total']];
        }
        return $out;
    }

    /** @return array{reporting: int, with_failed_services: int, updates_pending: int, backup_failing: int, ssl_expiring: int, disk_high: int, failed_jobs: int} from each installation's latest health report */
    public function health(int $days, array $f = []): array
    {
        [$w, $p] = $this->scope($days, $f);
        $r = $this->db->one(
            "SELECT COUNT(*) AS n, SUM(h.services_failed > 0) AS failed, SUM(h.updates_pending = 1) AS upd, SUM(h.last_backup_ok = 0) AS bk, SUM(h.ssl_expiring = 1) AS ssl_exp, SUM(h.disk_used_bucket >= 90) AS disk, SUM(h.failed_jobs > 0) AS jobs
             FROM telemetry_installations i JOIN telemetry_health_snapshots h ON h.installation_id = i.id
             WHERE {$w} AND h.captured_at = (SELECT MAX(x.captured_at) FROM telemetry_health_snapshots x WHERE x.installation_id = i.id)",
            $p
        ) ?? [];
        return ['reporting' => (int) ($r['n'] ?? 0), 'with_failed_services' => (int) ($r['failed'] ?? 0), 'updates_pending' => (int) ($r['upd'] ?? 0), 'backup_failing' => (int) ($r['bk'] ?? 0), 'ssl_expiring' => (int) ($r['ssl_exp'] ?? 0), 'disk_high' => (int) ($r['disk'] ?? 0), 'failed_jobs' => (int) ($r['jobs'] ?? 0)];
    }

    /** @return array{upgrades: int, upgrades_ok: int, installs: int, installs_ok: int, rollbacks: int} in the period */
    public function releases(int $days): array
    {
        $r = $this->db->one(
            "SELECT SUM(kind = 'upgrade') AS up, SUM(kind = 'upgrade' AND success = 1) AS upok, SUM(kind = 'install') AS ins, SUM(kind = 'install' AND success = 1) AS insok, SUM(kind = 'rollback') AS rb
             FROM telemetry_release_events WHERE occurred_at >= DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY)",
            [$days]
        ) ?? [];
        return ['upgrades' => (int) ($r['up'] ?? 0), 'upgrades_ok' => (int) ($r['upok'] ?? 0), 'installs' => (int) ($r['ins'] ?? 0), 'installs_ok' => (int) ($r['insok'] ?? 0), 'rollbacks' => (int) ($r['rb'] ?? 0)];
    }

    /** @return array<string, int> Y-m-d => installations that reported that day, the last $days days, zeros included */
    public function activePerDay(int $days): array
    {
        $rows = $this->db->all('SELECT DATE(received_at) AS d, COUNT(DISTINCT installation_id) AS n FROM telemetry_heartbeats WHERE received_at >= DATE_SUB(UTC_DATE(), INTERVAL ? DAY) GROUP BY d', [$days - 1]);
        $by = array_column($rows, 'n', 'd');
        $out = [];
        for ($i = $days - 1; $i >= 0; $i--) {
            $d = gmdate('Y-m-d', time() - $i * 86400);
            $out[$d] = (int) ($by[$d] ?? 0);
        }
        return $out;
    }

    /** @return array<string, int> Y-m-d => new registrations */
    public function registrationsPerDay(int $days): array
    {
        $rows = $this->db->all('SELECT DATE(registered_at) AS d, COUNT(*) AS n FROM telemetry_installations WHERE registered_at >= DATE_SUB(UTC_DATE(), INTERVAL ? DAY) GROUP BY d', [$days - 1]);
        $by = array_column($rows, 'n', 'd');
        $out = [];
        for ($i = $days - 1; $i >= 0; $i--) {
            $d = gmdate('Y-m-d', time() - $i * 86400);
            $out[$d] = (int) ($by[$d] ?? 0);
        }
        return $out;
    }

    /** @return list<string> */
    public function versions(): array
    {
        return array_map('strval', array_column($this->db->all("SELECT DISTINCT nimvor_version AS v FROM telemetry_installations WHERE status = 'active' ORDER BY v DESC LIMIT 50"), 'v'));
    }

    /** @return list<string> */
    public function systems(): array
    {
        return array_map('strval', array_column($this->db->all("SELECT DISTINCT CONCAT(o.os_id, ' ', o.os_version) AS s FROM telemetry_os_inventory o ORDER BY s LIMIT 50"), 's'));
    }

    /** @return array<string, int> endpoint/status => requests in the last $days days */
    public function apiHealth(int $days): array
    {
        $rows = $this->db->all("SELECT CONCAT(endpoint, ' ', status) AS k, COUNT(*) AS n FROM telemetry_api_events WHERE received_at >= DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY) GROUP BY k ORDER BY n DESC LIMIT 30", [$days]);
        return array_map('intval', array_column($rows, 'n', 'k'));
    }
}
