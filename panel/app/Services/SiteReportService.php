<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Security\Countries;

/**
 * A website's traffic report: requests over time, refused and limited requests, the busiest and most refused
 * addresses and URLs, countries (from the DB-IP database) and ModSecurity events. The agent summarises the access
 * log (query strings are dropped); everything else is worked out here.
 */
final class SiteReportService
{
    public const WINDOWS = [24 => 'Last 24 hours', 72 => 'Last 3 days', 168 => 'Last 7 days'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly GeoIpService $geo,
        private readonly ModSecurityService $modsec,
    ) {
    }

    public static function window(mixed $hours): int
    {
        $h = (int) $hours;
        return isset(self::WINDOWS[$h]) ? $h : 24;
    }

    /**
     * @param array<string, mixed> $site
     * @return array<string, mixed>
     */
    public function build(array $site, int $hours): array
    {
        $r = (array) $this->agent->call('webreport.site', ['domain' => (string) $site['domain'], 'hours' => $hours], 60);
        $rep = [
            'exists' => !empty($r['exists']), 'truncated' => !empty($r['truncated']), 'hours' => $hours,
            'requests' => (int) ($r['requests'] ?? 0), 'refused' => (int) ($r['refused'] ?? 0),
            'limited' => (int) ($r['limited'] ?? 0), 'closed' => (int) ($r['closed'] ?? 0), 'forbidden' => (int) ($r['forbidden'] ?? 0),
            'classes' => array_map('intval', (array) ($r['classes'] ?? [])),
            'timeline' => array_map(static fn ($b): array => ['start' => (int) ((array) $b)['start'], 'requests' => (int) ((array) $b)['requests'], 'refused' => (int) ((array) $b)['refused']], (array) ($r['timeline'] ?? [])),
            'ips' => self::counts($r['ips'] ?? []), 'refused_ips' => self::counts($r['refused_ips'] ?? []),
            'urls' => self::counts($r['urls'] ?? []), 'refused_urls' => self::counts($r['refused_urls'] ?? []),
            'agents' => self::counts($r['refused_agents'] ?? []),
        ];
        $rep['countries'] = $this->countries(self::counts($r['addresses'] ?? []));
        foreach (['ips', 'refused_ips'] as $k) {
            foreach ($rep[$k] as $i => $row) {
                $rep[$k][$i]['country'] = $this->geo->country($row['key']);
            }
        }
        $rep['modsec'] = $this->modsecSummary($site);
        return $rep;
    }

    /**
     * Countries of the addresses seen, busiest first.
     *
     * @param list<array{key: string, total: int, refused: int}> $addresses
     * @return list<array{code: string, name: string, total: int, refused: int}>
     */
    public function countries(array $addresses): array
    {
        if ($addresses === [] || !$this->geo->loaded()) {
            return [];
        }
        $by = [];
        foreach ($addresses as $a) {
            $cc = $this->geo->country($a['key']);
            $cc = $cc === '' ? '--' : $cc;
            $by[$cc] ??= ['code' => $cc, 'name' => $cc === '--' ? 'Unknown or private' : Countries::name($cc), 'total' => 0, 'refused' => 0];
            $by[$cc]['total'] += $a['total'];
            $by[$cc]['refused'] += $a['refused'];
        }
        $out = array_values($by);
        usort($out, static fn (array $x, array $y): int => [$y['total'], $x['code']] <=> [$x['total'], $y['code']]);
        return array_slice($out, 0, 30);
    }

    /**
     * What ModSecurity caught for the website (its latest events).
     *
     * @param array<string, mixed> $site
     * @return array{available: bool, events: int, ips: list<array{key: string, total: int}>, rules: list<array{key: string, total: int}>, urls: list<array{key: string, total: int}>}
     */
    public function modsecSummary(array $site): array
    {
        try {
            $events = $this->modsec->events($site, 500);
        } catch (AgentException) {
            return ['available' => false, 'events' => 0, 'ips' => [], 'rules' => [], 'urls' => []];
        }
        $ips = $rules = $urls = [];
        foreach ($events as $e) {
            $e = (array) $e;
            $ip = (string) ($e['client_ip'] ?? '');
            $ips[$ip] = ($ips[$ip] ?? 0) + 1;
            $uri = (string) ($e['uri'] ?? '');
            $uri = strtok($uri, '?') ?: $uri;
            $urls[$uri] = ($urls[$uri] ?? 0) + 1;
            foreach ((array) ($e['rules'] ?? []) as $rule) {
                $rule = (array) $rule;
                $k = trim((string) ($rule['id'] ?? '') . ' ' . (string) ($rule['message'] ?? ''));
                if ($k !== '') {
                    $rules[$k] = ($rules[$k] ?? 0) + 1;
                }
            }
        }
        return ['available' => true, 'events' => count($events), 'ips' => self::top($ips), 'rules' => self::top($rules), 'urls' => self::top($urls)];
    }

    /**
     * @param array<string, int> $m
     * @return list<array{key: string, total: int}>
     */
    private static function top(array $m, int $n = 10): array
    {
        arsort($m);
        $out = [];
        foreach (array_slice($m, 0, $n, true) as $k => $v) {
            $out[] = ['key' => (string) $k, 'total' => $v];
        }
        return $out;
    }

    /**
     * @return list<array{key: string, total: int, refused: int}>
     */
    private static function counts(mixed $rows): array
    {
        $out = [];
        foreach ((array) $rows as $r) {
            $r = (array) $r;
            $out[] = ['key' => (string) ($r['key'] ?? ''), 'total' => (int) ($r['total'] ?? 0), 'refused' => (int) ($r['refused'] ?? 0)];
        }
        return $out;
    }
}
