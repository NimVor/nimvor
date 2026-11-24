<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Hosting\Scope;
use NimVor\Http\Middleware\ApiAuthenticate;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\MailAdminService;
use NimVor\Services\MailGreylistService;
use NimVor\Services\MailRateService;
use NimVor\Support\ValidationException;

/**
 * REST API v1: the mail server's administration (server administrators): the queue, the server's settings, the
 * troubleshooter, delivery reports, greylisting and the outgoing sending limits. Every endpoint calls the same
 * services as the web interface (MailAdminService, MailGreylistService, MailRateService), so input is validated
 * before the server is asked, Postfix and Dovecot settings are checked and rolled back by the agent when it refuses
 * them, and every change is audited.
 */
final class MailAdminApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly MailAdminService $admin,
        private readonly MailGreylistService $grey,
        private readonly MailRateService $rates,
    ) {
    }

    private function adminOnly(Request $r): ?Response
    {
        return Scope::isAll($this->user($r)) ? null : ApiAuthenticate::error(403, 'forbidden', 'Mail server administration is for server administrators.');
    }

    /** Runs `$fn` for administrators only, with the shared error mapping. @param callable(): Response $fn */
    private function run(Request $r, callable $fn): Response
    {
        return $this->adminOnly($r) ?? $this->guard($fn);
    }

    // ---- queue --------------------------------------------------------------------------

    public function queue(Request $r): Response
    {
        return $this->run($r, fn (): Response => self::ok($this->admin->queue()));
    }

    public function queueMessage(Request $r, string $id): Response
    {
        return $this->run($r, function () use ($id): Response {
            if (preg_match('/^[0-9A-Za-z]{6,20}$/', $id) !== 1) {
                return self::notFound();
            }
            return self::ok(['id' => $id, 'headers' => $this->admin->message($id)]);
        });
    }

    public function queueAction(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $in = self::body($r);
            $ids = $in['ids'] ?? [];
            if (!is_array($ids)) {
                throw new ValidationException(['ids' => 'A list of queue IDs.']);
            }
            $res = $this->admin->queueAction((string) ($in['action'] ?? ''), array_map('strval', $ids));
            return self::ok(['action' => (string) $in['action'], 'count' => (int) $res['count']]);
        });
    }

    // ---- server, log, troubleshooter, reports ---------------------------------------------------

    public function server(Request $r): Response
    {
        return $this->run($r, fn (): Response => self::ok($this->admin->serverStatus()));
    }

    /** Only the settings that are sent change; unknown names are refused before the server is asked. */
    public function setServer(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $in = self::body($r);
            $known = $this->admin->serverStatus()['values'] ?? [];
            $out = [];
            foreach (['postfix', 'dovecot'] as $group) {
                $given = $in[$group] ?? [];
                if (!is_array($given) || ($given !== [] && array_is_list($given))) {
                    throw new ValidationException([$group => "{$group} must be an object of setting names and values."]);
                }
                foreach ($given as $name => $value) {
                    if (!array_key_exists((string) $name, (array) ($known[$group] ?? []))) {
                        throw new ValidationException([$group => "Unknown {$group} setting {$name}."]);
                    }
                    if (!is_scalar($value)) {
                        throw new ValidationException([$group => "The value of {$name} must be a string or a number."]);
                    }
                    $out[$group][(string) $name] = is_bool($value) ? ($value ? 'yes' : 'no') : (string) $value;
                }
            }
            if ($out === []) {
                throw new ValidationException(['postfix' => 'Send at least one setting to change.']);
            }
            $this->admin->setSettings($out['postfix'] ?? [], $out['dovecot'] ?? []);
            return self::ok($this->admin->serverStatus());
        });
    }

    public function log(Request $r): Response
    {
        return $this->run($r, fn (): Response => self::ok($this->admin->logTail(max(1, min(500, (int) ($r->query('lines') ?? 100))))));
    }

    public function trace(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $address = trim((string) ($r->query('address') ?? ''));
            if ($address === '') {
                throw new ValidationException(['address' => 'Enter an email address.']);
            }
            return self::ok($this->admin->trace($address));
        });
    }

    public function repair(Request $r): Response
    {
        return $this->run($r, fn (): Response => self::ok($this->admin->repair((string) (self::body($r)['domain'] ?? ''))));
    }

    public function report(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $range = (string) ($r->query('range') ?? '24h');
            if (!isset(MailAdminService::RANGES[$range])) {
                throw new ValidationException(['range' => 'Use 1h, 24h, 7d or 30d.']);
            }
            $status = (string) ($r->query('status') ?? '');
            return self::ok(['range' => $range] + $this->admin->report($range, (string) ($r->query('address') ?? ''), $status));
        });
    }

    // ---- greylisting -----------------------------------------------------------------------

    public function greylist(Request $r): Response
    {
        return $this->run($r, fn (): Response => self::ok($this->grey->state()));
    }

    /** Only what is sent changes. */
    public function setGreylist(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $in = self::body($r);
            $set = [];
            foreach (['enabled', 'spf_bypass', 'auto_trust_new'] as $k) {
                if (array_key_exists($k, $in)) {
                    if (!is_bool($in[$k])) {
                        throw new ValidationException([$k => "{$k} must be true or false."]);
                    }
                    $set[$k] = $in[$k];
                }
            }
            $limits = ['deferral' => [1, 1440], 'resend' => [1, 10080], 'expire' => [60, 525600]];
            foreach ($limits as $k => [$lo, $hi]) {
                if (array_key_exists($k, $in)) {
                    if (!is_int($in[$k]) || $in[$k] < $lo || $in[$k] > $hi) {
                        throw new ValidationException([$k => "{$k}: a whole number of minutes from {$lo} to {$hi}."]);
                    }
                    $set[$k] = $in[$k];
                }
            }
            if ($set === []) {
                throw new ValidationException(['enabled' => 'Send at least one setting to change.']);
            }
            $this->grey->saveSettings($set);
            return self::ok($this->grey->state());
        });
    }

    public function addTrusted(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $in = self::body($r);
            $this->grey->saveTrusted((string) ($in['range'] ?? ''), (string) ($in['comment'] ?? ''), null);
            return self::ok($this->grey->state(), 201);
        });
    }

    public function saveTrusted(Request $r, string $n): Response
    {
        return $this->run($r, function () use ($r, $n): Response {
            if (!$this->trustedExists($n)) {
                return self::notFound();
            }
            $in = self::body($r);
            $this->grey->saveTrusted((string) ($in['range'] ?? ''), (string) ($in['comment'] ?? ''), (int) $n);
            return self::ok($this->grey->state());
        });
    }

    public function deleteTrusted(Request $r, string $n): Response
    {
        return $this->run($r, function () use ($n): Response {
            if (!$this->trustedExists($n)) {
                return self::notFound();
            }
            $this->grey->deleteTrusted((int) $n);
            return self::ok($this->grey->state());
        });
    }

    private function trustedExists(string $n): bool
    {
        return ctype_digit($n) && isset(((array) ($this->grey->state()['config'] ?? []))['trusted'][(int) $n]);
    }

    /** Which providers are trusted and kept up to date; a provider not listed is switched off. */
    public function setProviders(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $in = self::body($r);
            $known = array_map('strval', array_keys((array) (((array) $this->grey->state()['config'])['providers'] ?? [])));
            $lists = [];
            foreach (['trust', 'auto_update'] as $k) {
                $v = $in[$k] ?? [];
                if (!is_array($v) || array_diff(array_map('strval', $v), $known) !== []) {
                    throw new ValidationException([$k => "A list of provider ids ({$k}): " . implode(', ', $known) . '.']);
                }
                $lists[$k] = array_map('strval', $v);
            }
            $this->grey->saveProviders($lists['trust'], $lists['auto_update']);
            return self::ok($this->grey->state());
        });
    }

    public function updateProviders(Request $r): Response
    {
        return $this->run($r, function (): Response {
            $this->grey->updateProviders();
            return self::ok($this->grey->state());
        });
    }

    public function greylistReport(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $q = MailGreylistService::normalizeQuery([
                'search' => (string) ($r->query('search') ?? ''), 'status' => (string) ($r->query('status') ?? ''), 'sort' => (string) ($r->query('sort') ?? ''),
                'dir' => (string) ($r->query('dir') ?? ''), 'page' => (int) ($r->query('page') ?? 1), 'per' => (int) ($r->query('per') ?? 50),
            ]);
            return self::ok(['query' => $q] + $this->grey->report($q));
        });
    }

    public function greylistCleanup(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $in = self::body($r);
            $op = (string) ($in['op'] ?? '');
            $triplets = [];
            if ($op === 'delete') {
                $given = $in['triplets'] ?? [];
                if (!is_array($given)) {
                    throw new ValidationException(['triplets' => 'A list of {ip, from, to} records.']);
                }
                foreach ($given as $t) {
                    if (!is_array($t) || !isset($t['ip'], $t['from'], $t['to'])) {
                        throw new ValidationException(['triplets' => 'Each record needs ip, from and to.']);
                    }
                    $triplets[] = ['ip' => (string) $t['ip'], 'from' => (string) $t['from'], 'to' => (string) $t['to']];
                }
            }
            return self::ok(['op' => $op, 'removed' => $this->grey->cleanup($op, $triplets)]);
        });
    }

    // ---- sending limits ---------------------------------------------------------------------

    public function limits(Request $r): Response
    {
        return $this->run($r, fn (): Response => self::ok($this->rates->state()));
    }

    /**
     * Only what is sent changes. In `accounts`, a number sets that website account's limit and `null` removes it
     * (the default `per_hour` applies); accounts not mentioned keep their limit.
     */
    public function setLimits(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $in = self::body($r);
            $cfg = (array) ($this->rates->state()['config'] ?? []);
            $accounts = (array) ($cfg['accounts'] ?? []);
            if (array_key_exists('accounts', $in)) {
                if (!is_array($in['accounts']) || ($in['accounts'] !== [] && array_is_list($in['accounts']))) {
                    throw new ValidationException(['accounts' => 'An object of website account names and limits (null removes a limit).']);
                }
                foreach ($in['accounts'] as $name => $limit) {
                    if ($limit === null) {
                        unset($accounts[(string) $name]);
                    } elseif (is_int($limit)) {
                        $accounts[(string) $name] = $limit;
                    } else {
                        throw new ValidationException(['accounts' => "The limit of {$name} must be a whole number or null."]);
                    }
                }
            }
            foreach (['enabled' => 'is_bool', 'per_hour' => 'is_int'] as $k => $check) {
                if (array_key_exists($k, $in) && !$check($in[$k])) {
                    throw new ValidationException([$k => $k === 'enabled' ? 'enabled must be true or false.' : 'per_hour must be a whole number (0 means no limit).']);
                }
            }
            $new = $this->rates->configure(['enabled' => $in['enabled'] ?? ($cfg['enabled'] ?? false), 'per_hour' => $in['per_hour'] ?? ($cfg['per_hour'] ?? 0), 'accounts' => $accounts]);
            return self::ok(['config' => $new] + $this->rates->state());
        });
    }

    public function resetLimits(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $account = trim((string) (self::body($r)['account'] ?? ''));
            if ($account !== '' && !in_array($account, array_map('strval', (array) ($this->rates->state()['accounts'] ?? [])), true)) {
                return self::notFound();
            }
            $this->rates->resetAccount($account);
            return self::ok(['reset' => $account !== '' ? $account : 'all']);
        });
    }
}
