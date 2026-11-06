<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Repositories\FetchedFileRepository;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * Files fetched from a remote SFTP or FTPS server into the server's own
 * folder of fetched files (Backups > Remote fetched files).
 *
 * The agent does the transfer (a job) and keeps the file; the panel keeps
 * the list. The login used for a transfer is passed to the agent for that one
 * request and is never stored, logged or audited. The server's key is
 * confirmed by the person first (SFTP host key / FTPS certificate key) and
 * the agent refuses a server that does not present exactly that key.
 */
final class FetchedFileService
{
    public function __construct(
        private readonly FetchedFileRepository $files,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
    ) {
    }

    public const PROTOCOLS = ['sftp' => 'SFTP (SSH)', 'ftp' => 'FTPS (FTP over TLS)'];

    /** @return list<array<string, mixed>> */
    public function list(): array
    {
        return array_map(static function (array $r): array {
            $r['id'] = (int) $r['id'];
            $r['size'] = (int) $r['size'];
            $r['port'] = (int) $r['port'];
            $r['source'] = ($r['protocol'] === 'sftp' ? 'sftp' : 'ftp') . '://' . $r['username'] . '@' . $r['host'] . ':' . $r['port'];
            return $r;
        }, $this->files->list($this->server->id()));
    }

    public function count(): int
    {
        return $this->files->count($this->server->id());
    }

