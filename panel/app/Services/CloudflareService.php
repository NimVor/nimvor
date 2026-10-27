<?php

declare(strict_types=1);

namespace WhaCP\Services;

use Closure;
use WhaCP\Database\Connection;
use WhaCP\Security\Crypto;
use WhaCP\Support\UserFacingException;

/**
 * Cloudflare integration (free plan CDN/DNS). One API token per server is
 * stored encrypted; the token talks to the Cloudflare v4 API to list zones,
 * manage their DNS records, toggle the proxy (orange cloud), set the SSL
 * mode and Always Use HTTPS, and purge the cache.
 *
 * All HTTP goes through an injectable transport so the service is testable
 * without the network.
 */
final class CloudflareService
{
    public const API = 'https://api.cloudflare.com/client/v4';
    public const SSL_MODES = ['off' => 'Off', 'flexible' => 'Flexible', 'full' => 'Full', 'strict' => 'Full (strict)'];

    /** @var Closure(string, string, ?array, string): array{status: int, body: array<string, mixed>} */
    private Closure $transport;

    public function __construct(
        private readonly Connection $db,
        private readonly Crypto $crypto,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
    ) {
        $this->transport = self::curlTransport();
    }

    /** Replace the HTTP transport (tests). */
    public function setTransport(Closure $transport): void
    {
        $this->transport = $transport;
    }

    /** @return Closure(string, string, ?array, string): array{status: int, body: array<string, mixed>} */
    private static function curlTransport(): Closure
    {
        return static function (string $method, string $url, ?array $body, string $token): array {
            $ch = curl_init($url);
            $headers = ['Authorization: Bearer ' . $token, 'Content-Type: application/json', 'Accept: application/json'];
            $opts = [
                CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 20, CURLOPT_CONNECTTIMEOUT => 8,
                CURLOPT_CUSTOMREQUEST => $method, CURLOPT_HTTPHEADER => $headers, CURLOPT_FOLLOWLOCATION => false,
            ];
            if ($body !== null) {
                $opts[CURLOPT_POSTFIELDS] = (string) json_encode($body);
            }
            curl_setopt_array($ch, $opts);
            $raw = curl_exec($ch);
            $status = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
            curl_close($ch);
            $decoded = is_string($raw) ? json_decode($raw, true) : null;
            return ['status' => $status, 'body' => is_array($decoded) ? $decoded : []];
        };
    }

    public function connected(): bool
    {
        return $this->row() !== null;
    }

    /** @return array<string, mixed>|null */
    private function row(): ?array
    {
        return $this->db->fetch('SELECT * FROM cloudflare_accounts WHERE server_id = ?', [$this->server->id()]);
    }

    private function token(): string
    {
        $row = $this->row();
        return $row === null ? '' : $this->crypto->decrypt((string) $row['token_enc']);
    }

    /** @return array<string, mixed>|null the stored account (without the token) */
    public function account(): ?array
    {
        $row = $this->row();
        if ($row === null) {
            return null;
        }
        unset($row['token_enc']);
        return $row;
    }

    /**
     * Call the Cloudflare API with the stored token.
     *
     * @param array<string, mixed>|null $body
     * @return array<string, mixed> the "result" payload
     */
    private function call(string $method, string $path, ?array $body = null, ?string $token = null): array
    {
        $token ??= $this->token();
        if ($token === '') {
            throw new UserFacingException('Cloudflare is not connected. Add an API token first.');
        }
        $res = ($this->transport)($method, self::API . $path, $body, $token);
        $payload = $res['body'];
        if (($res['status'] < 200 || $res['status'] >= 300) || empty($payload['success'])) {
            throw new UserFacingException('Cloudflare: ' . $this->errorText($payload, $res['status']));
        }
        return is_array($payload['result'] ?? null) ? $payload['result'] : [];
    }

    /** @param array<string, mixed> $payload */
    private function errorText(array $payload, int $status): string
    {
        foreach ((array) ($payload['errors'] ?? []) as $e) {
            if (is_array($e) && isset($e['message'])) {
                return (string) $e['message'];
            }
        }
        return 'request failed (HTTP ' . $status . ').';
    }

