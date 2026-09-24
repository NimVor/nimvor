<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Agent\RemoteTransport;
use WhaCP\Database\Connection;
use WhaCP\Security\Crypto;
use WhaCP\Support\Logger;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Servers managed by this panel: the local server (id 1) and remote
 * servers whose agents listen on TLS with a pinned certificate.
 */
final class ServerService
{
    public function __construct(
        private readonly Connection $db,
        private readonly Crypto $crypto,
        private readonly AuditLogger $audit,
        private readonly ServerContext $context,
        private readonly Logger $log,
    ) {
    }

    /** @return list<array<string, mixed>> */
    public function all(): array
    {
        return $this->db->fetchAll('SELECT s.id, s.name, s.kind, s.url, s.fingerprint, s.status, s.last_seen_at, s.last_error, s.agent_version, s.created_at,
            (SELECT COUNT(*) FROM websites w WHERE w.server_id = s.id) AS websites FROM servers s ORDER BY s.id');
    }

    /** @return array<string, mixed>|null */
    public function find(int $id): ?array
    {
        return $this->db->fetch('SELECT * FROM servers WHERE id = ?', [$id]);
    }

    /** @return array<string, mixed> agent health */
    private function probe(string $url, string $pin, string $token): array
    {
        $t = new RemoteTransport($url, $pin);
        $tokenFile = tempnam(sys_get_temp_dir(), 'whacp-probe');
        file_put_contents($tokenFile, $token);
        try {
            $client = new AgentClient($t, $tokenFile, $this->log, 15);
            $health = $client->health();
            $info = (array) $client->call('system.info');
        } finally {
            @unlink($tokenFile);
        }
        return ['version' => (string) ($health['version'] ?? $info['agent_version'] ?? ''), 'hostname' => (string) ($info['system']['hostname'] ?? '')];
    }

    /** @param array<string, mixed> $input */
    public function add(array $input): int
    {
        $errors = [];
        $name = trim((string) ($input['name'] ?? ''));
        if ($name === '' || mb_strlen($name) > 60) {
            $errors['name'] = 'Enter a name (up to 60 characters).';
        } elseif ($this->db->scalar('SELECT COUNT(*) FROM servers WHERE name = ?', [$name]) > 0) {
            $errors['name'] = 'A server with this name exists.';
        }
        $url = rtrim(trim((string) ($input['url'] ?? '')), '/');
        if (!RemoteTransport::validUrl($url)) {
            $errors['url'] = 'Enter the agent address, e.g. https://203.0.113.10:8443';
        }
        $pin = trim((string) ($input['pin'] ?? ''));
        if (!RemoteTransport::validPin($pin)) {
            $errors['pin'] = 'Paste the pin printed by "whacp-agent -pin" on that server (sha256//…).';
        }
        $token = trim((string) ($input['token'] ?? ''));
        if (strlen($token) < 32 || preg_match('/\s/', $token)) {
            $errors['token'] = 'Paste the agent token (/etc/whacp/agent.token on that server).';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $probe = $this->probe($url, $pin, $token);
        $this->db->run(
            "INSERT INTO servers (name, kind, url, token_enc, fingerprint, status, last_seen_at, agent_version, created_at) VALUES (?, 'remote', ?, ?, ?, 'online', ?, ?, ?)",
            [$name, $url, $this->crypto->encrypt($token), $pin, gmdate('c'), $probe['version'], gmdate('c')]
        );
        $id = $this->db->lastInsertId();
        $this->audit->record('server.add', 'server', (string) $id, AuditLogger::SUCCESS, ['name' => $name, 'url' => $url, 'pin' => $pin, 'hostname' => $probe['hostname']]);
        return $id;
    }

    /** Check a server and record its state. */
    public function check(int $id, AgentClient $client): bool
    {
        $prev = $this->context->forced();
        $this->context->force($id);
        try {
            $h = $client->health();
            $this->db->run("UPDATE servers SET status = 'online', last_seen_at = ?, last_error = '', agent_version = ? WHERE id = ?", [gmdate('c'), (string) ($h['version'] ?? ''), $id]);
            return true;
        } catch (AgentException $e) {
            $this->db->run("UPDATE servers SET status = 'offline', last_error = ? WHERE id = ?", [mb_substr($e->getMessage() . ' ' . $e->reason(), 0, 300), $id]);
            return false;
        } finally {
            $this->context->force($prev);
        }
    }

    public function remove(int $id): void
    {
        $s = $this->find($id);
        if ($s === null || $s['kind'] !== 'remote') {
            throw new UserFacingException('Only remote servers can be removed.');
        }
        if ($this->db->scalar('SELECT COUNT(*) FROM websites WHERE server_id = ?', [$id]) > 0) {
            throw new UserFacingException('This server still has websites in WhaCP.', '', 'Delete or move them first.');
        }
        $this->db->transaction(function (Connection $db) use ($id): void {
            foreach (['cron_jobs', 'workers', 'databases', 'database_users', 'backups', 'backup_schedules', 'agent_jobs', 'discovery_snapshots', 'metric_samples', 'alerts'] as $t) {
                $db->run("DELETE FROM {$t} WHERE server_id = ?", [$id]);
            }
            $db->run('DELETE FROM servers WHERE id = ?', [$id]);
        });
        if ($this->context->id() === $id) {
            $this->context->switchTo(1);
        }
        $this->audit->record('server.remove', 'server', (string) $id, AuditLogger::SUCCESS, ['name' => $s['name']]);
    }

    public function switchTo(int $id): void
    {
        if ($this->find($id) === null) {
            throw new UserFacingException('That server does not exist.');
        }
        $this->context->switchTo($id);
    }
}