    /**
     * What the form describes: protocol, host, port, user and the file's path.
     * A full URL in the file field (sftp://user@host:22/path, ftp:// or
     * ftps://) fills what the other fields leave empty and decides the
     * protocol, host, port and path.
     *
     * @param array<string, mixed> $in
     * @return array{protocol: string, host: string, port: int, username: string, path: string, tls: string, password: string, private_key: string, name: string}
     */
    public function resolve(array $in, bool $needLogin): array
    {
        $errors = [];
        $str = static fn (string $k): string => is_scalar($in[$k] ?? null) ? trim((string) $in[$k]) : '';
        $protocol = $str('protocol') ?: 'sftp';
        $host = $str('host');
        $port = $str('port');
        $user = $str('username');
        $password = is_scalar($in['password'] ?? null) ? (string) $in['password'] : '';
        $tls = $str('tls') ?: 'explicit';
        $path = $str('remote_path');

        if (preg_match('#^(sftp|ftps?)://#i', $path) === 1) {
            $u = parse_url($path);
            if ($u === false || empty($u['host'])) {
                $errors['remote_path'] = 'This address is not valid. Use sftp://host/path/file or just the path on the server.';
            } else {
                $scheme = strtolower((string) $u['scheme']);
                $protocol = $scheme === 'sftp' ? 'sftp' : 'ftp';
                $tls = $scheme === 'ftps' ? 'implicit' : ($protocol === 'ftp' ? $tls : 'explicit');
                $host = (string) $u['host'];
                $port = isset($u['port']) ? (string) $u['port'] : '';
                $user = $user !== '' ? $user : rawurldecode((string) ($u['user'] ?? ''));
                // A password in an address would end up in history and logs: it is ignored; the Password field is used.
                $path = rawurldecode((string) ($u['path'] ?? ''));
                // A URL's path starts with "/", which on SFTP and FTP means the server's root.
                if ($path === '' || $path === '/') {
                    $errors['remote_path'] = 'The address has no file name. Add the path of the file, e.g. /backups/site.tar.gz.';
                }
            }
        }
        if (!isset(self::PROTOCOLS[$protocol])) {
            $errors['protocol'] = 'Choose SFTP or FTPS.';
        }
        $host = trim($host, '[]');
        if ($host === '' || (!preg_match('/^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/', $host) && filter_var($host, FILTER_VALIDATE_IP) === false)) {
            $errors['host'] = 'Enter the server\'s host name or IP address.';
        }
        if (!in_array($tls, ['explicit', 'implicit'], true)) {
            $errors['tls'] = 'Choose explicit or implicit TLS. Plain FTP is not offered because it sends the login and the file unprotected.';
        }
        $portNumber = $port === '' ? ($protocol === 'sftp' ? 22 : ($tls === 'implicit' ? 990 : 21)) : (ctype_digit($port) ? (int) $port : 0);
        if ($portNumber < 1 || $portNumber > 65535) {
            $errors['port'] = 'The port is a number from 1 to 65535.';
        }
        if ($needLogin) {
            if (!preg_match('/^[A-Za-z0-9._@+-]{1,100}$/', $user)) {
                $errors['username'] = 'Enter the user name (letters, digits and . _ @ + -).';
            }
            if (strlen($password) > 500 || preg_match('/[\r\n\0]/', $password) === 1) {
                $errors['password'] = 'This password cannot be used.';
            }
        }
        $key = is_scalar($in['private_key'] ?? null) ? trim((string) $in['private_key']) : '';
        if ($protocol === 'ftp') {
            $key = '';
        }
        if ($needLogin) {
            if ($protocol === 'sftp' && $password === '' && $key === '') {
                $errors['password'] = 'Enter the password, or paste a private key.';
            }
            if ($protocol === 'ftp' && $password === '') {
                $errors['password'] = 'Enter the password.';
            }
            if ($key !== '' && (!str_contains($key, 'PRIVATE KEY-----') || strlen($key) > 16384 || str_contains($key, 'ENCRYPTED'))) {
                $errors['private_key'] = 'Paste an unencrypted private key in OpenSSH or PEM format (it starts with -----BEGIN).';
            }
        }
        if (!isset($errors['remote_path'])) {
            if ($path === '' || strlen($path) > 1024) {
                $errors['remote_path'] = 'Enter the path of the file on the remote server, e.g. /backups/site.tar.gz.';
            } elseif (preg_match('/[\x00-\x1f\x7f]/', $path) === 1) {
                $errors['remote_path'] = 'The path contains a character that is not allowed.';
            } elseif (str_ends_with($path, '/')) {
                $errors['remote_path'] = 'This is a folder. Enter the path of the file itself.';
            } elseif (in_array('..', explode('/', $path), true)) {
                $errors['remote_path'] = 'The path cannot contain "..".';
            }
        }
        $name = $str('name');
        if ($name !== '' && (!preg_match('/^[A-Za-z0-9][A-Za-z0-9._-]{0,150}$/', $name) || str_ends_with($name, '.partial'))) {
            $errors['name'] = 'Use letters, digits and . _ - (it must start with a letter or digit).';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['protocol' => $protocol, 'host' => $host, 'port' => $portNumber, 'username' => $user, 'path' => $path, 'tls' => $tls, 'password' => $password, 'private_key' => $key, 'name' => $name];
    }

    /**
     * Ask the server for its key, to be confirmed by the person before any
     * login is sent. Nothing but the address is sent to the agent.
     *
     * @param array<string, mixed> $in
     * @return array<string, mixed>
     */
    public function probe(array $in): array
    {
        $t = $this->resolve($in, false);
        $res = (array) $this->agent->call('backup.fetch_probe', ['type' => $t['protocol'], 'host' => $t['host'], 'port' => $t['port'], 'tls' => $t['tls']], 60);
        return [
            'protocol' => $t['protocol'],
            'host' => $t['host'],
            'port' => $t['port'],
            'username' => $t['username'],
            'remote_path' => $t['path'],
            'tls' => $t['tls'],
            'host_key' => (string) ($res['host_key'] ?? ''),
            'key_type' => (string) ($res['key_type'] ?? ''),
            'fingerprint' => (string) ($res['fingerprint'] ?? ''),
            'tls_pin' => (string) ($res['tls_pin'] ?? ''),
            'subject' => (string) ($res['subject'] ?? ''),
        ];
    }

    /**
     * Start fetching a file; returns the record's id and the job's id.
     *
     * @param array<string, mixed> $in
     * @return array{id: int, job: string}
     */
    public function fetch(array $in, ?int $userId): array
    {
        $t = $this->resolve($in, true);
        $hostKey = is_scalar($in['host_key'] ?? null) ? trim((string) $in['host_key']) : '';
        $pin = is_scalar($in['tls_pin'] ?? null) ? trim((string) $in['tls_pin']) : '';
        if ($t['protocol'] === 'sftp' ? $hostKey === '' : $pin === '') {
            throw new ValidationException(['host' => 'Confirm the server\'s key first: choose "Check the server".'], 'The server\'s key has not been confirmed.');
        }
        $remote = ['type' => $t['protocol'], 'host' => $t['host'], 'port' => $t['port'], 'username' => $t['username'], 'password' => $t['password']];
        if ($t['protocol'] === 'sftp') {
            $remote['host_key'] = $hostKey;
            if ($t['private_key'] !== '') {
                $remote['private_key'] = $t['private_key'];
            }
        } else {
            $remote['tls'] = $t['tls'];
            $remote['tls_pin'] = $pin;
        }
        $params = ['remote' => $remote, 'path' => $t['path']];
        if ($t['name'] !== '') {
            $params['name'] = $t['name'];
        }
        $details = ['protocol' => $t['protocol'], 'host' => $t['host'], 'port' => $t['port'], 'username' => $t['username'], 'path' => $t['path']];
        try {
            $res = (array) $this->agent->call('backup.fetch_remote', $params);
        } catch (AgentException $e) {
            $this->audit->record('backup.fetch', 'server', (string) $this->server->id(), AuditLogger::FAILURE, $details);
            throw $e;
        }
        $job = (string) ($res['job_id'] ?? '');
        $id = $this->files->insert($this->server->id(), $t['protocol'], $t['host'], $t['port'], $t['username'], $t['path'], $job, $userId);
        $this->audit->record('backup.fetch', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $details);
        $this->jobs->track($job, 'Fetch ' . basename($t['path']), 'server', (string) $this->server->id(), $userId);
        return ['id' => $id, 'job' => $job];
    }

    /** Update the fetches that are still running from their agent jobs. */
    public function refresh(): void
    {
        foreach ($this->files->running($this->server->id()) as $f) {
            try {
                $job = (array) $this->agent->call('jobs.get', ['id' => $f['job_id']]);
            } catch (AgentException $e) {
                if ($e->errorCode() === 'not_found') {
                    $this->files->fail((int) $f['id'], 'The job was lost (the agent restarted).');
                }
                continue;
            }
            $status = (string) ($job['status'] ?? '');
            if ($status === 'failed') {
                $this->files->fail((int) $f['id'], (string) ($job['error'] ?? 'The file could not be fetched.'));
            } elseif ($status === 'succeeded') {
                $r = (array) ($job['result'] ?? []);
                $this->files->complete((int) $f['id'], (string) ($r['name'] ?? ''), (int) ($r['size'] ?? 0), (string) ($r['sha256'] ?? ''));
            }
        }
    }

    /**
     * Delete records and their files. A fetch that is still running is left
     * alone; a file the agent cannot delete keeps its record.
     *
     * @param list<int> $ids
     * @return array{deleted: int, running: int, failed: array<string, string>}
     */
    public function delete(array $ids): array
    {
        $ids = array_values(array_unique(array_filter(array_map('intval', $ids), static fn (int $i): bool => $i > 0)));
        if ($ids === []) {
            throw new UserFacingException('Nothing was selected.');
        }
        $rows = $this->files->findMany($this->server->id(), $ids);
        $running = 0;
        $names = [];
        $byName = [];
        $remove = [];
        foreach ($rows as $r) {
            if ($r['status'] === 'running') {
                $running++;
                continue;
            }
            if ($r['status'] === 'completed' && $r['file_name'] !== '') {
                $names[] = (string) $r['file_name'];
                $byName[(string) $r['file_name']] = $r;
                continue;
            }
            $remove[] = (int) $r['id'];
        }
        $failed = [];
        if ($names !== []) {
            $res = (array) $this->agent->call('backup.fetched_delete', ['names' => $names], 300);
            foreach ((array) ($res['results'] ?? []) as $one) {
                $one = (array) $one;
                $name = (string) ($one['name'] ?? '');
                if (!isset($byName[$name])) {
                    continue;
                }
                if (!empty($one['deleted'])) {
                    $remove[] = (int) $byName[$name]['id'];
                } else {
                    $failed[$name] = (string) ($one['error'] ?? 'The file could not be deleted.');
                }
            }
        }
        $this->files->delete($remove);
        foreach ($rows as $r) {
            if (in_array((int) $r['id'], $remove, true)) {
                $this->audit->record('backup.fetched_delete', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['file' => $r['file_name'], 'host' => $r['host'], 'path' => $r['remote_path']]);
            }
        }
        foreach ($failed as $name => $why) {
            $this->audit->record('backup.fetched_delete', 'server', (string) $this->server->id(), AuditLogger::FAILURE, ['file' => $name, 'error' => $why]);
        }
        return ['deleted' => count($remove), 'running' => $running, 'failed' => $failed];
    }
}
