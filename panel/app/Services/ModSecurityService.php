<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * ModSecurity (web application firewall). Administrators set it up
 * server-wide (on/off, the OWASP Core Rule Set's paranoia level and
 * thresholds, custom rules) and per website (mode, which rule vendors
 * apply, single rules turned off). A website's owner may only switch its
 * own website between on, detection only and off; the rules stay with
 * administrators. The agent checks every change with nginx -t and rolls
 * it back on failure.
 */
final class ModSecurityService
{
    public const MODES = ['off' => 'Off', 'detect' => 'Detection only (log, do not block)', 'on' => 'On (block attacks)'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
    ) {
    }

    /** @return array<string, mixed> */
    public function status(): array
    {
        return self::arr($this->agent->call('modsec.status'));
    }

    /** @return array<string, mixed> */
    private static function arr(mixed $v): array
    {
        return (array) json_decode((string) json_encode($v), true);
    }

    /**
     * @param array<string, mixed> $in form input
     */
    public function configure(array $in, int $userId): void
    {
        $num = static function (mixed $v, int $lo, int $hi, string $label): int {
            if (!is_numeric($v) || (int) $v < $lo || (int) $v > $hi) {
                throw new ValidationException([$label => "{$label}: {$lo} to {$hi}."]);
            }
            return (int) $v;
        };
        $cfg = [
            'enabled' => ($in['enabled'] ?? '') === '1',
            'crs' => [
                'enabled' => ($in['crs_enabled'] ?? '') === '1',
                'paranoia' => $num($in['paranoia'] ?? 1, 1, 4, 'Paranoia level'),
                'inbound_threshold' => $num($in['inbound_threshold'] ?? 5, 2, 1000, 'Inbound threshold'),
                'outbound_threshold' => $num($in['outbound_threshold'] ?? 4, 2, 1000, 'Outbound threshold'),
            ],
            'custom' => ['enabled' => ($in['custom_enabled'] ?? '') === '1'],
        ];
        $this->agent->call('modsec.configure', $cfg, 120);
        $this->audit->record('modsec.configure', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $cfg, $userId);
    }

    public function customRules(): string
    {
        return (string) (self::arr($this->agent->call('modsec.custom_get'))['rules'] ?? '');
    }

    public function saveCustomRules(string $rules, int $userId): int
    {
        if (strlen($rules) > 65536) {
            throw new UserFacingException('The rules are larger than 64 KiB.');
        }
        $n = (int) (self::arr($this->agent->call('modsec.custom_set', ['rules' => $rules], 120))['rules'] ?? 0);
        $this->audit->record('modsec.custom_rules', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['rules' => $n, 'sha256' => hash('sha256', $rules)], $userId);
        return $n;
    }

    /**
     * A website's setting (from the agent's status).
     *
     * @param array<string, mixed> $status
     * @param array<string, mixed> $site
     * @return array{mode: string, exclude: list<string>, disabled_rules: list<int>}
     */
    public static function siteSetting(array $status, array $site): array
    {
        $s = (array) (($status['sites'] ?? [])[(string) $site['domain']] ?? []);
        return ['mode' => (string) ($s['mode'] ?? 'off'), 'exclude' => array_values(array_map('strval', (array) ($s['exclude'] ?? []))), 'disabled_rules' => array_values(array_map('intval', (array) ($s['disabled_rules'] ?? [])))];
    }

    /**
     * Set a website's ModSecurity. Only administrators change vendors and
     * rules; others keep what is set and change the mode only.
     *
     * @param array<string, mixed> $site
     * @param list<string>|null $exclude null keeps the current value
     * @param list<int>|null $disabled null keeps the current value
     */
    public function setSite(array $site, string $mode, ?array $exclude, ?array $disabled, bool $admin, int $userId): void
    {
        if (!isset(self::MODES[$mode])) {
            throw new UserFacingException('Choose off, detection only or on.');
        }
        $cur = self::siteSetting($this->status(), $site);
        if (!$admin || $exclude === null) {
            $exclude = $cur['exclude'];
        }
        if (!$admin || $disabled === null) {
            $disabled = $cur['disabled_rules'];
        }
        $vendors = ['crs', 'custom'];
        $exclude = array_values(array_intersect($vendors, $exclude));
        $disabled = array_values(array_unique(array_filter(array_map('intval', $disabled), static fn (int $i): bool => $i > 0 && $i <= 9999999)));
        if (count($disabled) > 200) {
            throw new UserFacingException('At most 200 rules can be turned off for a website.');
        }
        $params = ['domain' => (string) $site['domain'], 'mode' => $mode, 'exclude' => $exclude, 'disabled_rules' => $disabled];
        $this->agent->call('modsec.site', $params, 120);
        $this->audit->record('modsec.site', 'website', (string) $site['id'], AuditLogger::SUCCESS, $params, $userId);
    }

    /** Parse "942100, 941100 920350" into IDs. @return list<int> */
    public static function parseIds(string $text): array
    {
        if (trim($text) === '') {
            return [];
        }
        $ids = [];
        foreach (preg_split('/[\s,;]+/', trim($text)) ?: [] as $p) {
            if (!preg_match('/^\d{1,7}$/', $p) || (int) $p < 1) {
                throw new ValidationException(['disabled_rules' => "\"{$p}\" is not a rule ID."]);
            }
            $ids[] = (int) $p;
        }
        return array_values(array_unique($ids));
    }

    /**
     * Recent events, all or for a website's host names.
     *
     * @param array<string, mixed>|null $site
     * @return list<array<string, mixed>>
     */
    public function events(?array $site, int $limit = 100): array
    {
        $params = ['limit' => $limit];
        if ($site !== null) {
            $names = array_merge([(string) $site['domain'], 'www.' . $site['domain']], (array) ($site['aliases'] ?? []));
            $params['hosts'] = array_values(array_unique(array_map('strtolower', $names)));
        }
        return array_values((array) (self::arr($this->agent->call('modsec.events', $params, 30))['events'] ?? []));
    }

    public function update(int $userId): string
    {
        $job = (string) (self::arr($this->agent->call('modsec.update', [], 60))['job_id'] ?? '');
        $this->jobs->track($job, 'Update ModSecurity rules', 'server', (string) $this->server->id(), $userId);
        $this->audit->record('modsec.update', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['job' => $job], $userId);
        return $job;
    }

    /** Scheduler: keep the audit log small. @return list<string> */
    public function trim(): array
    {
        $n = (int) (self::arr($this->agent->call('modsec.trim', [], 60))['removed_bytes'] ?? 0);
        return $n > 0 ? ['ModSecurity audit log trimmed by ' . $n . ' bytes.'] : [];
    }
}
