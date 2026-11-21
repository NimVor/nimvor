<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;

/**
 * What the dashboard's database, cache and web stack widgets show, from the
 * agent's existing read-only actions (and php.fpm_health, which counts events
 * in the PHP-FPM logs). A source that cannot answer gives null, shown as N/A.
 */
final class ServiceOverviewService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly Connection $db,
        private readonly ServerContext $server,
    ) {
    }

    /** @param array<string, mixed> $params @return array<string, mixed>|null */
    private function read(string $action, array $params = [], int $timeout = 30): ?array
    {
        try {
            return (array) json_decode((string) json_encode($this->agent->call($action, $params, $timeout)), true);
        } catch (AgentException) {
            return null;
        }
    }

    private static function ratio(?float $part, ?float $whole): ?float
    {
        return $part === null || $whole === null || $whole <= 0 ? null : round(100 * $part / $whole, 1);
    }

    /**
     * MariaDB/MySQL and PostgreSQL: running, connections against the limit,
     * slow queries, cache hit ratio, lock waits.
     *
     * @return array<string, mixed>
     */
    public function databases(): array
    {
        $my = $this->read('db.server_status');
        $pgStatus = $this->read('pg.status');
        if ($my === null && $pgStatus === null) {
            throw new AgentException('Database status unavailable');
        }
        $out = ['mysql' => null, 'postgres' => null];
        if ($my !== null && (!empty($my['running']) || !empty($my['unit']))) {
            $st = (array) ($my['status'] ?? []);
            $vars = (array) ($my['variables'] ?? []);
            $num = static fn (string $k): ?float => isset($st[$k]) && is_numeric($st[$k]) ? (float) $st[$k] : null;
            $max = isset($vars['max_connections']) && is_numeric($vars['max_connections']) ? (int) $vars['max_connections'] : null;
            $tmp = $num('Created_tmp_tables');
            $out['mysql'] = [
                'running' => !empty($my['running']),
                'version' => (string) ($vars['version'] ?? ''),
                'uptime' => $num('Uptime') === null ? null : (int) $num('Uptime'),
                'connections' => $num('Threads_connected') === null ? null : (int) $num('Threads_connected'),
                'max_connections' => $max,
                'running_threads' => $num('Threads_running') === null ? null : (int) $num('Threads_running'),
                'slow_queries' => $num('Slow_queries') === null ? null : (int) $num('Slow_queries'),
                'aborted_connects' => $num('Aborted_connects') === null ? null : (int) $num('Aborted_connects'),
                'buffer_pool_hit' => $num('Innodb_buffer_pool_read_requests') === null || $num('Innodb_buffer_pool_reads') === null
                    ? null : self::ratio($num('Innodb_buffer_pool_read_requests') - $num('Innodb_buffer_pool_reads'), $num('Innodb_buffer_pool_read_requests')),
                'tmp_on_disk' => self::ratio($num('Created_tmp_disk_tables'), $tmp),
                'table_lock_waits' => $num('Table_locks_waited') === null ? null : (int) $num('Table_locks_waited'),
                'databases' => isset($my['databases']) ? (int) $my['databases'] : null,
                'bytes' => isset($my['bytes']) ? (int) $my['bytes'] : null,
                'error' => (string) ($my['error'] ?? ''),
            ];
        }
        if ($pgStatus !== null && !empty($pgStatus['installed'])) {
            $pg = ['running' => !empty($pgStatus['running']), 'version' => (string) ($pgStatus['version'] ?? ''), 'connections' => null, 'max_connections' => null, 'lock_waits' => null, 'long_running' => null, 'cache_hit' => null, 'uptime' => null];
            if ($pg['running'] && ($mon = $this->read('pg.monitor')) !== null) {
                $pg['connections'] = isset($mon['connections']['total']) ? (int) $mon['connections']['total'] : null;
                $pg['max_connections'] = isset($mon['max_connections']) ? (int) $mon['max_connections'] : null;
                $pg['lock_waits'] = isset($mon['lock_waits']) ? count((array) $mon['lock_waits']) : null;
                $pg['long_running'] = isset($mon['long_running']) ? count((array) $mon['long_running']) : null;
                $pg['uptime'] = isset($mon['uptime_seconds']) ? (int) $mon['uptime_seconds'] : null;
                $hit = $read = 0;
                foreach ((array) ($mon['databases'] ?? []) as $d) {
                    $hit += (int) ((array) $d)['blks_hit'];
                    $read += (int) ((array) $d)['blks_read'];
                }
                $pg['cache_hit'] = self::ratio((float) $hit, (float) ($hit + $read));
            }
            $out['postgres'] = $pg;
        }
        $out['_state'] = $out['mysql'] === null && $out['postgres'] === null ? 'empty' : 'ok';
        return $out;
    }

    /**
     * Redis and Memcached: memory, hit ratio, clients, evictions.
     *
     * @return array<string, mixed>
     */
    public function caches(): array
    {
        $redis = $this->read('redis.info');
        $mem = $this->read('memcached.status');
        if ($redis === null && $mem === null) {
            throw new AgentException('Cache status unavailable');
        }
        $out = ['redis' => null, 'memcached' => null];
        if ($redis !== null && isset($redis['info'])) {
            $i = (array) $redis['info'];
            $stats = (array) ($i['stats'] ?? []);
            $hits = isset($stats['keyspace_hits']) ? (float) $stats['keyspace_hits'] : null;
            $miss = isset($stats['keyspace_misses']) ? (float) $stats['keyspace_misses'] : null;
            $keys = 0;
            foreach ((array) ($i['keyspace'] ?? []) as $db) {
                $keys += (int) (is_array($db) ? ($db['keys'] ?? 0) : (preg_match('/keys=(\d+)/', (string) $db, $m) ? $m[1] : 0));
            }
            $max = (int) ($redis['maxmemory'] ?? 0);
            $used = isset($i['memory']['used_memory']) ? (int) $i['memory']['used_memory'] : null;
            $out['redis'] = [
                'version' => (string) ($i['server']['redis_version'] ?? ''),
                'used' => $used,
                'max' => $max > 0 ? $max : null,
                'hit' => $hits === null || $miss === null ? null : self::ratio($hits, $hits + $miss),
                'clients' => isset($i['clients']['connected_clients']) ? (int) $i['clients']['connected_clients'] : null,
                'evicted' => isset($stats['evicted_keys']) ? (int) $stats['evicted_keys'] : null,
                'keys' => $keys,
            ];
        }
        if ($mem !== null && !empty($mem['installed'])) {
            $s = (array) ($mem['stats'] ?? []);
            $hits = isset($s['get_hits']) ? (float) $s['get_hits'] : null;
            $miss = isset($s['get_misses']) ? (float) $s['get_misses'] : null;
            $out['memcached'] = [
                'active' => !empty($mem['active']),
                'answering' => $s !== [],
                'used' => isset($s['bytes']) ? (int) $s['bytes'] : null,
                'max' => isset($s['limit_maxbytes']) ? (int) $s['limit_maxbytes'] : null,
                'hit' => $hits === null || $miss === null ? null : self::ratio($hits, $hits + $miss),
                'clients' => isset($s['curr_connections']) ? (int) $s['curr_connections'] : null,
                'evicted' => isset($s['evictions']) ? (int) $s['evictions'] : null,
                'items' => isset($s['curr_items']) ? (int) $s['curr_items'] : null,
            ];
        }
        $out['_state'] = $out['redis'] === null && $out['memcached'] === null ? 'empty' : 'ok';
        return $out;
    }

    /**
     * Nginx (running, configuration valid) and PHP-FPM per PHP version in use:
     * running, and how often a pool reached its worker limit in 24 hours.
     * Request rates need Nginx's stub_status, which is not enabled by NimVor,
     * so they are not shown (and never shown as zero).
     *
     * @return array<string, mixed>
     */
    public function webStack(): array
    {
        $nginx = $this->read('nginx.status');
        $rows = $this->db->fetchAll("SELECT DISTINCT php_version FROM websites WHERE server_id = ? AND enabled = 1 AND php_version <> '' ORDER BY php_version", [$this->server->id()]);
        $versions = array_values(array_filter(array_map(static fn (array $r): string => (string) $r['php_version'], $rows), static fn (string $v): bool => (bool) preg_match('/^\d+\.\d+$/', $v)));
        $fpm = $versions === [] ? ['versions' => []] : $this->read('php.fpm_health', ['versions' => array_slice($versions, 0, 12)]);
        if ($nginx === null && $fpm === null) {
            throw new AgentException('Web stack status unavailable');
        }
        $pools = [];
        foreach ((array) ($fpm['versions'] ?? []) as $v) {
            $v = (array) $v;
            $pools[] = [
                'version' => (string) $v['version'],
                'installed' => !empty($v['installed']),
                'active' => !empty($v['active']),
                'max_children' => !empty($v['log_read']) ? (int) ($v['max_children_reached'] ?? 0) : null,
                'busy' => !empty($v['log_read']) ? (int) ($v['busy_warnings'] ?? 0) : null,
                'crashes' => !empty($v['log_read']) ? (int) ($v['child_crashes'] ?? 0) : null,
                'last_pool' => (string) ($v['last_max_children_pool'] ?? ''),
            ];
        }
        return [
            'nginx' => $nginx === null ? null : [
                'installed' => !empty($nginx['installed']),
                'running' => !empty($nginx['running']),
                'config_ok' => !empty($nginx['config_ok']),
                'config_error' => (string) ($nginx['config_error'] ?? ''),
            ],
            'php' => $pools,
            'fpm_known' => $fpm !== null,
        ];
    }
}