    /** Verify a token and store it (encrypted) for this server. */
    public function connect(string $token, ?int $userId): void
    {
        $token = trim($token);
        if ($token === '' || !preg_match('/^[A-Za-z0-9_-]{20,120}$/', $token)) {
            throw new UserFacingException('That does not look like a Cloudflare API token.');
        }
        // /user/tokens/verify confirms the token is active.
        $this->call('GET', '/user/tokens/verify', null, $token);
        $now = gmdate('c');
        $this->db->run(
            'INSERT INTO cloudflare_accounts (server_id, token_enc, verified_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
             ON CONFLICT(server_id) DO UPDATE SET token_enc = excluded.token_enc, verified_at = excluded.verified_at, updated_at = excluded.updated_at',
            [$this->server->id(), $this->crypto->encrypt($token), $now, $now, $now]
        );
        $this->audit->record('cloudflare.connect', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, [], $userId);
    }

    public function disconnect(?int $userId): void
    {
        $this->db->run('DELETE FROM cloudflare_accounts WHERE server_id = ?', [$this->server->id()]);
        $this->audit->record('cloudflare.disconnect', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, [], $userId);
    }

    /** @return list<array<string, mixed>> the account's zones */
    public function zones(): array
    {
        $out = [];
        foreach ($this->call('GET', '/zones?per_page=50&status=active,pending') as $z) {
            $z = (array) $z;
            $out[] = ['id' => (string) ($z['id'] ?? ''), 'name' => (string) ($z['name'] ?? ''), 'status' => (string) ($z['status'] ?? ''), 'name_servers' => array_map('strval', (array) ($z['name_servers'] ?? []))];
        }
        return $out;
    }

    /** @return array<string, mixed> zone + its settings and records */
    public function zone(string $zoneId): array
    {
        $this->assertZoneId($zoneId);
        $z = $this->call('GET', '/zones/' . $zoneId);
        return [
            'id' => $zoneId,
            'name' => (string) ($z['name'] ?? ''),
            'status' => (string) ($z['status'] ?? ''),
            'name_servers' => array_map('strval', (array) ($z['name_servers'] ?? [])),
            'ssl' => $this->setting($zoneId, 'ssl'),
            'always_https' => $this->setting($zoneId, 'always_use_https') === 'on',
            'records' => $this->records($zoneId),
        ];
    }

    private function setting(string $zoneId, string $key): string
    {
        $r = $this->call('GET', '/zones/' . $zoneId . '/settings/' . $key);
        return (string) ($r['value'] ?? '');
    }

    /** @return list<array<string, mixed>> */
    public function records(string $zoneId): array
    {
        $this->assertZoneId($zoneId);
        $out = [];
        foreach ($this->call('GET', '/zones/' . $zoneId . '/dns_records?per_page=100') as $r) {
            $r = (array) $r;
            $out[] = ['id' => (string) ($r['id'] ?? ''), 'type' => (string) ($r['type'] ?? ''), 'name' => (string) ($r['name'] ?? ''), 'content' => (string) ($r['content'] ?? ''), 'proxied' => !empty($r['proxied']), 'proxiable' => !empty($r['proxiable'])];
        }
        return $out;
    }

    public function setProxied(string $zoneId, string $recordId, bool $proxied, ?int $userId): void
    {
        $this->assertZoneId($zoneId);
        $this->assertZoneId($recordId);
        $this->call('PATCH', '/zones/' . $zoneId . '/dns_records/' . $recordId, ['proxied' => $proxied]);
        $this->audit->record('cloudflare.proxy', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['zone' => $zoneId, 'record' => $recordId, 'proxied' => $proxied], $userId);
    }

    public function setSslMode(string $zoneId, string $mode, ?int $userId): void
    {
        $this->assertZoneId($zoneId);
        if (!isset(self::SSL_MODES[$mode])) {
            throw new UserFacingException('Unknown SSL mode.');
        }
        $this->call('PATCH', '/zones/' . $zoneId . '/settings/ssl', ['value' => $mode]);
        $this->audit->record('cloudflare.ssl', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['zone' => $zoneId, 'mode' => $mode], $userId);
    }

    public function setAlwaysHttps(string $zoneId, bool $on, ?int $userId): void
    {
        $this->assertZoneId($zoneId);
        $this->call('PATCH', '/zones/' . $zoneId . '/settings/always_use_https', ['value' => $on ? 'on' : 'off']);
        $this->audit->record('cloudflare.always_https', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['zone' => $zoneId, 'on' => $on], $userId);
    }

    public function purge(string $zoneId, ?int $userId): void
    {
        $this->assertZoneId($zoneId);
        $this->call('POST', '/zones/' . $zoneId . '/purge_cache', ['purge_everything' => true]);
        $this->audit->record('cloudflare.purge', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['zone' => $zoneId], $userId);
    }

    private function assertZoneId(string $id): void
    {
        if (!preg_match('/^[a-f0-9]{32}$/', $id)) {
            throw new UserFacingException('Invalid Cloudflare identifier.');
        }
    }
}
