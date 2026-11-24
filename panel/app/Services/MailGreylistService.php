<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * Greylisting (email administration): settings, trusted hosts, trusted mail providers and the triplet report. The
 * agent keeps the configuration and every change sends the whole of it; each change is audited here (a refused one
 * too), so the web interface and the API leave the same record.
 */
final class MailGreylistService
{
    public const SORTS = ['ip', 'from', 'to', 'deferred', 'accepted', 'created', 'block_expire', 'must_retry', 'record_expire'];
    public const STATUSES = ['deferring' => 'Deferring', 'waiting' => 'Retry accepted', 'passed' => 'Passed', 'expired' => 'Expired'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array<string, mixed> config, providers and statistics from the agent */
    public function state(): array
    {
        return (array) json_decode((string) json_encode($this->agent->call('mail.greylist_get', [], 60)), true);
    }

    /**
     * The request for mail.greylist_set from the current configuration.
     *
     * @param array<string, mixed> $cfg
     * @return array<string, mixed>
     */
    private static function params(array $cfg): array
    {
        $providers = [];
        foreach ((array) ($cfg['providers'] ?? []) as $id => $p) {
            $providers[(string) $id] = ['trust' => !empty($p['trust']), 'auto_update' => !empty($p['auto_update'])];
        }
        return [
            'enabled' => !empty($cfg['enabled']),
            'deferral' => (int) ($cfg['deferral'] ?? 5),
            'resend' => (int) ($cfg['resend'] ?? 240),
            'expire' => (int) ($cfg['expire'] ?? 4320),
            'spf_bypass' => !empty($cfg['spf_bypass']),
            'auto_trust_new' => !empty($cfg['auto_trust_new']),
            'trusted' => array_values(array_map(static fn (array $t): array => ['range' => (string) $t['range'], 'comment' => (string) ($t['comment'] ?? '')], (array) ($cfg['trusted'] ?? []))),
            'providers' => (object) $providers,
        ];
    }

    /**
     * Applies a change to the whole configuration.
     *
     * @param callable(array<string, mixed>): array<string, mixed> $change
     * @param array<string, mixed> $details
     */
    private function change(string $action, callable $change, array $details = []): void
    {
        try {
            $params = $change(self::params((array) ($this->state()['config'] ?? [])));
            $this->agent->call('mail.greylist_set', $params, 180);
        } catch (UserFacingException $e) {
            $this->audit->record($action, 'server', 'greylisting', AuditLogger::FAILURE, $details + ['error' => $e->getMessage()]);
            throw $e;
        }
        $this->audit->record($action, 'server', 'greylisting', AuditLogger::SUCCESS, $details);
    }

    /**
     * @param array{enabled?: bool, deferral?: int, resend?: int, expire?: int, spf_bypass?: bool, auto_trust_new?: bool} $in only these keys change
     */
    public function saveSettings(array $in): void
    {
        $this->change('mail.greylist_settings', static fn (array $p): array => array_replace($p, $in), $in);
    }

    /** Add a trusted host or range, or replace the one at `$index`. */
    public function saveTrusted(string $range, string $comment, ?int $index = null): void
    {
        $range = trim($range);
        if ($range === '') {
            throw new ValidationException(['range' => 'Enter an IP address or range.']);
        }
        $comment = trim($comment);
        $this->change('mail.greylist_trusted', static function (array $p) use ($range, $comment, $index): array {
            $entry = ['range' => $range, 'comment' => $comment];
            if ($index !== null && isset($p['trusted'][$index])) {
                $p['trusted'][$index] = $entry;
            } else {
                $p['trusted'][] = $entry;
            }
            return $p;
        }, ['range' => $range, 'comment' => $comment]);
    }

    public function deleteTrusted(int $index): void
    {
        $this->change('mail.greylist_trusted_delete', static function (array $p) use ($index): array {
            if (!isset($p['trusted'][$index])) {
                throw new UserFacingException('That trusted host no longer exists.');
            }
            array_splice($p['trusted'], $index, 1);
            return $p;
        }, ['index' => $index]);
    }

    /**
     * Which mail providers are trusted and kept up to date; a provider that is not listed is switched off.
     *
     * @param list<string> $trust
     * @param list<string> $auto
     */
    public function saveProviders(array $trust, array $auto): void
    {
        $this->change('mail.greylist_providers', static function (array $p) use ($trust, $auto): array {
            $providers = [];
            foreach (array_keys((array) $p['providers']) as $id) {
                $providers[$id] = ['trust' => in_array($id, $trust, true), 'auto_update' => in_array($id, $auto, true)];
            }
            $p['providers'] = (object) $providers;
            return $p;
        }, ['trusted' => $trust]);
    }

    public function updateProviders(): void
    {
        $this->agent->call('mail.greylist_update_providers', ['all' => true], 300);
        $this->audit->record('mail.greylist_update_providers', 'server', 'greylisting');
    }

    /**
     * @param array{search?: string, status?: string, sort?: string, dir?: string, page?: int, per?: int} $q
     * @return array{search: string, status: string, sort: string, dir: string, page: int, per: int}
     */
    public static function normalizeQuery(array $q): array
    {
        $status = (string) ($q['status'] ?? '');
        $sort = (string) ($q['sort'] ?? '');
        $per = (int) ($q['per'] ?? 50);
        return [
            'search' => mb_substr(trim((string) ($q['search'] ?? '')), 0, 200),
            'status' => isset(self::STATUSES[$status]) ? $status : '',
            'sort' => in_array($sort, self::SORTS, true) ? $sort : 'created',
            'dir' => ($q['dir'] ?? '') === 'asc' ? 'asc' : 'desc',
            'page' => max(1, min(100000, (int) ($q['page'] ?? 1))),
            'per' => in_array($per, [25, 50, 100, 250], true) ? $per : 50,
        ];
    }

    /**
     * @param array{search: string, status: string, sort: string, dir: string, page: int, per: int} $q
     * @return array<string, mixed>
     */
    public function report(array $q): array
    {
        return (array) json_decode((string) json_encode($this->agent->call('mail.greylist_report', [
            'search' => $q['search'], 'status' => $q['status'], 'sort' => $q['sort'], 'desc' => $q['dir'] === 'desc', 'page' => $q['page'], 'per' => $q['per'],
        ], 60)), true);
    }

    /**
     * Remove records: `delete` the given triplets (at most 1000), `purge` the expired, or `delete_all`.
     *
     * @param list<array{ip: string, from: string, to: string}> $triplets
     * @return int how many records were removed
     */
    public function cleanup(string $op, array $triplets = []): int
    {
        if (!in_array($op, ['delete', 'purge', 'delete_all'], true)) {
            throw new ValidationException(['op' => 'Use delete, purge or delete_all.']);
        }
        if ($op === 'delete' && $triplets === []) {
            throw new ValidationException(['triplets' => 'Select records first.']);
        }
        $res = (array) $this->agent->call('mail.greylist_cleanup', ['op' => $op, 'triplets' => array_slice($triplets, 0, 1000)], 60);
        $this->audit->record('mail.greylist_cleanup', 'server', 'greylisting', AuditLogger::SUCCESS, ['op' => $op, 'count' => count($triplets)]);
        return (int) ($res['removed'] ?? 0);
    }
}
