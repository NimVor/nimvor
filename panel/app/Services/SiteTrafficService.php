<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * A website's traffic protection: traffic limits (requests per second or minute from one address to a URL or the
 * whole website, above which Nginx answers with an error). The agent writes the Nginx rules, checks them and rolls
 * back when Nginx refuses; every change is audited.
 */
final class SiteTrafficService
{
    public const MAX_LIMITS = 20;
    /** What Nginx can answer when a limit trips. */
    public const STATUSES = [429 => '429 Too Many Requests', 503 => '503 Service Unavailable', 444 => 'Close the connection (no answer)'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @return array{limits: list<array<string, mixed>>, status: int}
     */
    public function limits(array $site): array
    {
        $res = (array) $this->agent->call('protect.status', ['domain' => (string) $site['domain']]);
        $limits = [];
        foreach ((array) ($res['limits'] ?? []) as $l) {
            $limits[] = (array) $l;
        }
        return ['limits' => $limits, 'status' => (int) ($res['limit_status'] ?? 429)];
    }

    /**
     * Adds a rule, or changes the one with the same name.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $in name, match, path, rate, unit, burst, per_ua
     */
    public function save(array $site, array $in, int $userId): string
    {
        self::writable($site);
        $rule = self::rule($in);
        $cur = $this->limits($site);
        $list = $cur['limits'];
        $found = false;
        foreach ($list as $i => $l) {
            if (($l['name'] ?? '') === $rule['name']) {
                $list[$i] = $rule;
                $found = true;
            }
        }
        if (!$found) {
            if (count($list) >= self::MAX_LIMITS) {
                throw new UserFacingException('A website can have at most ' . self::MAX_LIMITS . ' traffic limits.', status: 409);
            }
            $list[] = $rule;
        }
        $this->send($site, $list, $cur['status'], 'site_traffic.limit_save', ['limit' => $rule['name'], 'path' => $rule['path'], 'rate' => $rule['rate'] . '/' . $rule['unit']], $userId);
        return ($found ? 'The limit ' : 'The new limit ') . $rule['name'] . ' is on: more than ' . $rule['rate'] . ' request(s) per ' . ($rule['unit'] === 'm' ? 'minute' : 'second') . ' from one address to ' . $site['domain'] . $rule['path'] . ' gets an error.';
    }

    /** @param array<string, mixed> $site */
    public function remove(array $site, string $name, int $userId): string
    {
        self::writable($site);
        $cur = $this->limits($site);
        $list = array_values(array_filter($cur['limits'], static fn (array $l): bool => ($l['name'] ?? '') !== $name));
        if (count($list) === count($cur['limits'])) {
            throw new UserFacingException('That limit does not exist.', status: 404);
        }
        $this->send($site, $list, $cur['status'], 'site_traffic.limit_remove', ['limit' => $name], $userId);
        return 'The limit ' . $name . ' was removed.';
    }

    /** @param array<string, mixed> $site */
    public function toggle(array $site, string $name, bool $on, int $userId): string
    {
        self::writable($site);
        $cur = $this->limits($site);
        $found = false;
        $list = $cur['limits'];
        foreach ($list as $i => $l) {
            if (($l['name'] ?? '') === $name) {
                $list[$i]['enabled'] = $on;
                $found = true;
            }
        }
        if (!$found) {
            throw new UserFacingException('That limit does not exist.', status: 404);
        }
        $this->send($site, $list, $cur['status'], 'site_traffic.limit_toggle', ['limit' => $name, 'enabled' => $on], $userId);
        return 'The limit ' . $name . ' is ' . ($on ? 'on' : 'off') . '.';
    }

    /** The answer a tripped limit gives (for all of the website's limits). @param array<string, mixed> $site */
    public function setStatus(array $site, int $status, int $userId): string
    {
        self::writable($site);
        if (!isset(self::STATUSES[$status])) {
            throw new ValidationException(['status' => 'Choose 429, 503 or close the connection.']);
        }
        $cur = $this->limits($site);
        $this->send($site, $cur['limits'], $status, 'site_traffic.limit_status', ['status' => $status], $userId);
        return 'Visitors over a limit now get: ' . self::STATUSES[$status] . '.';
    }

    /**
     * @param array<string, mixed> $in
     * @return array<string, mixed>
     */
    public static function rule(array $in): array
    {
        $errors = [];
        $name = strtolower(trim((string) ($in['name'] ?? '')));
        if (preg_match('/^[a-z0-9][a-z0-9_-]{0,39}$/', $name) !== 1) {
            $errors['name'] = 'Use 1 to 40 lowercase letters, digits, dashes or underscores.';
        }
        $match = (string) ($in['match'] ?? 'prefix');
        if (!in_array($match, ['prefix', 'exact'], true)) {
            $errors['match'] = 'Choose a folder (and everything below it) or one exact address.';
        }
        $path = trim((string) ($in['path'] ?? ''));
        if ($path === '' || $path[0] !== '/') {
            $path = '/' . $path;
        }
        if (preg_match('#^/[A-Za-z0-9._~/-]{0,198}$#', $path) !== 1 || str_contains($path, '..') || str_contains($path, '//')) {
            $errors['path'] = 'Use a URL path such as / (the whole website), /api/ or /wp-login.php.';
        }
        $unit = (string) ($in['unit'] ?? 's');
        $rate = (int) ($in['rate'] ?? 0);
        if (!in_array($unit, ['s', 'm'], true)) {
            $errors['unit'] = 'Choose per second or per minute.';
        } elseif ($rate < 1 || $rate > ($unit === 's' ? 1000 : 60000)) {
            $errors['rate'] = $unit === 's' ? 'Use 1 to 1000 requests per second.' : 'Use 1 to 60000 requests per minute.';
        }
        $burst = (int) ($in['burst'] ?? 0);
        if ($burst < 0 || $burst > 1000) {
            $errors['burst'] = 'Use 0 to 1000.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['name' => $name, 'match' => $match, 'path' => $path, 'rate' => $rate, 'unit' => $unit, 'burst' => $burst, 'per_ua' => !empty($in['per_ua']), 'enabled' => true];
    }

    /**
     * @param array<string, mixed> $site
     * @param list<array<string, mixed>> $limits
     * @param array<string, mixed> $details
     */
    private function send(array $site, array $limits, int $status, string $action, array $details, int $userId): void
    {
        $this->agent->call('protect.limits', ['domain' => (string) $site['domain'], 'status' => $status, 'limits' => $limits], 120);
        $this->audit->record($action, 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain']] + $details, $userId);
    }

    /** @param array<string, mixed> $site */
    private static function writable(array $site): void
    {
        if (!empty($site['account_suspended'])) {
            throw new UserFacingException('The hosting account is suspended; traffic protection cannot be changed.', status: 409);
        }
    }
}
