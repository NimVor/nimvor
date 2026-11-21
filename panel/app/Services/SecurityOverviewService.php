<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;

/**
 * What the dashboard's Security and SSL widgets show, put together from the
 * agent's existing read-only actions. A source that cannot answer gives null
 * (shown as N/A), never a made-up zero or an "all clear".
 */
final class SecurityOverviewService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly Connection $db,
        private readonly SettingsService $settings,
    ) {
    }

    /** @return array<string, mixed>|null null when the agent cannot answer */
    private function read(string $action, int $timeout = 30): ?array
    {
        try {
            return (array) json_decode((string) json_encode($this->agent->call($action, [], $timeout)), true);
        } catch (AgentException) {
            return null;
        }
    }

    /**
     * Firewall, fail2ban, SSH sign-ins, malware scanner, brute force protection,
     * ModSecurity, the audit's findings and the panel's own failed sign-ins.
     *
     * @return array<string, mixed>
     */
    public function security(): array
    {
        $fw = $this->read('firewall.status');
        $f2b = $this->read('fail2ban.status');
        $ssh = $this->read('ssh.logins', 60);
        $av = $this->read('av.status');
        $mod = $this->read('modsec.status');
        $audit = $this->read('security.audit', 120);
        if ($fw === null && $f2b === null && $ssh === null && $av === null && $audit === null) {
            throw new AgentException('Security status unavailable');
        }
        $banned = null;
        $jails = null;
        if ($f2b !== null && !empty($f2b['installed'])) {
            $jails = count((array) ($f2b['jails'] ?? []));
            $banned = array_sum(array_map(static fn ($j): int => (int) ((array) $j)['currently_banned'], (array) ($f2b['jails'] ?? [])));
        }
        $findings = null;
        if ($audit !== null) {
            $findings = ['fail' => 0, 'warn' => 0, 'high' => 0];
            foreach ($audit as $c) {
                $c = (array) $c;
                $st = (string) ($c['status'] ?? '');
                if ($st === 'fail' || $st === 'warn') {
                    $findings[$st]++;
                    if (($c['severity'] ?? '') === 'high') {
                        $findings['high']++;
                    }
                }
            }
        }
        $since = gmdate('c', time() - 86400);
        $panelFailed = (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'auth.login' AND result <> 'success' AND created_at >= ?", [$since]);
        $sshWeek = $ssh !== null && !empty($ssh['available']) ? (array) $ssh['week'] : null;
        $sshToday = $ssh !== null && !empty($ssh['available']) ? (array) $ssh['today'] : null;
        return [
            'firewall' => $fw === null || empty($fw['installed']) ? ($fw === null ? null : 'missing') : (!empty($fw['active']) ? 'on' : 'off'),
            'fail2ban' => $f2b === null ? null : (empty($f2b['installed']) ? 'missing' : (!empty($f2b['running']) ? 'on' : 'off')),
            'jails' => $jails,
            'banned' => $banned,
            'ssh_failed_today' => $sshToday === null ? null : (int) ($sshToday[1] ?? 0),
            'ssh_failed_week' => $sshWeek === null ? null : (int) ($sshWeek[1] ?? 0),
            'ssh_accepted_week' => $sshWeek === null ? null : (int) ($sshWeek[0] ?? 0),
            'ssh_last_failure' => $ssh['last_failure'] ?? null,
            'panel_failed_24h' => $panelFailed,
            'malware' => $av === null ? null : (empty($av['installed']) ? 'missing' : (!empty($av['freshclam']) ? 'on' : 'stale')),
            'malware_version' => $av['version'] ?? null,
            'hulk' => $this->settings->bool('hulk_enabled'),
            'modsec' => $mod === null ? null : (!empty($mod['active']) ? 'on' : 'off'),
            'findings' => $findings,
        ];
    }

    /**
     * Certificates: how many, which are close to expiry or expired, whether
     * automatic renewal runs, and which websites serve HTTPS without a
     * certificate that covers them.
     *
     * @return array<string, mixed>
     */
    public function ssl(): array
    {
        $store = $this->read('ssl.store', 60);
        if ($store === null) {
            throw new AgentException('Certificates unavailable');
        }
        $renewal = $this->read('ssl.renewal_status');
        $warn = $this->settings->int('ssl_warning_days');
        $certs = [];
        $expired = $expiring = $selfSigned = $valid = 0;
        foreach ((array) ($store['certificates'] ?? []) as $c) {
            $c = (array) $c;
            $cert = (array) ($c['certificate'] ?? []);
            if (($cert['error'] ?? '') !== '') {
                continue;
            }
            $days = (int) ($cert['days_remaining'] ?? 0);
            if (!empty($cert['expired']) || $days < 0) {
                $expired++;
            } elseif ($days <= $warn) {
                $expiring++;
            } else {
                $valid++;
            }
            if (!empty($cert['self_signed'])) {
                $selfSigned++;
            }
            $certs[] = ['name' => (string) ($c['name'] ?? ''), 'days' => $days, 'issuer' => (string) ($cert['issuer'] ?? ''), 'source' => (string) ($c['source'] ?? ''), 'unused' => ((array) ($c['used_by'] ?? [])) === []];
        }
        usort($certs, static fn (array $a, array $b): int => $a['days'] <=> $b['days']);
        $uncovered = [];
        $noHttps = 0;
        foreach ((array) ($store['hosts'] ?? []) as $h) {
            $h = (array) $h;
            if (empty($h['enabled'])) {
                continue;
            }
            if (empty($h['https'])) {
                $noHttps++;
            } elseif (empty($h['covered'])) {
                $uncovered[] = (string) ($h['name'] ?? '');
            }
        }
        return [
            'total' => count($certs),
            'valid' => $valid,
            'expiring' => $expiring,
            'expired' => $expired,
            'self_signed' => $selfSigned,
            'warn_days' => $warn,
            'soonest' => array_slice($certs, 0, 5),
            'uncovered' => array_slice($uncovered, 0, 10),
            'uncovered_total' => count($uncovered),
            'without_https' => $noHttps,
            'renewal' => $renewal === null || empty($renewal['certbot_installed']) ? ($renewal === null ? null : 'missing') : (!empty($renewal['timer_active']) ? 'on' : 'off'),
            'auto_ssl' => $this->settings->bool('auto_ssl'),
            '_state' => $certs === [] && $noHttps === 0 && $uncovered === [] ? 'empty' : 'ok',
        ];
    }
}
