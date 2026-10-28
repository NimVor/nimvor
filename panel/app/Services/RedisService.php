<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Crypto;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * The local Redis server: status, memory policy, password, per-website
 * ACL users (restricted to a key prefix) and flushing a database.
 * Generated passwords are shown once and never stored by the panel.
 */
final class RedisService
{
    public const POLICIES = [
        'noeviction' => 'No eviction (writes fail when full)',
        'allkeys-lru' => 'Evict least recently used keys (cache)',
        'allkeys-lfu' => 'Evict least frequently used keys (cache)',
        'volatile-lru' => 'Evict least recently used keys with an expiry',
        'volatile-ttl' => 'Evict keys closest to expiry',
        'allkeys-random' => 'Evict random keys',
        'volatile-lfu' => 'Evict least frequently used keys with an expiry',
        'volatile-random' => 'Evict random keys with an expiry',
    ];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
    }

    /** @return array<string, mixed> */
    public function info(): array
    {
        return (array) $this->agent->call('redis.info');
    }

    public function configure(int $maxMemoryMb, string $policy): void
    {
        if ($maxMemoryMb < 0 || $maxMemoryMb > 1048576) {
            throw new ValidationException(['maxmemory_mb' => 'Enter 0 (no limit) or a size in MB.']);
        }
        if (!isset(self::POLICIES[$policy])) {
            throw new ValidationException(['policy' => 'Choose an eviction policy.']);
        }
        $this->agent->call('redis.configure', ['maxmemory_mb' => $maxMemoryMb, 'policy' => $policy]);
        $this->audit->record('redis.configure', 'service', 'redis', AuditLogger::SUCCESS, ['maxmemory_mb' => $maxMemoryMb, 'policy' => $policy]);
    }

    /** Set a new random password (returned once) or clear it. */
    public function password(bool $clear): string
    {
        $pw = $clear ? '' : Crypto::password(32);
        $this->agent->call('redis.password', ['password' => $pw]);
        $this->audit->record($clear ? 'redis.password_clear' : 'redis.password_set', 'service', 'redis');
        return $pw;
    }

    public function flush(int $db, string $confirm): void
    {
        if ($confirm !== 'db' . $db) {
            throw new UserFacingException('Type db' . $db . ' to confirm.');
        }
        $this->agent->call('redis.flush', ['db' => $db, 'confirm' => $confirm], 120);
        $this->audit->record('redis.flush', 'service', 'redis', AuditLogger::SUCCESS, ['db' => $db]);
    }

    /**
     * Create (or reset) an ACL user for a website, limited to "<user>:*" keys.
     *
     * @return array{user: string, password: string, prefix: string}
     */
    public function createSiteUser(int $websiteId): array
    {
        $site = $this->sites->find($this->server->id(), $websiteId, null) ?? throw new UserFacingException('Website not found.');
        $user = strtolower((string) $site['system_user']);
        $prefix = $user . ':';
        $pw = Crypto::password(32);
        $this->agent->call('redis.acl_set', ['user' => $user, 'password' => $pw, 'prefix' => $prefix]);
        $this->audit->record('redis.acl_set', 'website', (string) $websiteId, AuditLogger::SUCCESS, ['user' => $user, 'prefix' => $prefix]);
        return ['user' => $user, 'password' => $pw, 'prefix' => $prefix];
    }

    public function deleteUser(string $user): void
    {
        if (!preg_match('/^[a-z][a-z0-9_-]{1,31}$/', $user) || $user === 'default') {
            throw new UserFacingException('That Redis user cannot be removed here.');
        }
        $this->agent->call('redis.acl_del', ['user' => $user]);
        $this->audit->record('redis.acl_del', 'service', 'redis', AuditLogger::SUCCESS, ['user' => $user]);
    }
}
