<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Hosting\Scope;
use NimVor\Http\Middleware\ApiAuthenticate;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ModSecurityService;
use NimVor\Services\ServerContext;
use NimVor\Support\ValidationException;

/**
 * REST API v1: the ModSecurity web application firewall. Server-wide setup (on/off, Core Rule Set, custom rules,
 * installation, updates, events) is for server administrators; a website's mode (off, detection only, on) can be set by
 * its owner, who cannot change vendors or single rules. Every change goes through ModSecurityService, so nginx -t checks
 * it and the agent keeps the previous configuration when it refuses.
 */
final class ModSecurityApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly ModSecurityService $modsec,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
    }

    private function uid(Request $r): int
    {
        return (int) ($this->user($r)['id'] ?? 0);
    }

    private function adminOnly(Request $r): ?Response
    {
        return Scope::isAll($this->user($r)) ? null : ApiAuthenticate::error(403, 'forbidden', 'ModSecurity is set up by server administrators.');
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    // ---- server-wide ---------------------------------------------------------------------

    /** @return array<string, mixed> */
    private static function statusOut(array $s): array
    {
        $c = (array) ($s['config'] ?? []);
        $crs = (array) ($c['crs'] ?? []);
        $conn = (array) ($s['connector'] ?? []);
        $found = (array) ($s['crs'] ?? []);
        return [
            'enabled' => !empty($c['enabled']),
            'active' => !empty($s['active']),
            'module_loaded' => !empty($s['module_loaded']),
            'detection_only' => !empty($c['detection_only']),
            'connector' => ['state' => (string) ($conn['state'] ?? ''), 'reason' => (string) ($conn['reason'] ?? '')],
            'crs' => [
                'enabled' => !empty($crs['enabled']),
                'installed' => !empty($found['available']),
                'version' => (string) ($found['version'] ?? ''),
                'paranoia' => (int) ($crs['paranoia'] ?? 1),
                'inbound_threshold' => (int) ($crs['inbound_threshold'] ?? 5),
                'outbound_threshold' => (int) ($crs['outbound_threshold'] ?? 4),
            ],
            'custom' => ['enabled' => !empty(($c['custom'] ?? [])['enabled']), 'rules' => (int) ($s['custom_rules'] ?? 0)],
            'packages_available' => !empty($s['packages_available']),
            'audit_log_bytes' => (int) ($s['audit_log_bytes'] ?? 0),
            'vendors' => array_values(array_map(static fn (mixed $v): array => ['id' => (string) ((array) $v)['id'], 'name' => (string) (((array) $v)['name'] ?? ''), 'description' => (string) (((array) $v)['description'] ?? '')], (array) ($s['vendors'] ?? []))),
        ];
    }

    public function show(Request $r): Response
    {
        return $this->adminOnly($r) ?? $this->guard(fn (): Response => self::ok(self::statusOut($this->modsec->status())));
    }

    /** Change only what is sent. */
    public function configure(Request $r): Response
    {
        return $this->adminOnly($r) ?? $this->guard(function () use ($r): Response {
            $in = self::body($r);
            $cur = self::statusOut($this->modsec->status());
            $bool = static function (array $a, string $k, bool $d): bool {
                if (!array_key_exists($k, $a)) {
                    return $d;
                }
                if (!is_bool($a[$k])) {
                    throw new ValidationException([$k => "{$k} must be true or false."]);
                }
                return $a[$k];
            };
            $crs = is_array($in['crs'] ?? null) ? $in['crs'] : [];
            $custom = is_array($in['custom'] ?? null) ? $in['custom'] : [];
            $form = [
                'enabled' => $bool($in, 'enabled', $cur['enabled']) ? '1' : '0',
                'detection_only' => $bool($in, 'detection_only', $cur['detection_only']) ? '1' : '0',
                'crs_enabled' => $bool($crs, 'enabled', $cur['crs']['enabled']) ? '1' : '0',
                'paranoia' => $crs['paranoia'] ?? $cur['crs']['paranoia'],
                'inbound_threshold' => $crs['inbound_threshold'] ?? $cur['crs']['inbound_threshold'],
                'outbound_threshold' => $crs['outbound_threshold'] ?? $cur['crs']['outbound_threshold'],
                'custom_enabled' => $bool($custom, 'enabled', $cur['custom']['enabled']) ? '1' : '0',
            ];
            $this->modsec->configure($form, $this->uid($r));
            return self::ok(self::statusOut($this->modsec->status()));
        });
    }

    // ---- custom rules --------------------------------------------------------------------

    /** @return list<array<string, mixed>> */
    private function rulesOut(): array
    {
        $out = [];
        foreach (ModSecurityService::splitRules($this->modsec->customRules()) as $i => $x) {
            $out[] = ['index' => $i, 'id' => $x['id'], 'phase' => $x['phase'], 'action' => $x['action'], 'message' => $x['msg'], 'comment' => $x['comment'], 'rule' => $x['text'], 'sha256' => hash('sha256', $x['text'])];
        }
        return $out;
    }

    public function rules(Request $r): Response
    {
        return $this->adminOnly($r) ?? $this->guard(fn (): Response => self::ok(['rules' => $this->rulesOut(), 'enabled' => $this->modsec->customEnabled(), 'next_id' => ModSecurityService::nextRuleId(ModSecurityService::splitRules($this->modsec->customRules()))]));
    }

    public function addRule(Request $r): Response
    {
        return $this->adminOnly($r) ?? $this->guard(function () use ($r): Response {
            $in = self::body($r);
            $res = $this->modsec->changeRule('save', null, '', (string) ($in['rule'] ?? ''), (string) ($in['comment'] ?? ''), $this->uid($r));
            $rules = $this->rulesOut();
            return self::ok(['rule' => $rules[count($rules) - 1] ?? null, 'rules' => $res['rules'], 'custom_enabled' => $res['custom_enabled']], 201);
        });
    }

    public function saveRule(Request $r, string $index): Response
    {
        return $this->adminOnly($r) ?? $this->withRule($index, function (int $i) use ($r): Response {
            $in = self::body($r);
            $res = $this->modsec->changeRule('save', $i, (string) ($in['sha256'] ?? ''), (string) ($in['rule'] ?? ''), (string) ($in['comment'] ?? ''), $this->uid($r));
            return self::ok(['rule' => $this->rulesOut()[$i] ?? null, 'rules' => $res['rules'], 'custom_enabled' => $res['custom_enabled']]);
        });
    }

    public function deleteRule(Request $r, string $index): Response
    {
        return $this->adminOnly($r) ?? $this->withRule($index, function (int $i) use ($r): Response {
            $res = $this->modsec->changeRule('delete', $i, (string) ($r->query('sha256') ?? ''), '', '', $this->uid($r));
            return self::ok(['deleted' => true, 'rules' => $res['rules']]);
        });
    }

    /** @param callable(int): Response $fn */
    private function withRule(string $index, callable $fn): Response
    {
        return $this->guard(function () use ($index, $fn): Response {
            if (!ctype_digit($index) || !isset($this->rulesOut()[(int) $index])) {
                return self::notFound();
            }
            return $fn((int) $index);
        });
    }

    // ---- events, installation, updates -----------------------------------------------------

    public function events(Request $r): Response
    {
        return $this->adminOnly($r) ?? $this->guard(fn (): Response => self::ok(['events' => $this->modsec->events(null, self::limit($r))]));
    }

    private static function limit(Request $r): int
    {
        $n = (int) ($r->query('limit') ?? 50);
        return max(1, min(200, $n > 0 ? $n : 50));
    }

    public function update(Request $r): Response
    {
        return $this->adminOnly($r) ?? $this->guard(fn (): Response => self::ok(['job_id' => $this->modsec->update($this->uid($r))], 202));
    }

    public function install(Request $r): Response
    {
        return $this->adminOnly($r) ?? $this->guard(function () use ($r): Response {
            $in = self::body($r);
            $method = (string) ($in['method'] ?? 'auto');
            if (!in_array($method, ['auto', 'source'], true)) {
                throw new ValidationException(['method' => 'Use auto or source.']);
            }
            $job = $this->modsec->install(['method' => $method, 'upgrade' => !empty($in['upgrade']) ? '1' : '', 'nginx_source_sha256' => (string) ($in['nginx_source_sha256'] ?? '')], $this->uid($r));
            return self::ok(['job_id' => $job], 202);
        });
    }

    public function installCrs(Request $r): Response
    {
        return $this->adminOnly($r) ?? $this->guard(fn (): Response => self::ok(['job_id' => $this->modsec->installCrs($this->uid($r))], 202));
    }

    public function installs(Request $r): Response
    {
        return $this->adminOnly($r) ?? $this->guard(fn (): Response => self::ok(['installs' => $this->modsec->installs()]));
    }

    public function rollback(Request $r, string $id): Response
    {
        return $this->adminOnly($r) ?? $this->guard(function () use ($r, $id): Response {
            $known = array_map(static fn (array $i): string => (string) ($i['id'] ?? ''), $this->modsec->installs());
            if (!in_array($id, $known, true)) {
                return self::notFound();
            }
            return self::ok(['job_id' => $this->modsec->rollback($id, $this->uid($r))], 202);
        });
    }

    // ---- a website -----------------------------------------------------------------------

    /**
     * @param array<string, mixed> $site
     * @return array<string, mixed>
     */
    private function siteOut(array $site): array
    {
        $status = $this->modsec->status();
        $set = ModSecurityService::siteSetting($status, $site);
        return [
            'domain' => (string) $site['domain'], 'mode' => $set['mode'], 'vendors_off' => $set['exclude'], 'disabled_rules' => $set['disabled_rules'],
            'available' => !empty($status['module_loaded']) && !empty($status['active']),
            'detection_only_server' => !empty(($status['config'] ?? [])['detection_only']),
        ];
    }

    public function showSite(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : $this->guard(fn (): Response => self::ok($this->siteOut($s)));
    }

    /** The mode for everyone who manages the website; vendors and single rules for administrators only. */
    public function setSite(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $admin = Scope::isAll($this->user($r));
            foreach (['vendors_off', 'disabled_rules'] as $k) {
                if (array_key_exists($k, $in) && !$admin) {
                    throw new ValidationException([$k => "Only server administrators can change {$k}."]);
                }
            }
            $cur = ModSecurityService::siteSetting($this->modsec->status(), $s);
            $mode = array_key_exists('mode', $in) ? (string) $in['mode'] : $cur['mode'];
            $off = null;
            if (array_key_exists('vendors_off', $in)) {
                if (!is_array($in['vendors_off']) || array_diff(array_map('strval', $in['vendors_off']), ['crs', 'custom']) !== []) {
                    throw new ValidationException(['vendors_off' => 'A list of crs and/or custom.']);
                }
                $off = array_map('strval', $in['vendors_off']);
            }
            $disabled = null;
            if (array_key_exists('disabled_rules', $in)) {
                if (!is_array($in['disabled_rules'])) {
                    throw new ValidationException(['disabled_rules' => 'A list of rule IDs.']);
                }
                $disabled = ModSecurityService::parseIds(implode(' ', array_map('strval', $in['disabled_rules'])));
            }
            $this->modsec->setSite($s, $mode, $off, $disabled, $admin, $this->uid($r));
            return self::ok($this->siteOut($s));
        });
    }

    public function siteEvents(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : $this->guard(fn (): Response => self::ok(['events' => $this->modsec->events($s, self::limit($r))]));
    }
}
