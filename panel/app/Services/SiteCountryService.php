<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Security\Countries;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * A website's country restriction: refuse visitors from chosen countries, or serve only visitors from them. The
 * country address ranges come from the DB-IP country database the panel already keeps (Brute Force Protection);
 * the agent turns them into an Nginx geo variable, checks it with nginx -t and rolls back when Nginx refuses.
 */
final class SiteCountryService
{
    public const MODES = ['off' => 'No restriction', 'deny' => 'Refuse the selected countries', 'allow_only' => 'Serve only the selected countries'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly GeoIpService $geo,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @return array{mode: string, codes: list<string>, status: int}
     */
    public function current(array $site): array
    {
        $res = (array) $this->agent->call('protect.status', ['domain' => (string) $site['domain']]);
        $c = (array) ($res['countries'] ?? []);
        $mode = (string) ($c['mode'] ?? 'off');
        return [
            'mode' => isset(self::MODES[$mode]) ? $mode : 'off',
            'codes' => array_values(array_map('strval', (array) ($c['codes'] ?? []))),
            'status' => (int) ($res['access_status'] ?? 403),
        ];
    }

    public function databaseLoaded(): bool
    {
        return $this->geo->loaded();
    }

    /**
     * @param array<string, mixed> $site
     * @param list<string> $codes
     */
    public function save(array $site, string $mode, array $codes, int $userId): string
    {
        if (!empty($site['account_suspended'])) {
            throw new UserFacingException('The hosting account is suspended; the restriction cannot be changed.', status: 409);
        }
        if (!isset(self::MODES[$mode])) {
            throw new ValidationException(['mode' => 'Choose a restriction.']);
        }
        $domain = (string) $site['domain'];
        if ($mode === 'off') {
            $this->agent->call('protect.countries', ['domain' => $domain, 'mode' => ''], 120);
            $this->audit->record('site_countries.off', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $domain], $userId);
            return 'The country restriction of ' . $domain . ' is off.';
        }
        $clean = [];
        foreach ($codes as $c) {
            $c = strtoupper(trim((string) $c));
            if (!isset(Countries::ALL[$c])) {
                throw new ValidationException(['codes' => 'Unknown country: ' . $c . '.']);
            }
            $clean[$c] = true;
        }
        $clean = array_keys($clean);
        if ($clean === []) {
            throw new ValidationException(['codes' => 'Select at least one country.']);
        }
        if (!$this->geo->loaded()) {
            throw new UserFacingException('The country database is not loaded yet. An administrator can load it under Security → Brute Force Protection → Countries Management.', status: 409);
        }
        $ranges = $this->geo->rangesByCountry($clean);
        foreach ($ranges as $code => $list) {
            if ($list === []) {
                throw new ValidationException(['codes' => 'The country database has no addresses for ' . (Countries::ALL[$code] ?? $code) . '.']);
            }
        }
        $cur = $this->current($site);
        $this->agent->call('protect.countries', ['domain' => $domain, 'mode' => $mode, 'codes' => $clean, 'ranges' => $ranges, 'status' => $cur['status']], 300);
        $this->audit->record('site_countries.save', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $domain, 'mode' => $mode, 'countries' => $clean], $userId);
        return ($mode === 'deny' ? 'Visitors from ' : 'Only visitors from ') . count($clean) . ' countr' . (count($clean) === 1 ? 'y are ' : 'ies are ') . ($mode === 'deny' ? 'refused' : 'served') . ' on ' . $domain . '.';
    }
}
