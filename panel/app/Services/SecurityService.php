<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Support\UserFacingException;

/**
 * Firewall, SSH and fail2ban. Firewall and SSH changes are applied by the
 * agent under a guard and reverted automatically unless confirmed within
 * two minutes, so a change that cuts off access undoes itself.
 */
final class SecurityService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ?HulkService $hulk = null,
    ) {
    }

    /** @return array<string, mixed>|null */
    public function pending(): ?array
    {
        try {
            $p = ((array) $this->agent->call('security.pending'))['pending'] ?? null;
        } catch (AgentException) {
            return null;
        }
        return is_array($p) ? $p : null;
    }

    /** @param array<string, mixed> $res */
    private function recordGuarded(string $action, array $res, array $details): string
    {
        $p = (array) ($res['pending'] ?? []);
        $this->audit->record($action, 'server', 'security', AuditLogger::SUCCESS, $details + ['pending' => $p['id'] ?? null]);
        return empty($res['changed']) ? 'Nothing to change.' : 'Applied. Check that you can still connect, then confirm within 2 minutes — otherwise the change is undone automatically.';
    }

    /** @param array<string, mixed> $input */
    public function addRule(array $input): string
    {
        $rule = [
            'action' => (string) ($input['action'] ?? 'allow'),
            'port' => trim((string) ($input['port'] ?? '')),
            'proto' => (string) ($input['proto'] ?? ''),
            'from' => trim((string) ($input['from'] ?? '')),
            'comment' => trim((string) ($input['comment'] ?? '')),
        ];
        return $this->recordGuarded('firewall.rule_add', (array) $this->agent->call('firewall.rule_add', $rule), $rule);
    }

    public function deleteRule(string $spec): string
    {
        return $this->recordGuarded('firewall.rule_delete', (array) $this->agent->call('firewall.rule_delete', ['spec' => $spec]), ['rule' => $spec]);
    }

    /**
     * Turn the firewall on or off. Turning it on first opens SSH and the
     * panel (always), the listening services' ports ($services) and every
     * port for the administrator's address ($myIp, also whitelisted in
     * brute force protection), all in the same guarded change.
     */
    public function setFirewall(bool $enabled, bool $services = true, string $myIp = ''): string
    {
        if (!$enabled) {
            return $this->recordGuarded('firewall.disable', (array) $this->agent->call('firewall.set_enabled', ['enabled' => false]), []);
        }
        $ip = self::publicAddress($myIp) ? $myIp : '';
        $params = ['enabled' => true, 'allow_services' => $services, 'allow_from' => $ip];
        $msg = $this->recordGuarded('firewall.enable', (array) $this->agent->call('firewall.set_enabled', $params, 90), $params);
        if ($ip !== '' && $this->hulk !== null) {
            $this->hulk->addToWhitelist($ip);
        }
        return $msg;
    }

    /** An address worth allowing (not loopback, private tunnels are fine). */
    public static function publicAddress(string $ip): bool
    {
        return filter_var($ip, FILTER_VALIDATE_IP) !== false && !str_starts_with($ip, '127.') && $ip !== '::1';
    }

    /** @param array<string, mixed> $input */
    public function configureSsh(array $input): string
    {
        $s = [
            'port' => (int) ($input['port'] ?? 22),
            'permit_root_login' => (string) ($input['permit_root_login'] ?? 'prohibit-password'),
            'password_authentication' => filter_var($input['password_authentication'] ?? false, FILTER_VALIDATE_BOOL),
            'max_auth_tries' => (int) ($input['max_auth_tries'] ?? 4),
            'x11_forwarding' => filter_var($input['x11_forwarding'] ?? false, FILTER_VALIDATE_BOOL),
        ];
        return $this->recordGuarded('ssh.configure', (array) $this->agent->call('ssh.configure', $s, 90), $s);
    }

    public function confirm(string $id): void
    {
        $this->agent->call('security.confirm', ['id' => $id]);
        $this->audit->record('security.confirm', 'server', 'security', AuditLogger::SUCCESS, ['pending' => $id]);
    }

    public function undo(string $id): void
    {
        $this->agent->call('security.undo', ['id' => $id]);
        $this->audit->record('security.undo', 'server', 'security', AuditLogger::SUCCESS, ['pending' => $id]);
    }

    /** @param array<string, mixed> $input */
    public function configureFail2ban(array $input): void
    {
        $ignore = preg_split('/[\s,]+/', trim((string) ($input['ignore'] ?? ''))) ?: [];
        $cfg = [
            'jails' => array_values(array_map('strval', (array) ($input['jails'] ?? []))),
            'ban_minutes' => (int) ($input['ban_minutes'] ?? 60),
            'find_minutes' => (int) ($input['find_minutes'] ?? 10),
            'max_retry' => (int) ($input['max_retry'] ?? 5),
            'ignore' => array_values(array_filter($ignore, static fn ($x) => $x !== '')),
        ];
        $this->agent->call('fail2ban.configure', $cfg, 60);
        $this->audit->record('fail2ban.configure', 'server', 'fail2ban', AuditLogger::SUCCESS, $cfg);
    }

    public function unban(string $jail, string $ip): void
    {
        if (filter_var($ip, FILTER_VALIDATE_IP) === false) {
            throw new UserFacingException('That is not an IP address.');
        }
        $this->agent->call('fail2ban.unban', ['jail' => $jail, 'ip' => $ip]);
        $this->audit->record('fail2ban.unban', 'server', 'fail2ban', AuditLogger::SUCCESS, ['jail' => $jail, 'ip' => $ip]);
    }
}
