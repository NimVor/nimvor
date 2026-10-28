<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

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
        private readonly ?SettingsService $settings = null,
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
            // Websites set to block only log until blocking is allowed.
            'detection_only' => ($in['detection_only'] ?? '') === '1',
        ];
        $this->agent->call('modsec.configure', $cfg, 120);
        $this->audit->record('modsec.configure', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $cfg, $userId);
    }

    public function customRules(): string
    {
        return (string) (self::arr($this->agent->call('modsec.custom_get'))['rules'] ?? '');
    }

    /**
     * The custom rules file as single rules: each directive (with its
     * continuation lines) and the comment lines right above it.
     *
     * @return list<array{text: string, comment: string, id: int|null, phase: string, action: string, msg: string}>
     */
    public static function splitRules(string $file): array
    {
        $rules = [];
        $comment = [];
        $cur = null;
        foreach (preg_split('/\R/', str_replace("\r\n", "\n", $file)) ?: [] as $line) {
            $t = trim($line);
            if ($cur !== null) {
                $cur[] = rtrim($line);
                if (!str_ends_with(rtrim($line), '\\')) {
                    $rules[] = self::describeRule(implode("\n", $cur), implode("\n", $comment));
                    $cur = null;
                    $comment = [];
                }
                continue;
            }
            if ($t === '') {
                $comment = [];
                continue;
            }
            if (str_starts_with($t, '#')) {
                $comment[] = trim(ltrim($t, '#'));
                continue;
            }
            if (str_ends_with(rtrim($line), '\\')) {
                $cur = [rtrim($line)];
                continue;
            }
            $rules[] = self::describeRule(rtrim($line), implode("\n", $comment));
            $comment = [];
        }
        if ($cur !== null) {
            $rules[] = self::describeRule(implode("\n", $cur), implode("\n", $comment));
        }
        return $rules;
    }

    /** @return array{text: string, comment: string, id: int|null, phase: string, action: string, msg: string} */
    private static function describeRule(string $text, string $comment): array
    {
        $id = preg_match('/\bid:(\d+)/', $text, $m) ? (int) $m[1] : null;
        $phase = preg_match('/\bphase:(\w+)/', $text, $m) ? $m[1] : '';
        $action = preg_match('/\b(deny|block|drop|allow|pass|redirect)\b/', $text, $m) ? $m[1] : '';
        $msg = preg_match("/\bmsg:'([^']*)'/", $text, $m) ? $m[1] : '';
        return ['text' => $text, 'comment' => $comment, 'id' => $id, 'phase' => $phase, 'action' => $action, 'msg' => $msg];
    }

    /** @param list<array{text: string, comment: string}> $rules */
    public static function joinRules(array $rules): string
    {
        $out = [];
        foreach ($rules as $r) {
            $c = trim($r['comment']);
            $out[] = ($c !== '' ? implode("\n", array_map(static fn (string $l): string => '# ' . $l, explode("\n", $c))) . "\n" : '') . trim($r['text']);
        }
        return $out === [] ? '' : implode("\n\n", $out) . "\n";
    }

    /** A free rule ID for a new custom rule (10000–99999). @param list<array{id: int|null}> $rules */
    public static function nextRuleId(array $rules): int
    {
        $used = array_filter(array_column($rules, 'id'));
        $n = $used === [] ? 10001 : max($used) + 1;
        return $n > 99999 ? 10000 : max(10001, $n);
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

    /**
     * Install (or upgrade) the Nginx connector as a transaction on the
     * server (a background job).
     *
     * @param array<string, mixed> $in method (auto|source), upgrade, nginx_source_sha256
     */
    public function install(array $in, int $userId): string
    {
        $method = ($in['method'] ?? 'auto') === 'source' ? 'source' : 'auto';
        $sha = strtolower(trim((string) ($in['nginx_source_sha256'] ?? '')));
        if ($sha !== '' && !preg_match('/^[0-9a-f]{64}$/', $sha)) {
            throw new ValidationException(['nginx_source_sha256' => 'The SHA-256 is 64 hexadecimal characters.']);
        }
        $params = ['method' => $method, 'upgrade' => ($in['upgrade'] ?? '') === '1', 'retention_days' => $this->retention()];
        if ($sha !== '') {
            $params['nginx_source_sha256'] = $sha;
        }
        $job = (string) (self::arr($this->agent->call('modsec.install', $params, 60))['job_id'] ?? '');
        $this->jobs->track($job, $params['upgrade'] ? 'Upgrade the ModSecurity connector' : 'Install the ModSecurity connector', 'server', (string) $this->server->id(), $userId);
        $this->audit->record('modsec.install', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['job' => $job] + $params, $userId);
        return $job;
    }

    public function installCrs(int $userId): string
    {
        $job = (string) (self::arr($this->agent->call('modsec.crs_install', ['retention_days' => $this->retention()], 60))['job_id'] ?? '');
        $this->jobs->track($job, 'Install the OWASP Core Rule Set', 'server', (string) $this->server->id(), $userId);
        $this->audit->record('modsec.crs_install', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['job' => $job], $userId);
        return $job;
    }

    /** @return list<array<string, mixed>> installation records, newest first */
    public function installs(): array
    {
        return array_values((array) (self::arr($this->agent->call('modsec.installs', [], 30))['installs'] ?? []));
    }

    public function rollback(string $id, ?int $userId): string
    {
        if (!preg_match('/^[0-9TZ-]{17,40}[0-9a-f]*$/', $id)) {
            throw new ValidationException(['id' => 'Unknown installation.']);
        }
        $job = (string) (self::arr($this->agent->call('modsec.rollback', ['id' => $id], 60))['job_id'] ?? '');
        $this->jobs->track($job, 'Roll back ModSecurity installation ' . $id, 'server', (string) $this->server->id(), $userId);
        $this->audit->record('modsec.rollback', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['job' => $job, 'installation' => $id], $userId);
        return $job;
    }

    private function retention(): int
    {
        return $this->settings?->int('modsec_backup_retention_days') ?? 30;
    }

    /** Scheduler: keep the audit log small. @return list<string> */
    public function trim(): array
    {
        $n = (int) (self::arr($this->agent->call('modsec.trim', [], 60))['removed_bytes'] ?? 0);
        return $n > 0 ? ['ModSecurity audit log trimmed by ' . $n . ' bytes.'] : [];
    }
}
