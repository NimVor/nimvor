<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\AccessListService;
use NimVor\Services\ServerContext;
use NimVor\Services\SiteCountryService;
use NimVor\Services\SiteTrafficService;
use NimVor\Support\ValidationException;

/**
 * REST API v1: a website's web protection: request-rate limits, the allow/deny list (addresses, browsers,
 * URLs) and the country restriction. Every endpoint calls the same services as the web interface: each
 * rule is validated, the website's web server configuration is written and checked before it is reloaded,
 * and a refused change is undone. A suspended account's protection cannot be changed (409).
 */
final class ProtectionApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly SiteTrafficService $traffic,
        private readonly AccessListService $lists,
        private readonly SiteCountryService $countries,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    private function uid(Request $r): int
    {
        return (int) ($this->user($r)['id'] ?? 0);
    }

    // ---- request-rate limits ----------------------------------------------------------

    /**
     * @param array<string, mixed> $l
     * @return array<string, mixed>
     */
    private static function limitOut(array $l): array
    {
        return [
            'name' => (string) ($l['name'] ?? ''), 'match' => (string) ($l['match'] ?? 'prefix'), 'path' => (string) ($l['path'] ?? '/'),
            'rate' => (int) ($l['rate'] ?? 0), 'unit' => (string) ($l['unit'] ?? 's'), 'burst' => (int) ($l['burst'] ?? 0),
            'per_ua' => !empty($l['per_ua']), 'enabled' => !array_key_exists('enabled', $l) || !empty($l['enabled']),
        ];
    }

    /** @param array<string, mixed> $s */
    private function limitsOut(array $s): array
    {
        $d = $this->traffic->limits($s);
        return ['status' => (int) $d['status'], 'limits' => array_map([self::class, 'limitOut'], $d['limits'])];
    }

    public function listLimits(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(fn (): Response => self::ok($this->limitsOut($s)));
    }

    public function saveLimit(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $rule = SiteTrafficService::rule($in);
            $existed = in_array($rule['name'], array_column($this->traffic->limits($s)['limits'], 'name'), true);
            $this->traffic->save($s, $in, $this->uid($r));
            foreach ($this->limitsOut($s)['limits'] as $l) {
                if ($l['name'] === $rule['name']) {
                    return self::ok($l, $existed ? 200 : 201);
                }
            }
            return self::ok(self::limitOut($rule), $existed ? 200 : 201);
        });
    }

    public function toggleLimit(Request $r, string $id, string $name): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $name): Response {
            $in = self::body($r);
            if (!array_key_exists('enabled', $in) || filter_var($in['enabled'], FILTER_VALIDATE_BOOL, FILTER_NULL_ON_FAILURE) === null) {
                throw new ValidationException(['enabled' => 'Send true or false (to change the limit itself, send it again with POST).']);
            }
            $this->traffic->toggle($s, $name, filter_var($in['enabled'], FILTER_VALIDATE_BOOL), $this->uid($r));
            foreach ($this->limitsOut($s)['limits'] as $l) {
                if ($l['name'] === $name) {
                    return self::ok($l);
                }
            }
            return self::notFound();
        });
    }

    public function deleteLimit(Request $r, string $id, string $name): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $name): Response {
            $this->traffic->remove($s, $name, $this->uid($r));
            return self::ok(['deleted' => $name]);
        });
    }

    public function setLimitStatus(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $this->traffic->setStatus($s, (int) (self::body($r)['status'] ?? 0), $this->uid($r));
            return self::ok($this->limitsOut($s));
        });
    }

    // ---- the allow / deny list --------------------------------------------------------

    /** @param array<string, mixed> $s */
    private function listOut(array $s): array
    {
        $d = $this->lists->read($s);
        return ['status' => (int) $d['status'], 'max' => AccessListService::MAX_SITE, 'rules' => $d['rules']];
    }

    public function showAccessList(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(fn (): Response => self::ok($this->listOut($s)));
    }

    public function addAccessRule(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $rule = ['kind' => (string) ($in['kind'] ?? ''), 'action' => (string) ($in['action'] ?? ''), 'value' => (string) ($in['value'] ?? ''), 'note' => (string) ($in['note'] ?? '')];
            $this->lists->add($s, $rule, $this->uid($r));
            return self::ok($this->listOut($s), 201);
        });
    }

    public function deleteAccessRule(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $kind = (string) ($r->query('kind') ?? self::body($r)['kind'] ?? '');
            $value = (string) ($r->query('value') ?? self::body($r)['value'] ?? '');
            if ($kind === '' || $value === '') {
                throw new ValidationException(['kind' => 'Name the entry by its kind and value, as the list shows them.']);
            }
            $this->lists->remove($s, $kind, $value, $this->uid($r));
            return self::ok($this->listOut($s));
        });
    }

    public function setAccessStatus(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $this->lists->setStatus($s, (int) (self::body($r)['status'] ?? 0), $this->uid($r));
            return self::ok($this->listOut($s));
        });
    }

    public function importAccessList(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $mode = (string) ($in['mode'] ?? 'merge');
            if (!in_array($mode, ['merge', 'replace'], true)) {
                throw new ValidationException(['mode' => 'Use merge or replace.']);
            }
            $this->lists->import($s, (string) ($in['text'] ?? ''), $mode === 'replace', $this->uid($r));
            return self::ok($this->listOut($s));
        });
    }

    // ---- countries --------------------------------------------------------------------

    /** @param array<string, mixed> $s */
    private function countriesOut(array $s): array
    {
        $c = $this->countries->current($s);
        return ['mode' => $c['mode'], 'codes' => $c['codes'], 'refusal_status' => $c['status'], 'database_loaded' => $this->countries->databaseLoaded()];
    }

    public function showCountries(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(fn (): Response => self::ok($this->countriesOut($s)));
    }

    public function setCountries(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $codes = $in['codes'] ?? [];
            if (!is_array($codes)) {
                throw new ValidationException(['codes' => 'Send a list of two-letter country codes.']);
            }
            $this->countries->save($s, (string) ($in['mode'] ?? ''), array_map('strval', $codes), $this->uid($r));
            return self::ok($this->countriesOut($s));
        });
    }
}
