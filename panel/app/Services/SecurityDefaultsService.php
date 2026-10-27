<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Support\UserFacingException;

/**
 * The recommended security settings, as switches: malware scans, fail2ban
 * jails, compiler access, SMTP restrictions, the outgoing connection block
 * for cloud metadata services, brute force protection (cPHulk), shell fork
 * bomb protection, traceroute and ModSecurity.
 *
 * The server setup turns the recommended ones on once ({@see applyDefaults});
 * the Security overview lists each one with an Enable / Disable button
 * ({@see state}, {@see set}), so an administrator can change any of them
 * afterwards. Each switch is the same agent action or setting its own page
 * uses, and every change is audited.
 */
final class SecurityDefaultsService
{
    /** Switches: id => [title, what turning it on does]. */
    public const ITEMS = [
        'malware' => ['Malware scans', 'Scan every website weekly and quarantine infected files.'],
        'fail2ban' => ['Brute-force protection (fail2ban)', 'Ban addresses that fail SSH logins, scan for exploits or exceed Nginx rate limits (60 min ban, 5 attempts in 10 min, repeat offenders for a week).'],
        'compilers' => ['Compiler access', 'Only root (and NimVor\'s build account) may run compilers.'],
        'smtp' => ['SMTP restrictions', 'Only the mail server may open outgoing SMTP connections.'],
        'egress' => ['Outgoing connections', 'Block website accounts from reaching cloud metadata services.'],
        'hulk' => ['cPHulk Brute Force Protection', 'Block addresses and accounts with repeated failed logins to the panel, SSH, mail and FTP, using the recommended limits.'],
        'forkbomb' => ['Shell Fork Bomb Protection', 'Limit how many processes one shell login may start.'],
        'traceroute' => ['Traceroute', 'Disabled for users other than root.'],
        'modsec' => ['ModSecurity', 'The web application firewall for Nginx, with the OWASP Core Rule Set (detection only until blocking is allowed).'],
    ];

    /** The jails the recommended fail2ban setup enables. */
    public const JAILS = ['sshd', 'recidive', 'nginx-http-auth', 'nginx-botsearch', 'nginx-limit-req'];

    /** Recommended brute force protection (cPHulk) settings. */
    public const HULK = [
        'hulk_enabled' => '1', 'hulk_user_enabled' => '1', 'hulk_user_period' => 5, 'hulk_user_max' => 5, 'hulk_user_scope' => 'all',
        'hulk_ip_enabled' => '1', 'hulk_ip_period' => 15, 'hulk_ip_max' => 15, 'hulk_oneday_max' => 30, 'hulk_retention' => 360,
        'hulk_notify_root' => '1', 'hulk_notify_brute' => '1',
    ];

    /** Whitelisted in cPHulk when it is turned on (the administrator's own address). */
    public ?string $adminIp = null;

    public function __construct(
        private readonly AgentClient $agent,
        private readonly SettingsService $settings,
        private readonly HulkService $hulk,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
    ) {
    }

