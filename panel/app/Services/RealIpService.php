<?php

declare(strict_types=1);

namespace NimVor\Services;

use Closure;
use NimVor\Agent\AgentClient;
use NimVor\Support\UserFacingException;

/**
 * Restoring the visitor's own address for websites behind Cloudflare (Server → Cloudflare). The agent
 * writes the Nginx setting, which trusts the CF-Connecting-IP header only from Cloudflare's networks;
 * this service switches it and can fetch Cloudflare's current list of networks (a public page, no
 * account needed).
 */
final class RealIpService
{
    public const IPS_URL = 'https://api.cloudflare.com/client/v4/ips';

    /** @var Closure(string): array{status: int, body: array<string, mixed>} */
    private Closure $fetch;

    public function __construct(
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
    ) {
        $this->fetch = static function (string $url): array {
            $ch = curl_init($url);
            curl_setopt_array($ch, [CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 15, CURLOPT_CONNECTTIMEOUT => 8, CURLOPT_FOLLOWLOCATION => false, CURLOPT_HTTPHEADER => ['Accept: application/json']]);
            $raw = curl_exec($ch);
            $status = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
            curl_close($ch);
            $decoded = is_string($raw) ? json_decode($raw, true) : null;
            return ['status' => $status, 'body' => is_array($decoded) ? $decoded : []];
        };
    }

    /** @param Closure(string): array{status: int, body: array<string, mixed>} $fetch */
    public function setFetcher(Closure $fetch): void
    {
        $this->fetch = $fetch;
    }

    /** @return array{enabled: bool, custom: bool, ranges: list<string>, header: string, file_present: bool} */
    public function status(): array
    {
        $s = (array) $this->agent->call('realip.status');
        return [
            'enabled' => !empty($s['enabled']),
            'custom' => !empty($s['custom']),
            'ranges' => array_values(array_map('strval', (array) ($s['ranges'] ?? []))),
            'header' => (string) ($s['header'] ?? 'CF-Connecting-IP'),
            'file_present' => !empty($s['file_present']),
        ];
    }

    public function setEnabled(bool $enabled, ?int $userId): void
    {
        $this->agent->call('realip.set', ['enabled' => $enabled], 120);
        $this->audit->record('realip.set', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['enabled' => $enabled], $userId);
    }

    /** Takes Cloudflare's current networks and applies them (keeping the setting on or off as it is). */
    public function refresh(?int $userId): int
    {
        try {
            $res = ($this->fetch)(self::IPS_URL);
        } catch (\Throwable) {
            throw new UserFacingException('Cloudflare could not be reached. Try again in a moment.');
        }
        $result = (array) ($res['body']['result'] ?? []);
        $ranges = [];
        foreach (['ipv4_cidrs', 'ipv6_cidrs'] as $k) {
            foreach ((array) ($result[$k] ?? []) as $c) {
                if (is_string($c) && preg_match('#^[0-9a-fA-F:.]+/\d{1,3}$#', $c) === 1) {
                    $ranges[] = $c;
                }
            }
        }
        if ($res['status'] !== 200 || $ranges === [] || count($ranges) > 100) {
            throw new UserFacingException('Cloudflare did not return its list of networks. Nothing was changed.');
        }
        $enabled = $this->status()['enabled'];
        $this->agent->call('realip.set', ['enabled' => $enabled, 'ranges' => $ranges], 120);
        $this->audit->record('realip.refresh', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['networks' => count($ranges)], $userId);
        return count($ranges);
    }
}
