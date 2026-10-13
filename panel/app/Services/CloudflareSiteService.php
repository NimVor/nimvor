<?php

declare(strict_types=1);

namespace WhaCP\Services;

use Closure;
use WhaCP\Database\Connection;
use WhaCP\Security\Crypto;
use WhaCP\Support\UserFacingException;

/**
 * Per-website Cloudflare for the customer portal. A customer connects their
 * own Cloudflare API token (scoped to their own zone); WhaCP finds the zone of
 * that token whose name matches the website's domain and lets them toggle the
 * common free-plan settings for just that zone. The token is stored encrypted,
 * per website, and never leaves the panel except as a Bearer header to
 * Cloudflare's own API.
 */
final class CloudflareSiteService
{
    public const API = 'https://api.cloudflare.com/client/v4';
    public const SSL_MODES = CloudflareService::SSL_MODES;

    /** @var Closure(string, string, ?array, string): array{status: int, body: array<string, mixed>} */
    private Closure $transport;

    public function __construct(
        private readonly Connection $db,
        private readonly Crypto $crypto,
        private readonly AuditLogger $audit,
    ) {
        $this->transport = self::curlTransport();
    }

    public function setTransport(Closure $transport): void
    {
        $this->transport = $transport;
    }

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

    /** @return array<string, mixed>|null the stored row (without the token) */
    public function connection(array $site): ?array
    {
        $row = $this->row((int) $site['id']);
        if ($row === null) {
            return null;
        }
        unset($row['token_enc']);
        return $row;
    }

    /** @return array<string, mixed>|null */
    private function row(int $websiteId): ?array
    {
        return $this->db->fetch('SELECT * FROM cloudflare_sites WHERE website_id = ?', [$websiteId]);
    }

    private function token(int $websiteId): string
    {
        $row = $this->row($websiteId);
        return $row === null ? '' : $this->crypto->decrypt((string) $row['token_enc']);
    }

    /**
     * @param array<string, mixed>|null $body
     * @return array<string, mixed> the "result" payload
     */
    private function call(string $token, string $method, string $path, ?array $body = null): array
    {
        if ($token === '') {
            throw new UserFacingException('Cloudflare is not connected for this website.');
        }
        $res = ($this->transport)($method, self::API . $path, $body, $token);
        $payload = $res['body'];
        if (($res['status'] < 200 || $res['status'] >= 300) || empty($payload['success'])) {
            foreach ((array) ($payload['errors'] ?? []) as $e) {
                if (is_array($e) && isset($e['message'])) {
                    throw new UserFacingException('Cloudflare: ' . (string) $e['message']);
                }
            }
            throw new UserFacingException('Cloudflare: request failed (HTTP ' . $res['status'] . ').');
        }
        return is_array($payload['result'] ?? null) ? $payload['result'] : [];
    }

    /** Verify a token, find the zone for this site's domain, and store both. */
    public function connect(array $site, string $token, ?int $userId): void
    {
        $token = trim($token);
        if (!preg_match('/^[A-Za-z0-9_-]{20,120}$/', $token)) {
            throw new UserFacingException('That does not look like a Cloudflare API token.');
        }
        $this->call($token, 'GET', '/user/tokens/verify');
        $domain = strtolower((string) $site['domain']);
        $best = null;
        foreach ($this->call($token, 'GET', '/zones?per_page=50') as $z) {
            $z = (array) $z;
            $name = strtolower((string) ($z['name'] ?? ''));
            if ($name !== '' && ($domain === $name || str_ends_with($domain, '.' . $name))) {
                if ($best === null || strlen($name) > strlen((string) $best['name'])) {
                    $best = ['id' => (string) ($z['id'] ?? ''), 'name' => $name];
                }
            }
        }
        if ($best === null) {
            throw new UserFacingException('This token has no Cloudflare zone that matches ' . $domain . '. Add the domain to Cloudflare first, and give the token access to that zone.');
        }
        $now = gmdate('c');
        $this->db->run(
            'INSERT INTO cloudflare_sites (website_id, token_enc, zone_id, zone_name, verified_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
             ON CONFLICT(website_id) DO UPDATE SET token_enc = excluded.token_enc, zone_id = excluded.zone_id, zone_name = excluded.zone_name, verified_at = excluded.verified_at, updated_at = excluded.updated_at',
            [(int) $site['id'], $this->crypto->encrypt($token), $best['id'], $best['name'], $now, $now, $now]
        );
        $this->audit->record('cloudflare.site_connect', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['zone' => $best['name']], $userId);
    }