    /**
     * Every switch with whether it is on. "available" is false when it
     * cannot be turned on from here yet (e.g. ModSecurity not installed).
     *
     * @return list<array{id: string, title: string, help: string, on: bool, available: bool, detail: string}>
     */
    public function state(): array
    {
        $h = $this->read('hardening.status');
        $f2b = $this->read('fail2ban.status');
        $mod = $this->read('modsec.status');
        $jails = array_map(static fn ($j): string => (string) ((array) $j)['name'], (array) ($f2b['jails'] ?? []));
        $on = [
            'malware' => $this->settings->int('av_scan_days') > 0 && $this->settings->bool('av_quarantine'),
            'fail2ban' => !empty($f2b['running']) && array_diff(self::JAILS, $jails) === [],
            'compilers' => !empty($h['compilers']['restricted']),
            'smtp' => !empty($h['smtp']['enabled']),
            'egress' => !empty($h['egress']['metadata_blocked']),
            'hulk' => $this->settings->bool('hulk_enabled'),
            'forkbomb' => !empty($h['forkbomb']['enabled']),
            'traceroute' => isset($h['traceroute']) && empty($h['traceroute']['enabled']),
            'modsec' => !empty($mod['active']),
        ];
        $detail = [
            'fail2ban' => empty($f2b['installed']) ? 'fail2ban is not installed yet.' : '',
            'smtp' => isset($h['smtp']) && empty($h['smtp']['available']) ? 'nftables is not installed.' : '',
            'egress' => isset($h['egress']) && empty($h['egress']['available']) ? 'nftables is not installed.' : '',
            'modsec' => $mod === [] ? '' : (empty($mod['module_loaded']) ? 'The Nginx connector is not installed yet.' : (empty($on['modsec']) ? 'Installed, but turned off.' : '')),
        ];
        $out = [];
        foreach (self::ITEMS as $id => [$title, $help]) {
            $out[] = ['id' => $id, 'title' => $title, 'help' => $help, 'on' => $on[$id], 'available' => ($detail[$id] ?? '') === '' || $on[$id], 'detail' => $detail[$id] ?? ''];
        }
        return $out;
    }

    /**
     * Turns one switch on or off.
     *
     * @return string what was done
     */
    public function set(string $id, bool $on, ?int $userId): string
    {
        if (!isset(self::ITEMS[$id])) {
            throw new UserFacingException('Unknown security setting.');
        }
        $msg = match ($id) {
            'malware' => $this->malware($on, $userId),
            'fail2ban' => $this->fail2ban($on),
            'compilers' => $this->call('hardening.compilers', ['restricted' => $on, 'users' => []], 120, $on ? 'Compilers are restricted to root.' : 'Compilers can be run by every account again.'),
            'smtp' => $this->call('hardening.smtp', ['enabled' => $on], 120, $on ? 'SMTP restrictions are on.' : 'SMTP restrictions are off.'),
            'egress' => $this->egress($on),
            'hulk' => $this->hulk($on, $userId),
            'forkbomb' => $this->call('hardening.forkbomb', ['enabled' => $on], 60, $on ? 'Fork bomb protection is on. It applies to new logins.' : 'Fork bomb protection is off.'),
            // "On" for traceroute means disabled for users.
            'traceroute' => $this->call('hardening.traceroute', ['enabled' => !$on], 60, $on ? 'Traceroute is disabled for users other than root.' : 'Traceroute is enabled for every user.'),
            'modsec' => $this->modsec($on),
        };
        $this->audit->record('security.default', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['setting' => $id, 'on' => $on], $userId);
        return $msg;
    }

    /**
     * The server setup: turns on every recommended switch that is off. A
     * switch that cannot be turned on yet (a component not installed) is
     * reported, not forced.
     *
     * @return array{0: string, 1: string} status (ok|skip|fail) and a summary
     */
    public function applyDefaults(?int $userId): array
    {
        $done = [];
        $failed = [];
        $skipped = [];
        foreach ($this->state() as $s) {
            if ($s['on']) {
                continue;
            }
            // ModSecurity needs its connector installed first (a long build
            // the administrator starts from the ModSecurity page).
            if ($s['id'] === 'modsec' || !$s['available']) {
                $skipped[] = $s['title'] . ($s['detail'] !== '' ? ' (' . rtrim($s['detail'], '.') . ')' : '');
                continue;
            }
            try {
                $this->set($s['id'], true, $userId);
                $done[] = $s['title'];
            } catch (AgentException | UserFacingException $e) {
                $failed[] = $s['title'] . ': ' . $e->getMessage();
            }
        }
        $parts = [];
        if ($done !== []) {
            $parts[] = 'On: ' . implode(', ', $done) . '.';
        }
        if ($skipped !== []) {
            $parts[] = 'Not yet: ' . implode('; ', $skipped) . '.';
        }
        if ($failed !== []) {
            return ['fail', implode(' ', array_merge($parts, ['Failed: ' . implode('; ', $failed) . '.']))];
        }
        return [$done === [] ? 'skip' : 'ok', $parts === [] ? 'The recommended security settings are already on.' : implode(' ', $parts)];
    }

