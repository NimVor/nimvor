<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Security\Crypto;
use WhaCP\Support\Events;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Backups of websites (files + their databases) and of the server
 * configuration, stored on the server and/or remotely (S3-compatible,
 * SFTP, FTPS, Google Drive or a local mounted folder).
 *
 * The agent creates, verifies, encrypts and restores; the panel keeps the
 * list (with each backup's manifest), remote storage credentials
 * (encrypted), schedules and retention.
 */
final class BackupService
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly Crypto $crypto,
        private readonly Events $events,
        private readonly OAuthClient $oauth,
    ) {
    }

    // ---- Remote storage -------------------------------------------------

    public const STORAGE_TYPES = [
        's3' => 'S3-compatible bucket',
        'sftp' => 'SFTP server',
        'ftp' => 'FTP server (FTPS)',
        'gdrive' => 'Google Drive',
        'local' => 'Local folder (mounted disk or NAS)',
    ];

    /**
     * @param bool $activeOnly only storages turned on (offered for new backups)
     * @return list<array<string, mixed>> without secrets
     */
    public function storages(bool $activeOnly = false): array
    {
        $rows = $this->db->fetchAll('SELECT * FROM backup_storages' . ($activeOnly ? ' WHERE active = 1' : '') . ' ORDER BY name');
        return array_map(function (array $r): array {
            $cfg = $this->storageConfig((int) $r['id']);
            return [
                'id' => (int) $r['id'], 'name' => $r['name'], 'kind' => $r['kind'], 'where' => self::describe($cfg), 'prefix' => $cfg['prefix'] ?? '',
                'created_at' => $r['created_at'], 'active' => (int) ($r['active'] ?? 1) === 1, 'test_ok' => (int) ($r['test_ok'] ?? 1) === 1,
                'test_error' => (string) ($r['test_error'] ?? ''), 'tested_at' => $r['tested_at'] ?? null, 'fields' => self::editableFields($cfg),
            ];
        }, $rows);
    }

    /**
     * The settings of a storage the edit window shows (never secrets).
     *
     * @param array<string, mixed> $cfg
     * @return array<string, mixed>
     */
    private static function editableFields(array $cfg): array
    {
        $keep = ['type', 'prefix', 'endpoint', 'region', 'bucket', 'path_style', 'host', 'port', 'username', 'tls', 'path'];
        return array_intersect_key($cfg, array_flip($keep));
    }

    /**
     * The agent "remote" parameter of a storage a new backup may use: it must
     * be turned on (restores, downloads and deletions use any storage).
     *
     * @return array<string, mixed>
     */
    public function activeStorageConfig(int $id): array
    {
        if ((int) $this->db->scalar('SELECT active FROM backup_storages WHERE id = ?', [$id]) !== 1) {
            throw new UserFacingException('That remote storage is turned off.', 'Only storages that are turned on receive new backups.', 'Test it and turn it on under Backups → Remote storage, or choose another.');
        }
        return $this->storageConfig($id);
    }

    public function storageActive(int $id): bool
    {
        return (int) $this->db->scalar('SELECT active FROM backup_storages WHERE id = ?', [$id]) === 1;
    }

    /**
     * Test a saved storage again and record the result.
     *
     * @return array{ok: bool, error: string}
     */
    public function retestStorage(int $id): array
    {
        $cfg = $this->storageConfig($id);
        $error = '';
        try {
            $trust = $this->testStorage($cfg);
            if ($trust !== []) {
                // A pinned key is always sent: something else answered.
                $error = 'The server presented a key that is not the one confirmed when the storage was added.';
            }
        } catch (AgentException $e) {
            $error = trim($e->getMessage() . ' ' . $e->reason());
        }
        $ok = $error === '';
        $this->db->run('UPDATE backup_storages SET test_ok = ?, test_error = ?, tested_at = ?' . ($ok ? '' : ', active = 0') . ' WHERE id = ?', [$ok ? 1 : 0, mb_substr($error, 0, 500), gmdate('c'), $id]);
        $this->audit->record('backup.storage_test', 'backup_storage', (string) $id, $ok ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['error' => $error !== '' ? mb_substr($error, 0, 300) : null]);
        return ['ok' => $ok, 'error' => $error];
    }

    /** Turn a storage on (only after a successful test) or off. */
    public function setStorageActive(int $id, bool $active): void
    {
        $row = $this->db->fetch('SELECT * FROM backup_storages WHERE id = ?', [$id]) ?? throw new UserFacingException('The remote storage no longer exists.');
        if ($active && ((int) $row['test_ok'] !== 1 || $row['tested_at'] === null)) {
            throw new UserFacingException('Test the storage first.', 'Its last connection test did not succeed.', 'Use "Test" and turn it on once the test passes.');
        }
        $this->db->run('UPDATE backup_storages SET active = ? WHERE id = ?', [$active ? 1 : 0, $id]);
        $this->audit->record($active ? 'backup.storage_on' : 'backup.storage_off', 'backup_storage', (string) $id, AuditLogger::SUCCESS, ['name' => $row['name']]);
    }

    /**
     * Change a storage: its name, sub-folder and settings. Secret fields
     * left empty keep their value. The server of an SFTP or FTP storage
     * (whose key was confirmed) is not changed here: add a new storage.
     * The changed settings are tested before they are saved.
     *
     * @param array<string, mixed> $input
     */
    public function updateStorage(int $id, array $input): void
    {
        $row = $this->db->fetch('SELECT * FROM backup_storages WHERE id = ?', [$id]) ?? throw new UserFacingException('The remote storage no longer exists.');
        $cfg = $this->storageConfig($id);
        $type = (string) $cfg['type'];
        $str = static fn (string $k): string => is_scalar($input[$k] ?? null) ? trim((string) $input[$k]) : '';
        $form = ['type' => $type, 'name' => '__edit__' . $id] + array_map('strval', array_filter(self::editableFields($cfg), 'is_scalar'));
        foreach (['prefix', 'endpoint', 'region', 'bucket', 'username', 'tls', 'path', 'port'] as $k) {
            if (array_key_exists($k, $input)) {
                $form[$k] = $str($k);
            }
        }
        $form['path_style'] = !empty($input['path_style']);
        // Secrets: empty keeps the stored one.
        foreach (['access_key', 'secret_key', 'password', 'private_key', 'client_id', 'client_secret'] as $k) {
            $form[$k] = $str($k) !== '' ? (string) $input[$k] : (string) ($cfg[$k] ?? '');
        }
        if (in_array($type, ['sftp', 'ftp'], true)) {
            $form['host'] = (string) $cfg['host'];
            if ($str('host') !== '' && $str('host') !== (string) $cfg['host']) {
                throw new ValidationException(['host' => 'The server of a storage cannot be changed (its key was confirmed for this server). Add a new storage instead.']);
            }
            if ((int) $form['port'] !== (int) ($cfg['port'] ?? 0)) {
                throw new ValidationException(['port' => 'The port of a storage cannot be changed. Add a new storage instead.']);
            }
        }
        $name = $str('name');
        if ($name === '' || mb_strlen($name) > 60) {
            throw new ValidationException(['name' => 'Enter a name (up to 60 characters).']);
        }
        if ($this->db->scalar('SELECT COUNT(*) FROM backup_storages WHERE name = ? AND id <> ?', [$name, $id]) > 0) {
            throw new ValidationException(['name' => 'A storage with this name exists.']);
        }
        if ($type === 'gdrive') {
            // Its access comes from Google's consent page: only the name and sub-folder change.
            $new = $cfg;
            $prefix = trim($str('prefix'), '/');
            if (!preg_match('#^[A-Za-z0-9._/-]{0,200}$#', $prefix) || str_contains($prefix, '..')) {
                throw new ValidationException(['prefix' => 'Use letters, digits and . _ / -']);
            }
            $new['prefix'] = $prefix;
        } else {
            $new = $this->buildStorage($form)['cfg'];
            foreach (['host_key', 'tls_pin', 'folder_id'] as $k) {
                if (isset($cfg[$k])) {
                    $new[$k] = $cfg[$k];
                }
            }
        }
        $trust = $this->testStorage($new);
        if ($trust !== []) {
            throw new UserFacingException('The storage presented a key that was not confirmed.', '', 'Add it as a new storage to confirm its key.');
        }
        $this->db->run('UPDATE backup_storages SET name = ?, config_enc = ?, test_ok = 1, test_error = \'\', tested_at = ? WHERE id = ?', [$name, $this->crypto->encrypt((string) json_encode($new)), gmdate('c'), $id]);
        $this->audit->record('backup.storage_update', 'backup_storage', (string) $id, AuditLogger::SUCCESS, ['name' => $name, 'was' => $row['name'], 'where' => self::describe($new)]);
    }

    /** @param array<string, mixed> $cfg */
    public static function describe(array $cfg): string
    {
        return match ($cfg['type'] ?? 's3') {
            'sftp', 'ftp' => ($cfg['type']) . '://' . ($cfg['username'] ?? '') . '@' . ($cfg['host'] ?? '') . (!empty($cfg['port']) ? ':' . $cfg['port'] : ''),
            'gdrive' => 'Google Drive · folder ' . ($cfg['folder_id'] ?? ''),
            'local' => (string) ($cfg['path'] ?? ''),
            default => ($cfg['endpoint'] ?? '') . ' · ' . ($cfg['bucket'] ?? ''),
        };
    }

    /** @return array<string, mixed> agent "remote" parameter */
    public function storageConfig(int $id): array
    {
        $row = $this->db->fetch('SELECT * FROM backup_storages WHERE id = ?', [$id]);
        if ($row === null) {
            throw new UserFacingException('The remote storage no longer exists.');
        }
        $cfg = (array) json_decode($this->crypto->decrypt((string) $row['config_enc']), true);
        $cfg['type'] ??= (string) $row['kind'];
        return $cfg;
    }

    /**
     * Validate the form for one storage type.
     *
     * @param array<string, mixed> $input
     * @return array{name: string, cfg: array<string, mixed>}
     */
    public function buildStorage(array $input): array
    {
        $errors = [];
        $str = static fn (string $k): string => is_scalar($input[$k] ?? null) ? trim((string) $input[$k]) : '';
        $name = $str('name');
        if (str_starts_with($name, '__edit__')) {
            // updateStorage checks the name itself.
        } elseif ($name === '' || mb_strlen($name) > 60) {
            $errors['name'] = 'Enter a name (up to 60 characters).';
        } elseif ($this->db->scalar('SELECT COUNT(*) FROM backup_storages WHERE name = ?', [$name]) > 0) {
            $errors['name'] = 'A storage with this name exists.';
        }
        $type = $str('type') ?: 's3';
        if (!isset(self::STORAGE_TYPES[$type])) {
            $errors['type'] = 'Choose a storage type.';
        }
        $prefix = trim($str('prefix'), '/');
        if (!preg_match('#^[A-Za-z0-9._/-]{0,200}$#', $prefix) || str_contains($prefix, '..')) {
            $errors['prefix'] = 'Use letters, digits and . _ / -';
        }
        $cfg = ['type' => $type, 'prefix' => $prefix];
        switch ($type) {
            case 's3':
                $endpoint = rtrim($str('endpoint'), '/');
                if (!preg_match('#^https://[A-Za-z0-9.-]+(:[0-9]+)?$#', $endpoint) && !preg_match('#^http://(127\.0\.0\.1|localhost)(:[0-9]+)?$#', $endpoint)) {
                    $errors['endpoint'] = 'Enter the https:// endpoint, e.g. https://s3.eu-central-1.amazonaws.com.';
                }
                if (!preg_match('/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/', $str('bucket'))) {
                    $errors['bucket'] = 'Enter a valid bucket name.';
                }
                $region = $str('region') ?: 'us-east-1';
                if (!preg_match('/^[a-z0-9-]{1,40}$/', $region)) {
                    $errors['region'] = 'Enter a region such as eu-central-1 (or us-east-1 if unsure).';
                }
                if ($str('access_key') === '' || $str('secret_key') === '') {
                    $errors['access_key'] = 'Enter the access key and secret key.';
                }
                $cfg += ['endpoint' => $endpoint, 'region' => $region, 'bucket' => $str('bucket'), 'access_key' => $str('access_key'), 'secret_key' => $str('secret_key'), 'path_style' => filter_var($input['path_style'] ?? false, FILTER_VALIDATE_BOOL)];
                break;
            case 'sftp':
            case 'ftp':
                $host = $str('host');
                if (!preg_match('/^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/', $host) && filter_var(trim($host, '[]'), FILTER_VALIDATE_IP) === false) {
                    $errors['host'] = 'Enter a host name or IP address.';
                }
                $port = $str('port') === '' ? 0 : (int) $str('port');
                if ($port < 0 || $port > 65535) {
                    $errors['port'] = 'Enter a port between 1 and 65535.';
                }
                if (!preg_match('/^[A-Za-z0-9._@+-]{1,100}$/', $str('username'))) {
                    $errors['username'] = 'Enter the user name (letters, digits and . _ @ + -).';
                }
                $password = is_string($input['password'] ?? null) ? (string) $input['password'] : '';
                $key = is_string($input['private_key'] ?? null) ? trim((string) $input['private_key']) : '';
                if ($type === 'sftp' && $password === '' && $key === '') {
                    $errors['password'] = 'Enter a password or paste a private key.';
                }
                if ($type === 'ftp' && $password === '') {
                    $errors['password'] = 'Enter the password.';
                }
                $cfg += ['host' => trim($host, '[]'), 'port' => $port, 'username' => $str('username'), 'password' => $password];
                if ($type === 'sftp') {
                    $cfg['private_key'] = $key;
                } else {
                    $tls = $str('tls') ?: 'explicit';
                    if (!in_array($tls, ['explicit', 'implicit', 'none'], true)) {
                        $errors['tls'] = 'Choose the encryption.';
                    }
                    $cfg['tls'] = $tls;
                }
                break;
            case 'local':
                $path = '/' . trim($str('path'), '/');
                if (!preg_match('#^/(mnt|media|backup|backups|srv/backups?)/[A-Za-z0-9._/-]+$#', $path) || str_contains($path, '..')) {
                    $errors['path'] = 'Enter a folder under /mnt, /media, /backup(s) or /srv/backup(s).';
                }
                $cfg['path'] = $path;
                break;
            case 'gdrive':
                if ($str('client_id') === '' || $str('client_secret') === '') {
                    $errors['client_id'] = 'Enter the OAuth client ID and secret of your Google Cloud project.';
                }
                $cfg += ['client_id' => $str('client_id'), 'client_secret' => $str('client_secret')];
                break;
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['name' => $name, 'cfg' => $cfg];
    }

    /**
     * Test a storage. Returns what the agent discovered that must be
     * trusted before saving (SFTP host key, FTPS certificate pin), or an
     * empty array when it can be saved as is.
     *
     * @param array<string, mixed> $cfg updated with discovered values
     * @return array{fingerprint?: string, host_key?: string, tls_pin?: string, tls_subject?: string}
     */
    public function testStorage(array &$cfg): array
    {
        $res = (array) $this->agent->call('backup.remote_test', ['remote' => $cfg], 120);
        $trust = [];
        if (($cfg['type'] ?? '') === 'sftp' && empty($cfg['host_key']) && !empty($res['host_key'])) {
            $cfg['host_key'] = (string) $res['host_key'];
            $trust = ['host_key' => $cfg['host_key'], 'fingerprint' => (string) ($res['fingerprint'] ?? '')];
        }
        if (($cfg['type'] ?? '') === 'ftp' && empty($cfg['tls_pin']) && !empty($res['tls_pin'])) {
            $cfg['tls_pin'] = (string) $res['tls_pin'];
            $trust = ['tls_pin' => $cfg['tls_pin'], 'tls_subject' => (string) ($res['tls_subject'] ?? '')];
        }
        if (($cfg['type'] ?? '') === 'gdrive' && !empty($res['folder_id'])) {
            $cfg['folder_id'] = (string) $res['folder_id'];
        }
        return $trust;
    }

    /** @param array<string, mixed> $cfg */
    public function insertStorage(string $name, array $cfg): int
    {
        if ($this->db->scalar('SELECT COUNT(*) FROM backup_storages WHERE name = ?', [$name]) > 0) {
            throw new ValidationException(['name' => 'A storage with this name exists.']);
        }
        // Added only after a successful test: on, with that test recorded.
        $this->db->run('INSERT INTO backup_storages (name, kind, config_enc, created_at, active, test_ok, tested_at) VALUES (?, ?, ?, ?, 1, 1, ?)', [$name, (string) $cfg['type'], $this->crypto->encrypt((string) json_encode($cfg)), gmdate('c'), gmdate('c')]);
        $id = $this->db->lastInsertId();
        $this->audit->record('backup.storage_create', 'backup_storage', (string) $id, AuditLogger::SUCCESS, ['name' => $name, 'type' => $cfg['type'], 'where' => self::describe($cfg), 'pinned' => $cfg['host_key'] ?? $cfg['tls_pin'] ?? null]);
        return $id;
    }

    /**
     * Validate, test and save a storage in one step (no trust needed).
     *
     * @param array<string, mixed> $input
     */
    public function saveStorage(array $input, bool $test = true): int
    {
        ['name' => $name, 'cfg' => $cfg] = $this->buildStorage($input);
        if ($test) {
            $this->testStorage($cfg);
        }
        return $this->insertStorage($name, $cfg);
    }

    /**
     * A storage waiting for confirmation (host key, certificate) or for the
     * Google consent, sealed so its credentials never sit in the session in
     * clear text.
     *
     * @param array{name: string, cfg: array<string, mixed>} $pending
     */
    public function seal(array $pending): string
    {
        return $this->crypto->encrypt((string) json_encode($pending + ['at' => time()]));
    }

    /** @return array{name: string, cfg: array<string, mixed>} */
    public function unseal(mixed $sealed): array
    {
        try {
            $p = is_string($sealed) && $sealed !== '' ? (array) json_decode($this->crypto->decrypt($sealed), true) : [];
        } catch (\Throwable) {
            $p = [];
        }
        if (!isset($p['name'], $p['cfg'], $p['at']) || !is_array($p['cfg']) || time() - (int) $p['at'] > 1800) {
            throw new UserFacingException('The storage setup expired.', '', 'Add the storage again.');
        }
        return ['name' => (string) $p['name'], 'cfg' => $p['cfg']];
    }

    // ---- Google Drive (OAuth) --------------------------------------------

    public const GOOGLE_SCOPE = 'https://www.googleapis.com/auth/drive.file';

    public function googleRedirectUri(string $panelUrl): string
    {
        return rtrim($panelUrl, '/') . '/backups/storages/google/callback';
    }

    /** @param array{name: string, cfg: array<string, mixed>} $pending */
    public function googleAuthUrl(array $pending, string $redirectUri, string $state): string
    {
        return $this->oauth->authUrl([
            'client_id' => (string) $pending['cfg']['client_id'], 'redirect_uri' => $redirectUri, 'response_type' => 'code',
            'scope' => self::GOOGLE_SCOPE, 'access_type' => 'offline', 'prompt' => 'consent', 'state' => $state,
        ]);
    }

    /**
     * Exchange the authorisation code for a refresh token, create the
     * backup folder (through the agent's test) and save the storage.
     *
     * @param array{name: string, cfg: array<string, mixed>} $pending
     */
    public function googleComplete(array $pending, string $code, string $redirectUri): int
    {
        $res = $this->oauth->token([
            'grant_type' => 'authorization_code', 'code' => $code, 'redirect_uri' => $redirectUri,
            'client_id' => (string) $pending['cfg']['client_id'], 'client_secret' => (string) $pending['cfg']['client_secret'],
        ]);
        if (empty($res['refresh_token']) || !is_string($res['refresh_token'])) {
            throw new UserFacingException('Google did not grant offline access.', (string) ($res['error_description'] ?? $res['error'] ?? ''), 'Try again; if it keeps failing, remove WhaCP from your Google account\'s third-party access and connect again.');
        }
        $cfg = $pending['cfg'] + ['refresh_token' => $res['refresh_token']];
        $this->testStorage($cfg);
        return $this->insertStorage($pending['name'], $cfg);
    }

    public function deleteStorage(int $id): void
    {
        if ($this->db->scalar("SELECT COUNT(*) FROM backups WHERE storage_id = ? AND local = 0 AND status = 'completed'", [$id]) > 0) {
            throw new UserFacingException('Some backups are only stored there.', 'Deleting the storage would make them unreachable from WhaCP.', 'Delete those backups first.');
        }
        $this->db->run('DELETE FROM backup_storages WHERE id = ?', [$id]);
        $this->audit->record('backup.storage_delete', 'backup_storage', (string) $id);
    }

    /**
     * Find the backups kept in a storage and record those this server can
     * restore (recovery onto a new server). The agent imports only backups
     * encrypted with this server's backup key whose manifest that key
     * authenticates; the rest come back with the reason. Website backups are
     * recorded for websites that exist here (by domain).
     *
     * @return array{imported: int, known: int, no_website: list<string>, skipped: list<string>}
     */
    public function findInStorage(int $storageId, ?int $userId): array
    {
        $res = (array) $this->agent->call('backup.remote_import', ['remote' => $this->storageConfig($storageId)], 600);
        $out = ['imported' => 0, 'known' => 0, 'no_website' => [], 'skipped' => []];
        foreach ((array) ($res['backups'] ?? []) as $b) {
            $b = (array) $b;
            $id = (string) ($b['id'] ?? '');
            $what = ($b['kind'] ?? '') === 'server' ? 'server backup ' . $id : ($b['domain'] ?? '') . ' ' . $id;
            if (($b['skipped'] ?? '') !== '' || !is_array($b['manifest'] ?? null)) {
                $out['skipped'][] = trim($what) . ': ' . (string) ($b['skipped'] ?? 'not readable');
                continue;
            }
            $m = (array) $b['manifest'];
            if ($this->db->scalar('SELECT COUNT(*) FROM backups WHERE server_id = ? AND backup_id = ?', [$this->server->id(), $id]) > 0) {
                $out['known']++;
                continue;
            }
            $websiteId = null;
            if ($m['kind'] === 'site') {
                $websiteId = $this->db->scalar('SELECT id FROM websites WHERE server_id = ? AND domain = ?', [$this->server->id(), (string) $m['domain']]);
                if ($websiteId === null) {
                    $out['no_website'][] = (string) $m['domain'];
                    continue;
                }
            }
            unset($m['meta']);
            $created = (string) ($m['created_at'] ?? gmdate('c'));
            $this->db->run(
                "INSERT INTO backups (server_id, website_id, kind, domain, backup_id, label, manifest, size, encrypted, storage_id, local, status, user_id, created_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, 0, 'completed', ?, ?, ?)",
                [$this->server->id(), $websiteId === null ? null : (int) $websiteId, (string) $m['kind'], (string) ($m['domain'] ?? ''), $id, mb_substr((string) ($m['label'] ?? ''), 0, 100), json_encode($m, JSON_UNESCAPED_SLASHES), (int) ($m['size'] ?? 0), $storageId, $userId, $created, $created]
            );
            $out['imported']++;
        }
        $out['no_website'] = array_values(array_unique($out['no_website']));
        $this->audit->record('backup.storage_scan', 'backup_storage', (string) $storageId, AuditLogger::SUCCESS, ['imported' => $out['imported'], 'known' => $out['known'], 'skipped' => count($out['skipped']), 'no_website' => $out['no_website']]);
        return $out;
    }

    // ---- Backups --------------------------------------------------------

    /**
     * @param array<string, mixed> $site
     * @return list<string>
     */
    public function siteDatabases(array $site): array
    {
        return array_map(static fn (array $r): string => (string) $r['name'], $this->db->fetchAll('SELECT name FROM databases WHERE website_id = ? ORDER BY name', [(int) $site['id']]));
    }

    /**
     * Start a website backup; returns the agent job id.
     *
     * @param array<string, mixed> $site
     * @param array{files?: bool, databases?: bool, encrypt?: bool, storage_id?: int|null, keep_local?: bool, label?: string, schedule_id?: int|null} $opts
     */
    public function createSite(array $site, array $opts, ?int $userId): string
    {
        $files = (bool) ($opts['files'] ?? true);
        $dbs = ($opts['databases'] ?? true) ? $this->siteDatabases($site) : [];
        if (!$files && $dbs === []) {
            throw new UserFacingException('Nothing to back up.', 'Choose the website files, or link a database to this website.');
        }
        $label = mb_substr(trim((string) ($opts['label'] ?? '')), 0, 100);
        $storageId = $opts['storage_id'] ?? null;
        $params = [
            'domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'],
            'files' => $files, 'databases' => $dbs, 'encrypt' => (bool) ($opts['encrypt'] ?? true), 'label' => $label,
            'meta' => ['website' => WebsiteService::spec($site, false), 'panel_version' => 1],
        ];
        if ($storageId !== null) {
            $params['remote'] = $this->activeStorageConfig($storageId);
            $params['keep_local'] = (bool) ($opts['keep_local'] ?? true);
        }
        $res = (array) $this->agent->call('backup.create', $params);
        $job = (string) $res['job_id'];
        $this->db->run(
            'INSERT INTO backups (server_id, website_id, schedule_id, kind, domain, label, encrypted, storage_id, status, job_id, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), (int) $site['id'], $opts['schedule_id'] ?? null, 'site', $site['domain'], $label, $params['encrypt'] ? 1 : 0, $storageId, 'running', $job, $userId, gmdate('c')]
        );
        $this->audit->record('backup.create', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'files' => $files, 'databases' => $dbs, 'remote' => $storageId !== null]);
        return $job;
    }

    /**
     * Server backups hold the agent token, the panel key and TLS private
     * keys, so they are always encrypted ("encrypt" in $opts is ignored).
     *
     * @param array{encrypt?: bool, storage_id?: int|null, keep_local?: bool, label?: string, schedule_id?: int|null} $opts
     */
    public function createServer(array $opts, ?int $userId): string
    {
        $storageId = $opts['storage_id'] ?? null;
        $encrypt = true;
        $label = mb_substr(trim((string) ($opts['label'] ?? '')), 0, 100);
        $params = ['encrypt' => $encrypt, 'label' => $label];
        if ($storageId !== null) {
            $params['remote'] = $this->activeStorageConfig($storageId);
            $params['keep_local'] = (bool) ($opts['keep_local'] ?? true);
        }
        $res = (array) $this->agent->call('backup.server', $params);
        $job = (string) $res['job_id'];
        $this->db->run(
            'INSERT INTO backups (server_id, schedule_id, kind, label, encrypted, storage_id, status, job_id, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), $opts['schedule_id'] ?? null, 'server', $label, $encrypt ? 1 : 0, $storageId, 'running', $job, $userId, gmdate('c')]
        );
        $this->audit->record('backup.server', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['remote' => $storageId !== null]);
        return $job;
    }

    /** Update running backups from their agent jobs. */
    public function refresh(): void
    {
        foreach ($this->db->fetchAll("SELECT * FROM backups WHERE server_id = ? AND status = 'running'", [$this->server->id()]) as $b) {
            try {
                $job = (array) $this->agent->call('jobs.get', ['id' => $b['job_id']]);
            } catch (AgentException $e) {
                if ($e->errorCode() === 'not_found') {
                    $this->db->run("UPDATE backups SET status = 'failed', error = ?, finished_at = ? WHERE id = ?", ['The job was lost (agent restarted).', gmdate('c'), (int) $b['id']]);
                }
                continue;
            }
            $status = (string) ($job['status'] ?? '');
            if ($status === 'failed') {
                $this->db->run("UPDATE backups SET status = 'failed', error = ?, finished_at = ? WHERE id = ?", [mb_substr((string) ($job['error'] ?? ''), 0, 500), gmdate('c'), (int) $b['id']]);
                $this->events->dispatch('backup.failed', ['backup' => $b]);
            } elseif ($status === 'succeeded') {
                $m = (array) ($job['result'] ?? []);
                unset($m['meta']);
                $this->db->run(
                    "UPDATE backups SET status = 'completed', backup_id = ?, manifest = ?, size = ?, encrypted = ?, local = ?, finished_at = ? WHERE id = ?",
                    [(string) ($m['id'] ?? ''), json_encode($m, JSON_UNESCAPED_SLASHES), (int) ($m['size'] ?? 0), !empty($m['encrypted']) ? 1 : 0, !empty($m['local']) ? 1 : 0, gmdate('c'), (int) $b['id']]
                );
                $this->events->dispatch('backup.completed', ['backup' => $b]);
            }
        }
    }

    /**
     * @param array<string, mixed>|null $site null = server backups
     * @return list<array<string, mixed>>
     */
    public function list(?array $site, int $limit = 100): array
    {
        $rows = $site === null
            ? $this->db->fetchAll("SELECT b.*, s.name AS storage_name FROM backups b LEFT JOIN backup_storages s ON s.id = b.storage_id WHERE b.server_id = ? AND b.kind = 'server' ORDER BY b.id DESC LIMIT ?", [$this->server->id(), $limit])
            : $this->db->fetchAll('SELECT b.*, s.name AS storage_name FROM backups b LEFT JOIN backup_storages s ON s.id = b.storage_id WHERE b.website_id = ? ORDER BY b.id DESC LIMIT ?', [(int) $site['id'], $limit]);
        return array_map([self::class, 'hydrate'], $rows);
    }

    /**
     * Website backups visible to a user (all websites for admins).
     *
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function listForUser(array $user, int $limit = 200): array
    {
        [$cond, $params] = Scope::condition($user, 'w.owner_id');
        $rows = $this->db->fetchAll(
            "SELECT b.*, s.name AS storage_name FROM backups b JOIN websites w ON w.id = b.website_id LEFT JOIN backup_storages s ON s.id = b.storage_id
             WHERE b.server_id = :server AND b.kind = 'site' AND {$cond} ORDER BY b.id DESC LIMIT :lim",
            ['server' => $this->server->id(), 'lim' => $limit] + $params
        );
        return array_map([self::class, 'hydrate'], $rows);
    }

    /**
     * @param array<string, mixed> $r
     * @return array<string, mixed>
     */
    private static function hydrate(array $r): array
    {
        $m = json_decode((string) $r['manifest'], true);
        $r['manifest'] = is_array($m) ? $m : [];
        $r['parts'] = array_map(static fn ($f): array => (array) $f, (array) ($r['manifest']['files'] ?? []));
        $r['local'] = (int) $r['local'] === 1;
        $r['encrypted'] = (int) $r['encrypted'] === 1;
        return $r;
    }

    /**
     * A backup of a website (scoped) or, with $site null, a server backup.
     *
     * @param array<string, mixed>|null $site
     * @return array<string, mixed>|null
     */
    public function find(?array $site, int $id): ?array
    {
        $row = $site === null
            ? $this->db->fetch("SELECT * FROM backups WHERE id = ? AND server_id = ? AND kind = 'server'", [$id, $this->server->id()])
            : $this->db->fetch('SELECT * FROM backups WHERE id = ? AND website_id = ?', [$id, (int) $site['id']]);
        return $row === null ? null : self::hydrate($row);
    }

    /** @param array<string, mixed> $b */
    private function target(array $b): array
    {
        $t = $b['kind'] === 'server' ? ['kind' => 'server'] : ['domain' => $b['domain']];
        return $t + ['id' => $b['backup_id']];
    }

    /** @param array<string, mixed> $b */
    public function delete(array $b): void
    {
        if ($b['status'] === 'running') {
            throw new UserFacingException('This backup is still running.');
        }
        if ($b['status'] === 'completed') {
            $params = $this->target($b);
            if ($b['storage_id'] !== null) {
                try {
                    $params['remote'] = $this->storageConfig((int) $b['storage_id']);
                } catch (UserFacingException) {
                    // storage removed: only the local copy can be deleted
                }
            }
            try {
                $this->agent->call('backup.delete', $params, 300);
            } catch (AgentException $e) {
                if ($e->errorCode() !== 'not_found') {
                    throw $e;
                }
            }
        }
        $this->db->run('DELETE FROM backups WHERE id = ?', [(int) $b['id']]);
        $this->audit->record('backup.delete', $b['kind'] === 'server' ? 'server' : 'website', (string) ($b['website_id'] ?? $b['server_id']), AuditLogger::SUCCESS, ['backup' => $b['backup_id'], 'domain' => $b['domain']]);
    }

    /**
     * Restore selected parts of a website backup; returns the job id.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $b
     * @param list<string> $databases source database names to restore (into the same name)
     */
    public function restore(array $site, array $b, bool $files, array $databases, string $confirm, ?int $userId): string
    {
        if ($b['status'] !== 'completed' || $b['kind'] !== 'site') {
            throw new UserFacingException('Only completed website backups can be restored.');
        }
        if ($confirm !== $site['domain']) {
            throw new UserFacingException('Type the domain name to confirm the restore.');
        }
        $available = [];
        foreach ($b['parts'] as $p) {
            if (($p['kind'] ?? '') === 'database') {
                $available[] = (string) $p['database'];
            }
        }
        $map = [];
        foreach ($databases as $d) {
            if (!in_array($d, $available, true)) {
                throw new UserFacingException('The backup does not contain database ' . $d . '.');
            }
            $map[$d] = $d;
        }
        if (!$files && $map === []) {
            throw new UserFacingException('Choose what to restore.');
        }
        $params = ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'id' => $b['backup_id'], 'files' => $files, 'databases' => (object) $map, 'safety_backup' => $files];
        if ($b['storage_id'] !== null) {
            $params['remote'] = $this->storageConfig((int) $b['storage_id']);
        }
        $res = (array) $this->agent->call('backup.restore', $params);
        $this->audit->record('backup.restore', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'backup' => $b['backup_id'], 'files' => $files, 'databases' => array_keys($map)]);
        return (string) $res['job_id'];
    }

    public const SERVER_COMPONENTS = [
        'nginx' => 'Nginx (/etc/nginx)',
        'php' => 'PHP (/etc/php)',
        'mysql' => 'MySQL/MariaDB settings (/etc/mysql; restarts the database)',
        'redis' => 'Redis (/etc/redis; restarts Redis)',
        'letsencrypt' => 'Certificates (/etc/letsencrypt)',
        'fail2ban' => 'Fail2ban (/etc/fail2ban)',
        'cron' => 'System cron jobs (/etc/cron.d)',
    ];

    /**
     * Restore parts of a server configuration backup, or extract it for
     * manual review. Returns the job id.
     *
     * @param array<string, mixed> $b
     * @param list<string> $components
     */
    public function restoreServer(array $b, array $components, bool $extractOnly, string $confirm): string
    {
        if ($b['status'] !== 'completed' || $b['kind'] !== 'server') {
            throw new UserFacingException('Only completed server backups can be restored.');
        }
        $components = array_values(array_unique(array_intersect($components, array_keys(self::SERVER_COMPONENTS))));
        if (!$extractOnly && $components === []) {
            throw new UserFacingException('Choose what to restore, or extract the backup for review.');
        }
        if (!$extractOnly && strtolower(trim($confirm)) !== 'restore') {
            throw new UserFacingException('Type "restore" to confirm.');
        }
        $params = ['id' => $b['backup_id'], 'components' => $extractOnly ? [] : $components, 'extract_only' => $extractOnly, 'confirm' => true];
        if ($b['storage_id'] !== null) {
            $params['remote'] = $this->storageConfig((int) $b['storage_id']);
        }
        $res = (array) $this->agent->call('backup.server_restore', $params);
        $this->audit->record($extractOnly ? 'backup.server_extract' : 'backup.server_restore', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['backup' => $b['backup_id'], 'components' => $components]);
        return (string) $res['job_id'];
    }

    /**
     * First chunk of a stored part (for streaming downloads).
     *
     * @param array<string, mixed> $b
     * @return array{0: array<string, mixed>, 1: callable(int): array<string, mixed>}
     */
    public function download(array $b, string $file): array
    {
        $names = array_map(static fn (array $p): string => (string) $p['name'], $b['parts']);
        if (!in_array($file, $names, true) || !$b['local']) {
            throw new UserFacingException('That file is not stored on this server.', $b['local'] ? '' : 'This backup is kept in remote storage only; download it from there.');
        }
        $params = $this->target($b) + ['file' => $file];
        $next = fn (int $offset): array => (array) $this->agent->call('backup.download', $params + ['offset' => $offset], 120);
        $first = $next(0);
        $this->audit->record('backup.download', $b['kind'] === 'server' ? 'server' : 'website', (string) ($b['website_id'] ?? $b['server_id']), AuditLogger::SUCCESS, ['backup' => $b['backup_id'], 'file' => $file]);
        return [$first, $next];
    }

    /** @return array<string, mixed>|null */
    public function keyInfo(): ?array
    {
        try {
            return (array) $this->agent->call('backup.key_info');
        } catch (AgentException) {
            return null;
        }
    }

    /**
     * The backup key encrypted with a passphrase (PBKDF2 + AES-GCM, done by
     * the agent): a key file to keep safe, useless without the passphrase.
     *
     * @return array{key_id: string, file: string}
     */
    public function exportKey(string $passphrase): array
    {
        $res = (array) $this->agent->call('backup.key_export', ['passphrase' => $passphrase], 60);
        $this->audit->record('backup.key_export', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['key_id' => (string) ($res['key_id'] ?? '')]);
        return ['key_id' => (string) ($res['key_id'] ?? ''), 'file' => (string) ($res['file'] ?? '')];
    }

    /**
     * Install a backup key from a key file, on a server that has none yet.
     *
     * @return array{key_id: string, already: bool}
     */
    public function importKey(string $file, string $passphrase): array
    {
        try {
            $res = (array) $this->agent->call('backup.key_import', ['file' => $file, 'passphrase' => $passphrase], 60);
        } catch (AgentException $e) {
            $this->audit->record('backup.key_import', 'server', (string) $this->server->id(), AuditLogger::FAILURE, ['error' => mb_substr($e->getMessage() . ' ' . $e->reason(), 0, 300)]);
            throw $e;
        }
        $this->audit->record('backup.key_import', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['key_id' => (string) ($res['key_id'] ?? ''), 'already' => !empty($res['already'])]);
        return ['key_id' => (string) ($res['key_id'] ?? ''), 'already' => !empty($res['already'])];
    }
}