    public function disconnect(array $site, ?int $userId): void
    {
        $this->db->run('DELETE FROM cloudflare_sites WHERE website_id = ?', [(int) $site['id']]);
        $this->audit->record('cloudflare.site_disconnect', 'website', (string) $site['id'], AuditLogger::SUCCESS, [], $userId);
    }

    /**
     * Live zone status for the site (SSL mode, Always HTTPS, proxiable records
     * for this domain and its www). Returns null when not connected; the
     * 'error' key is set when the API could not be reached.
     *
     * @return array<string, mixed>|null
     */
    public function status(array $site): ?array
    {
        $row = $this->row((int) $site['id']);
        if ($row === null) {
            return null;
        }
        $out = ['zone_id' => (string) $row['zone_id'], 'zone_name' => (string) $row['zone_name'], 'verified_at' => (string) $row['verified_at']];
        try {
            $token = $this->token((int) $site['id']);
            $out['ssl'] = (string) ($this->call($token, 'GET', '/zones/' . $row['zone_id'] . '/settings/ssl')['value'] ?? '');
            $out['always_https'] = (string) ($this->call($token, 'GET', '/zones/' . $row['zone_id'] . '/settings/always_use_https')['value'] ?? '') === 'on';
            $domain = strtolower((string) $site['domain']);
            $records = [];
            foreach ($this->call($token, 'GET', '/zones/' . $row['zone_id'] . '/dns_records?per_page=100') as $r) {
                $r = (array) $r;
                $name = strtolower((string) ($r['name'] ?? ''));
                if (($name === $domain || $name === 'www.' . $domain) && !empty($r['proxiable'])) {
                    $records[] = ['id' => (string) $r['id'], 'type' => (string) $r['type'], 'name' => (string) $r['name'], 'content' => (string) $r['content'], 'proxied' => !empty($r['proxied'])];
                }
            }
            $out['records'] = $records;
        } catch (UserFacingException $e) {
            $out['error'] = $e->getMessage();
        }
        return $out;
    }

    public function setSsl(array $site, string $mode, ?int $userId): void
    {
        if (!isset(self::SSL_MODES[$mode])) {
            throw new UserFacingException('Unknown SSL mode.');
        }
        $zone = $this->zoneId($site);
        $this->call($this->token((int) $site['id']), 'PATCH', '/zones/' . $zone . '/settings/ssl', ['value' => $mode]);
        $this->audit->record('cloudflare.site_ssl', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['mode' => $mode], $userId);
    }

    public function setAlwaysHttps(array $site, bool $on, ?int $userId): void
    {
        $zone = $this->zoneId($site);
        $this->call($this->token((int) $site['id']), 'PATCH', '/zones/' . $zone . '/settings/always_use_https', ['value' => $on ? 'on' : 'off']);
        $this->audit->record('cloudflare.site_always_https', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['on' => $on], $userId);
    }

    public function setProxied(array $site, string $recordId, bool $on, ?int $userId): void
    {
        $this->assertId($recordId);
        $zone = $this->zoneId($site);
        $this->call($this->token((int) $site['id']), 'PATCH', '/zones/' . $zone . '/dns_records/' . $recordId, ['proxied' => $on]);
        $this->audit->record('cloudflare.site_proxy', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['record' => $recordId, 'proxied' => $on], $userId);
    }

    public function purge(array $site, ?int $userId): void
    {
        $zone = $this->zoneId($site);
        $this->call($this->token((int) $site['id']), 'POST', '/zones/' . $zone . '/purge_cache', ['purge_everything' => true]);
        $this->audit->record('cloudflare.site_purge', 'website', (string) $site['id'], AuditLogger::SUCCESS, [], $userId);
    }

    private function zoneId(array $site): string
    {
        $row = $this->row((int) $site['id']) ?? throw new UserFacingException('Cloudflare is not connected for this website.');
        $id = (string) $row['zone_id'];
        $this->assertId($id);
        return $id;
    }

    private function assertId(string $id): void
    {
        if (!preg_match('/^[a-f0-9]{32}$/', $id)) {
            throw new UserFacingException('Invalid Cloudflare identifier.');
        }
    }
}