    private function malware(bool $on, ?int $userId): string
    {
        $this->settings->update(['av_scan_days' => $on ? 7 : 0, 'av_quarantine' => $on ? '1' : '0'], $userId);
        return $on ? 'Every website is scanned weekly and infected files are quarantined.' : 'Scheduled malware scans and quarantine are off.';
    }

    private function fail2ban(bool $on): string
    {
        $st = $this->read('fail2ban.status');
        if (empty($st['installed'])) {
            throw new UserFacingException('fail2ban is not installed yet. Install it under Components first.');
        }
        $keep = [];
        if (preg_match('/^\s*ignoreip\s*=\s*(.+)$/mi', (string) ($st['managed_config'] ?? ''), $m) === 1) {
            $keep = array_values(array_filter(preg_split('/[\s,]+/', trim($m[1])) ?: [], static fn (string $x): bool => $x !== '' && !in_array($x, ['127.0.0.1/8', '::1'], true)));
        }
        $jails = $on ? self::JAILS : [];
        $this->agent->call('fail2ban.configure', ['jails' => $jails, 'ban_minutes' => 60, 'find_minutes' => 10, 'max_retry' => 5, 'ignore' => $keep], 120);
        return $on ? 'fail2ban protects SSH and Nginx: 5 failures in 10 minutes ban for an hour.' : 'fail2ban jails are off.';
    }

    private function egress(bool $on): string
    {
        $e = (array) ($this->read('hardening.status')['egress'] ?? []);
        $this->agent->call('hardening.egress', [
            'metadata_blocked' => $on, 'block_private' => !empty($e['block_private']),
            'tcp_ports' => array_values((array) ($e['tcp_ports'] ?? [])), 'udp_ports' => array_values((array) ($e['udp_ports'] ?? [])),
            'loopback_isolation' => !empty($e['loopback_isolation']), 'loopback_ports' => array_values((array) ($e['loopback_ports'] ?? [])),
            'loopback_allow' => (object) ($e['loopback_allow'] ?? []),
        ], 120);
        return $on ? 'Website accounts can no longer reach cloud metadata services.' : 'Cloud metadata services are reachable again.';
    }

    private function hulk(bool $on, ?int $userId): string
    {
        $values = $on ? self::HULK : ['hulk_enabled' => '0'];
        if ($on && $this->hulk->whitelist() === [] && $this->adminIp !== null && $this->adminIp !== '') {
            $values['hulk_whitelist'] = $this->adminIp;
        }
        $this->settings->update($values, $userId);
        $this->hulk->sync();
        return $on ? 'cPHulk Brute Force Protection is on with the recommended limits.' : 'cPHulk Brute Force Protection is off.';
    }

    private function modsec(bool $on): string
    {
        $st = $this->read('modsec.status');
        if (empty($st['module_loaded'])) {
            throw new UserFacingException('The ModSecurity connector is not installed yet. Install it on the ModSecurity page first.');
        }
        $cfg = (array) ($st['config'] ?? []);
        $cfg['enabled'] = $on;
        if ($on && empty(((array) ($cfg['crs'] ?? []))['enabled']) && !empty(((array) ($st['crs'] ?? []))['available'])) {
            $cfg['crs'] = array_merge((array) ($cfg['crs'] ?? []), ['enabled' => true]);
        }
        $this->agent->call('modsec.configure', $cfg, 120);
        return $on ? 'ModSecurity is on.' : 'ModSecurity is off.';
    }

    /** @param array<string, mixed> $params */
    private function call(string $action, array $params, int $timeout, string $message): string
    {
        $this->agent->call($action, $params, $timeout);
        return $message;
    }

    /** @return array<string, mixed> an agent read; empty when the agent cannot answer */
    private function read(string $action): array
    {
        try {
            return (array) json_decode((string) json_encode($this->agent->call($action)), true);
        } catch (AgentException) {
            return [];
        }
    }
}
