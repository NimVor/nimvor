<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;

/**
 * The alert rules that come from Dashboard 2.0's data: website checks, stale
 * backups, security updates, the mail queue and the disk forecast. Their
 * thresholds are settings (category monitoring); 0 turns a rule off.
 *
 * A rule whose source cannot be read is listed as unknown, so the alert it
 * raised earlier stays open instead of being reported as resolved.
 */
final class AlertRules
{
    /** Rules this class raises. */
    public const RULES = ['website', 'slow', 'backup_stale', 'updates', 'mail_queue', 'disk_forecast'];

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly SettingsService $settings,
        private readonly ServerContext $server,
        private readonly ?CapacityService $capacity = null,
    ) {
    }

    /**
     * @return array{firing: list<array{0: string, 1: string, 2: string, 3: string}>, unknown: list<string>}
     */
    public function evaluate(int $now): array
    {
        $firing = [];
        $unknown = [];
        foreach ($this->websites() as $a) {
            $firing[] = $a;
        }
        foreach ($this->backups($now) as $a) {
            $firing[] = $a;
        }
        foreach ([
            'updates' => fn (): array => $this->updates(),
            'mail_queue' => fn (): array => $this->mail(),
        ] as $rule => $fn) {
            try {
                foreach ($fn() as $a) {
                    $firing[] = $a;
                }
            } catch (AgentException) {
                $unknown[] = $rule;
            }
        }
        foreach ($this->diskForecast($now) as $a) {
            $firing[] = $a;
        }
        return ['firing' => $firing, 'unknown' => $unknown];
    }

    /** @return list<array{0: string, 1: string, 2: string, 3: string}> */
    private function websites(): array
    {
        $out = [];
        $need = $this->settings->int('alert_site_down_checks');
        $slow = $this->settings->int('alert_site_slow_ms');
        $sid = $this->server->id();
        $sites = $this->db->fetchAll('SELECT id, domain FROM websites WHERE server_id = ? AND enabled = 1', [$sid]);
        foreach ($sites as $w) {
            $last = $this->db->fetchAll('SELECT status, ms FROM website_checks WHERE website_id = ? ORDER BY checked_at DESC LIMIT ?', [(int) $w['id'], max($need, 3)]);
            if (count($last) >= $need) {
                $failing = array_slice($last, 0, $need);
                $all = true;
                foreach ($failing as $c) {
                    $all = $all && ((int) $c['status'] === 0 || (int) $c['status'] >= 500);
                }
                if ($all) {
                    $s = (int) $failing[0]['status'];
                    $out[] = ['website', (string) $w['domain'], 'critical', $s === 0 ? "{$w['domain']} does not answer." : "{$w['domain']} answers with an error (HTTP {$s})."];
                    continue;
                }
            }
            if ($slow > 0 && count($last) >= 3) {
                $slowAll = true;
                foreach (array_slice($last, 0, 3) as $c) {
                    $slowAll = $slowAll && $c['ms'] !== null && (int) $c['ms'] > $slow;
                }
                if ($slowAll) {
                    $out[] = ['slow', (string) $w['domain'], 'warning', "{$w['domain']} answered slower than {$slow} ms in its last three checks."];
                }
            }
        }
        return $out;
    }

    /** @return list<array{0: string, 1: string, 2: string, 3: string}> */
    private function backups(int $now): array
    {
        $days = $this->settings->int('alert_backup_stale_days');
        if ($days <= 0) {
            return [];
        }
        $cut = gmdate('c', $now - $days * 86400);
        $rows = $this->db->fetchAll(
            "SELECT w.domain FROM websites w
             WHERE w.server_id = ? AND w.enabled = 1
               AND EXISTS (SELECT 1 FROM backup_schedules s WHERE s.website_id = w.id AND s.enabled = 1)
               AND NOT EXISTS (SELECT 1 FROM backups b WHERE b.website_id = w.id AND b.status = 'completed' AND b.created_at >= ?)
             ORDER BY w.domain",
            [$this->server->id(), $cut]
        );
        return array_map(static fn (array $r): array => ['backup_stale', (string) $r['domain'], 'warning', "{$r['domain']} has an enabled backup schedule but no completed backup for {$days} day(s)."], $rows);
    }

    /** @return list<array{0: string, 1: string, 2: string, 3: string}> */
    private function updates(): array
    {
        $min = $this->settings->int('alert_security_updates');
        if ($min <= 0) {
            return [];
        }
        $u = (array) json_decode((string) json_encode($this->agent->call('packages.updates', [], 90)), true);
        $n = (int) ($u['security'] ?? 0);
        return $n >= $min ? [['updates', '', 'warning', $n . ' security update' . ($n === 1 ? ' is' : 's are') . ' waiting to be installed.']] : [];
    }

    /** @return list<array{0: string, 1: string, 2: string, 3: string}> */
    private function mail(): array
    {
        $min = $this->settings->int('alert_mail_deferred');
        if ($min <= 0) {
            return [];
        }
        try {
            $q = (array) json_decode((string) json_encode($this->agent->call('mail.queue', [], 60)), true);
        } catch (AgentException $e) {
            // Mail is not installed on every server: an unreadable queue is not an alert.
            return [];
        }
        $n = (int) ($q['counts']['deferred'] ?? 0);
        return $n >= $min ? [['mail_queue', '', 'warning', "{$n} messages are deferred in the mail queue: delivery is failing."]] : [];
    }

    /** @return list<array{0: string, 1: string, 2: string, 3: string}> */
    private function diskForecast(int $now): array
    {
        $within = $this->settings->int('alert_disk_forecast_days');
        if ($within <= 0 || $this->capacity === null) {
            return [];
        }
        $f = $this->capacity->forecast($now);
        $d = $f['disk']['forecast'] ?? null;
        if (!is_array($d) || ($d['state'] ?? '') !== 'growing' || !empty($d['beyond_horizon']) || (int) $d['days'] > $within) {
            return [];
        }
        $days = (int) $d['days'];
        return [['disk_forecast', '', $days <= 3 ? 'critical' : 'warning', 'The fullest disk is growing by ' . number_format((float) $d['per_week'], 1) . '% a week and is forecast to be full in about ' . $days . ' day' . ($days === 1 ? '' : 's') . '.']];
    }
}
