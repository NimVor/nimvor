<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Database\Connection;
use WhaCP\Support\UserFacingException;

/**
 * Server hardening switches (administrators): compiler access, SMTP
 * restrictions, website accounts' outgoing connections, shell fork bomb
 * protection and traceroute. The agent
 * applies them; every change is audited.
 */
final class HardeningService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly Connection $db,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array<string, mixed> */
    public function status(): array
    {
        return json_decode((string) json_encode($this->agent->call('hardening.status')), true) ?: [];
    }

    /** @return list<string> the website accounts on this server */
    public function accounts(): array
    {
        return array_map('strval', array_column($this->db->fetchAll("SELECT DISTINCT system_user FROM websites WHERE server_id = ? AND system_user <> '' ORDER BY system_user", [$this->server->id()]), 'system_user'));
    }

    /**
     * @param list<string> $users
     * @param array<string, mixed> $actor
     */
    public function setCompilers(bool $restricted, array $users, array $actor): string
    {
        $this->admin($actor);
        $known = $this->accounts();
        $users = array_values(array_unique(array_map('strval', $users)));
        if (($unknown = array_diff($users, $known)) !== []) {
            throw new UserFacingException('Not a website account on this server: ' . implode(', ', $unknown) . '.');
        }
        $this->agent->call('hardening.compilers', ['restricted' => $restricted, 'users' => $restricted ? $users : []], 120);
        $this->record('hardening.compilers', ['restricted' => $restricted, 'users' => $users], $actor);
        return $restricted
            ? 'Compilers are restricted' . ($users === [] ? ' to root.' : '. Allowed: ' . implode(', ', $users) . '.')
            : 'Compilers can be run by every account again.';
    }

    /** @param array<string, mixed> $actor */
    public function setSmtp(bool $enabled, array $actor): string
    {
        $this->admin($actor);
        $this->agent->call('hardening.smtp', ['enabled' => $enabled], 120);
        $this->record('hardening.smtp', ['enabled' => $enabled], $actor);
        return $enabled ? 'SMTP restrictions are on.' : 'SMTP restrictions are off.';
    }

    /**
     * Website accounts' outgoing connections: cloud metadata services
     * (blocked unless turned off), private networks, and the only remote
     * ports allowed (comma- or space-separated; empty: any).
     *
     * @param array<string, mixed> $actor
     */
    public function setEgress(bool $metadataBlocked, bool $blockPrivate, string $tcp, string $udp, array $actor): string
    {
        $this->admin($actor);
        $params = ['metadata_blocked' => $metadataBlocked, 'block_private' => $blockPrivate, 'tcp_ports' => self::ports($tcp, 'TCP'), 'udp_ports' => self::ports($udp, 'UDP')];
        $this->agent->call('hardening.egress', $params, 120);
        $this->record('hardening.egress', $params, $actor);
        return 'Outgoing connection limits for website accounts saved.';
    }

    /** @return list<int> */
    private static function ports(string $list, string $what): array
    {
        $out = [];
        foreach (preg_split('/[\s,]+/', trim($list), -1, PREG_SPLIT_NO_EMPTY) ?: [] as $p) {
            if (!preg_match('/^[0-9]{1,5}$/', $p) || (int) $p < 1 || (int) $p > 65535) {
                throw new UserFacingException($what . ' ports: "' . $p . '" is not a port number (1–65535).');
            }
            $out[(int) $p] = (int) $p;
        }
        if (count($out) > 64) {
            throw new UserFacingException($what . ' ports: at most 64.');
        }
        ksort($out);
        return array_values($out);
    }

    /** @param array<string, mixed> $actor */
    public function setForkBomb(bool $enabled, array $actor): string
    {
        $this->admin($actor);
        $this->agent->call('hardening.forkbomb', ['enabled' => $enabled], 60);
        $this->record('hardening.forkbomb', ['enabled' => $enabled], $actor);
        return $enabled ? 'Fork bomb protection is on. It applies to new logins.' : 'Fork bomb protection is off.';
    }

    /** @param array<string, mixed> $actor */
    public function setTraceroute(bool $enabled, array $actor): string
    {
        $this->admin($actor);
        $this->agent->call('hardening.traceroute', ['enabled' => $enabled], 60);
        $this->record('hardening.traceroute', ['enabled' => $enabled], $actor);
        return $enabled ? 'Traceroute is enabled for every user.' : 'Traceroute is disabled for users other than root.';
    }

    /**
     * Scheduler pass: package updates can reset the programs' modes (rpm
     * does; dpkg keeps its overrides).
     */
    public function reapply(): void
    {
        $this->agent->call('hardening.reapply', [], 120);
    }

    /** @param array<string, mixed> $actor */
    private function admin(array $actor): void
    {
        if (($actor['role_name'] ?? '') !== 'admin') {
            throw new UserFacingException('Only administrators change server hardening.', status: 403);
        }
    }

    /**
     * @param array<string, mixed> $details
     * @param array<string, mixed> $actor
     */
    private function record(string $action, array $details, array $actor): void
    {
        $this->audit->record($action, 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $details, (int) $actor['id']);
    }
}
