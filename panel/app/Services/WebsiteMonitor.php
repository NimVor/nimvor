<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;

/**
 * Asks every enabled website for its home page (the agent does it on the server
 * itself, with the website's name in the Host header) and keeps the answers, so
 * the dashboard can say which websites are up, which fail and how fast they are.
 *
 * Outcome of one check: up (2xx/3xx), auth (401/403: the server answers but the
 * page wants a login), client_error (other 4xx), server_error (5xx), down
 * (nothing answered). A website that was never checked has no outcome at all.
 */
final class WebsiteMonitor
{
    public const KEEP_SECONDS = 14 * 86400;
    /** "Check now" is refused when the last pass is younger than this. */
    public const MIN_INTERVAL = 60;
    /** An answer slower than this is called slow. */
    public const SLOW_MS = 2000;

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
    ) {
    }

    /** One pass over every enabled website of the current server (scheduler). @return list<string> */
    public function run(): array
    {
        try {
            $n = $this->check();
        } catch (AgentException $e) {
            return ['Website checks not run: ' . $e->getMessage()];
        }
        return $n === null ? [] : ['Checked ' . $n . ' website' . ($n === 1 ? '' : 's') . '.'];
    }

    /**
     * Checks the websites now. Null when there is nothing to check or the agent
     * cannot open connections (nothing is recorded then, never a false "down").
     *
     * @param bool $force ignore MIN_INTERVAL
     */
    public function check(bool $force = true): ?int
    {
        $sid = $this->server->id();
        $last = (int) $this->db->scalar('SELECT MAX(checked_at) FROM website_checks WHERE server_id = ?', [$sid]);
        if (!$force && $last > time() - self::MIN_INTERVAL) {
            return null;
        }
        $rows = $this->db->fetchAll('SELECT id, domain, https, ipv4 FROM websites WHERE server_id = ? AND enabled = 1 AND monitor_disabled = 0 ORDER BY domain', [$sid]);
        if ($rows === []) {
            return null;
        }
        $sites = array_map(static fn (array $r): array => [
            'domain' => (string) $r['domain'],
            'ipv4' => (string) ($r['ipv4'] ?? ''),
            'https' => (int) $r['https'] === 1,
        ], $rows);
        $res = (array) json_decode((string) json_encode($this->agent->call('sites.probe', ['sites' => $sites, 'timeout_seconds' => 10], 330)), true);
        if (($res['supported'] ?? true) === false) {
            return null;
        }
        $byDomain = [];
        foreach ((array) ($res['results'] ?? []) as $r) {
            $byDomain[(string) ($r['domain'] ?? '')] = $r;
        }
        $now = time();
        $this->db->transaction(function () use ($rows, $byDomain, $sid, $now): void {
            foreach ($rows as $w) {
                $r = $byDomain[(string) $w['domain']] ?? null;
                if ($r === null) {
                    continue;
                }
                $status = (int) ($r['status'] ?? 0);
                $this->db->run(
                    'INSERT INTO website_checks (server_id, website_id, domain, checked_at, status, ms, error) VALUES (?, ?, ?, ?, ?, ?, ?)',
                    [$sid, (int) $w['id'], (string) $w['domain'], $now, $status, $status > 0 ? (int) ($r['ms'] ?? 0) : null, $status > 0 ? '' : (string) ($r['error'] ?? 'other')]
                );
            }
            $this->db->run('DELETE FROM website_checks WHERE checked_at < ?', [$now - self::KEEP_SECONDS]);
        });
        return count($byDomain);
    }

    public static function outcome(int $status): string
    {
        return match (true) {
            $status === 0 => 'down',
            $status >= 500 => 'server_error',
            $status === 401 || $status === 403 => 'auth',
            $status >= 400 => 'client_error',
            default => 'up',
        };
    }

    /**
     * What the widget shows, or null when no website has been checked yet.
     *
     * @return array<string, mixed>|null
     */
    public function summary(): ?array
    {
        $sid = $this->server->id();
        $enabled = (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE server_id = ?', [$sid]);
        $active = (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE server_id = ? AND enabled = 1 AND monitor_disabled = 0', [$sid]);
        $unmonitored = (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE server_id = ? AND enabled = 1 AND monitor_disabled = 1', [$sid]);
        // Only websites that are enabled and monitored count, in every figure below.
        $counted = 'website_id IN (SELECT id FROM websites WHERE server_id = :s AND enabled = 1 AND monitor_disabled = 0)';
        // Latest check per website that still exists.
        $latest = $this->db->fetchAll(
            'SELECT c.* FROM website_checks c
             JOIN websites w ON w.id = c.website_id AND w.enabled = 1 AND w.monitor_disabled = 0
             WHERE c.server_id = :s AND c.checked_at = (SELECT MAX(checked_at) FROM website_checks WHERE server_id = :s AND website_id = c.website_id)
             ORDER BY c.domain',
            ['s' => $sid]
        );
        if ($latest === []) {
            return null;
        }
        $counts = ['up' => 0, 'auth' => 0, 'client_error' => 0, 'server_error' => 0, 'down' => 0];
        $fast = [];
        $problems = [];
        foreach ($latest as $c) {
            $o = self::outcome((int) $c['status']);
            $counts[$o]++;
            if ($o === 'down' || $o === 'server_error') {
                $problems[] = ['domain' => (string) $c['domain'], 'status' => (int) $c['status'], 'error' => (string) $c['error'], 'since' => $this->failingSince((int) $c['website_id'])];
            }
            if ($c['ms'] !== null) {
                $fast[] = ['domain' => (string) $c['domain'], 'ms' => (int) $c['ms']];
            }
        }
        usort($fast, static fn (array $a, array $b): int => $b['ms'] <=> $a['ms']);
        $avg = $fast === [] ? null : (int) round(array_sum(array_column($fast, 'ms')) / count($fast));
        $since = time() - 86400;
        $day = $this->db->fetch("SELECT COUNT(*) AS n, SUM(CASE WHEN status > 0 AND status < 500 THEN 1 ELSE 0 END) AS ok FROM website_checks WHERE {$counted} AND checked_at >= :t", ['s' => $sid, 't' => $since]);
        $uptime = $day !== null && (int) $day['n'] > 0 ? round(100 * (int) $day['ok'] / (int) $day['n'], 2) : null;
        $lastDown = $this->db->scalar("SELECT MAX(checked_at) FROM website_checks WHERE {$counted} AND (status = 0 OR status >= 500)", ['s' => $sid]);
        $lastOk = $this->db->scalar("SELECT MAX(checked_at) FROM website_checks WHERE {$counted} AND status > 0 AND status < 500", ['s' => $sid]);
        return [
            'total' => $enabled,
            'enabled' => $active,
            'unmonitored' => $unmonitored,
            'checked' => count($latest),
            'counts' => $counts,
            'slowest' => array_slice($fast, 0, 5),
            'average_ms' => $avg,
            'slow' => count(array_filter($fast, static fn (array $f): bool => $f['ms'] > self::SLOW_MS)),
            'uptime_24h' => $uptime,
            'last_ok' => $lastOk === null || $lastOk === false ? null : (int) $lastOk,
            'last_down' => $lastDown === null || $lastDown === false ? null : (int) $lastDown,
            'checked_at' => (int) max(array_column($latest, 'checked_at')),
            'problems' => array_slice($problems, 0, 10),
        ];
    }

    /** When the website's current run of failures began (unix time). */
    private function failingSince(int $websiteId): ?int
    {
        $t = $this->db->scalar('SELECT MAX(checked_at) FROM website_checks WHERE website_id = ? AND status > 0 AND status < 500', [$websiteId]);
        $first = $this->db->scalar('SELECT MIN(checked_at) FROM website_checks WHERE website_id = ? AND checked_at > ?', [$websiteId, (int) $t]);
        return $first === null || $first === false ? null : (int) $first;
    }
}
