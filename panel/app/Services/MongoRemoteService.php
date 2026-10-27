<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Database\Connection;
use WhaCP\Security\Crypto;
use WhaCP\Support\UserFacingException;

/**
 * Remote MongoDB servers: saved connections to other MongoDB servers, used to
 * copy a database onto this one ("Get DB from server"). Passwords are stored
 * encrypted and only ever leave the panel inside an agent call, never to the
 * browser.
 */
final class MongoRemoteService
{
    public function __construct(
        private readonly Connection $db,
        private readonly Crypto $crypto,
        private readonly ServerContext $server,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * Saved remotes for this server, without the password.
     *
     * @return list<array{id:int,name:string,host:string,port:int,username:string,auth_db:string,tls:bool}>
     */
    public function list(): array
    {
        $rows = $this->db->fetchAll('SELECT id, name, host, port, username, auth_db, tls FROM mongo_remotes WHERE server_id = ? ORDER BY name', [$this->server->id()]);
        return array_map(static fn (array $r): array => [
            'id' => (int) $r['id'], 'name' => (string) $r['name'], 'host' => (string) $r['host'],
            'port' => (int) $r['port'], 'username' => (string) $r['username'],
            'auth_db' => (string) $r['auth_db'], 'tls' => (bool) $r['tls'],
        ], $rows);
    }

    /** @return array<string,mixed>|null */
    private function row(int $id): ?array
    {
        return $this->db->fetch('SELECT * FROM mongo_remotes WHERE id = ? AND server_id = ?', [$id, $this->server->id()]);
    }

    /** The connection parameters for an agent call (password decrypted). */
    private function params(int $id): array
    {
        $row = $this->row($id);
        if ($row === null) {
            throw new UserFacingException('That remote server is not configured.');
        }
        return [
            'host' => (string) $row['host'], 'port' => (int) $row['port'],
            'username' => (string) $row['username'],
            'password' => ((string) $row['password_enc']) !== '' ? $this->crypto->decrypt((string) $row['password_enc']) : '',
            'auth_db' => (string) $row['auth_db'], 'tls' => (bool) $row['tls'],
        ];
    }

    /**
     * Validate and normalise the submitted connection fields.
     *
     * @return array{name:string,host:string,port:int,username:string,password:string,auth_db:string,tls:bool}
     */
    private function clean(array $in): array
    {
        $name = trim((string) ($in['name'] ?? ''));
        $host = trim((string) ($in['host'] ?? ''));
        $port = (int) ($in['port'] ?? 27017);
        $user = trim((string) ($in['username'] ?? ''));
        $pass = (string) ($in['password'] ?? '');
        $authDb = trim((string) ($in['auth_db'] ?? '')) ?: 'admin';
        $tls = !empty($in['tls']);
        if ($name === '' || mb_strlen($name) > 64) {
            throw new UserFacingException('Enter a name for this connection (up to 64 characters).');
        }
        if ($host === '' || !preg_match('/^[A-Za-z0-9_.:-]{1,253}$/', $host)) {
            throw new UserFacingException('Enter a valid host name or IP address.');
        }
        if ($port < 1 || $port > 65535) {
            throw new UserFacingException('The port must be between 1 and 65535.');
        }
        if ($user !== '' && !preg_match('/^[A-Za-z0-9_.@-]{1,128}$/', $user)) {
            throw new UserFacingException('The user name contains characters that are not allowed.');
        }
        if (!preg_match('/^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$/', $authDb)) {
            throw new UserFacingException('The authentication database name is not valid.');
        }
        return ['name' => $name, 'host' => $host, 'port' => $port, 'username' => $user, 'password' => $pass, 'auth_db' => $authDb, 'tls' => $tls];
    }

    private function agentParams(array $c): array
    {
        return ['host' => $c['host'], 'port' => $c['port'], 'username' => $c['username'], 'password' => $c['password'], 'auth_db' => $c['auth_db'], 'tls' => $c['tls']];
    }

    /** Test the submitted connection and, if it works, save it (encrypted). */
    public function add(array $in, ?int $userId): string
    {
        $c = $this->clean($in);
        // Prove the connection works before storing it.
        $this->agent->call('mongo.remote_ping', $this->agentParams($c), 30);
        $now = gmdate('c');
        $this->db->run(
            'INSERT INTO mongo_remotes (server_id, name, host, port, username, password_enc, auth_db, tls, created_at, updated_at)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), $c['name'], $c['host'], $c['port'], $c['username'],
                $c['password'] !== '' ? $this->crypto->encrypt($c['password']) : '', $c['auth_db'], $c['tls'] ? 1 : 0, $now, $now]
        );
        $this->audit->record('mongo.remote_add', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['name' => $c['name'], 'host' => $c['host']], $userId);
        return $c['name'];
    }

    public function delete(int $id, ?int $userId): void
    {
        $row = $this->row($id);
        if ($row === null) {
            return;
        }
        $this->db->run('DELETE FROM mongo_remotes WHERE id = ? AND server_id = ?', [$id, $this->server->id()]);
        $this->audit->record('mongo.remote_delete', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['name' => (string) $row['name']], $userId);
    }

    /** Test a saved connection. @return array{version:string,databases:int} */
    public function ping(int $id): array
    {
        $o = (array) $this->agent->call('mongo.remote_ping', $this->params($id), 30);
        return ['version' => (string) ($o['version'] ?? ''), 'databases' => (int) ($o['databases'] ?? 0)];
    }

    /** List the databases on a saved remote. @return list<array{name:string,size:int}> */
    public function databases(int $id): array
    {
        $o = (array) $this->agent->call('mongo.remote_databases', $this->params($id), 30);
        return array_map(
            static fn ($d): array => ['name' => (string) ($d['name'] ?? ''), 'size' => (int) ($d['size'] ?? 0)],
            (array) ($o['databases'] ?? []),
        );
    }

    /** Copy a remote database into the local server. Returns the agent job id. */
    public function pull(int $id, string $remoteDb, string $targetDb, bool $drop, ?int $userId): string
    {
        $params = $this->params($id) + ['remote_db' => $remoteDb, 'target_db' => $targetDb !== '' ? $targetDb : $remoteDb, 'drop' => $drop];
        $o = (array) $this->agent->call('mongo.remote_pull', $params);
        $this->audit->record('mongo.remote_pull', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['remote_db' => $remoteDb, 'target_db' => $targetDb], $userId);
        return (string) ($o['job_id'] ?? '');
    }
}
