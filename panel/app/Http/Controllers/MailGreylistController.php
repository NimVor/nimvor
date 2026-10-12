<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AuditLogger;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/**
 * Greylisting (Email administration): settings, trusted hosts, trusted
 * mail providers and the triplet report. The agent keeps the configuration;
 * every change sends the whole of it.
 */
final class MailGreylistController extends Controller
{
    public const SORTS = ['ip', 'from', 'to', 'deferred', 'accepted', 'created', 'block_expire', 'must_retry', 'record_expire'];
    public const STATUSES = ['deferring' => 'Deferring', 'waiting' => 'Retry accepted', 'passed' => 'Passed', 'expired' => 'Expired'];
    private const BACK = '/mail/admin/greylisting';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Email administration is for server administrators.', 'status' => 403], 403);
    }

    /** @return array<string, mixed> */
    private function state(): array
    {
        return json_decode((string) json_encode($this->agent->call('mail.greylist_get', [], 60)), true);
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
     * Applies a change to the configuration.
     *
     * @param callable(array<string, mixed>): array<string, mixed> $change
     * @param array<string, mixed> $details
     */
    private function change(string $action, callable $change, string $done, array $details = []): Response
    {
        try {
            $params = $change(self::params((array) ($this->state()['config'] ?? [])));
            $this->agent->call('mail.greylist_set', $params, 180);
        } catch (UserFacingException $e) {
            $this->audit->record($action, 'server', 'greylisting', AuditLogger::FAILURE, $details + ['error' => $e->getMessage()]);
            return $this->redirectWithFailure(self::BACK, $e);
        }
        $this->audit->record($action, 'server', 'greylisting', AuditLogger::SUCCESS, $details);
        return $this->redirectWithSuccess(self::BACK, $done);
    }

    public function index(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $state = null;
        $error = null;
        try {
            $state = $this->state();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('mail/admin/greylisting', ['tab' => 'greylisting', 'state' => $state, 'agentError' => $error]);
    }

    public function save(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $in = [
            'enabled' => $request->input('enabled') === '1',
            'deferral' => (int) $request->string('deferral'),
            'resend' => (int) $request->string('resend'),
            'expire' => (int) $request->string('expire'),
            'spf_bypass' => $request->input('spf_bypass') === '1',
            'auto_trust_new' => $request->input('auto_trust_new') === '1',
        ];
        return $this->change('mail.greylist_settings', static fn (array $p): array => array_replace($p, $in), $in['enabled'] ? 'Greylisting settings saved; greylisting is on.' : 'Greylisting settings saved; greylisting is off.', $in);
    }

    public function saveTrusted(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $range = trim($request->string('range'));
        $comment = trim($request->string('comment'));
        $index = $request->input('index');
        if ($range === '') {
            return $this->redirectWithError(self::BACK . '#trusted', 'Enter an IP address or range.');
        }
        return $this->change('mail.greylist_trusted', static function (array $p) use ($range, $comment, $index): array {
            $entry = ['range' => $range, 'comment' => $comment];
            if (is_string($index) && ctype_digit($index) && isset($p['trusted'][(int) $index])) {
                $p['trusted'][(int) $index] = $entry;
            } else {
                $p['trusted'][] = $entry;
            }
            return $p;
        }, 'Trusted host ' . $range . ' saved.', ['range' => $range, 'comment' => $comment]);
    }

    public function deleteTrusted(Request $request, string $n): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        return $this->change('mail.greylist_trusted_delete', static function (array $p) use ($n): array {
            if (!ctype_digit($n) || !isset($p['trusted'][(int) $n])) {
                throw new UserFacingException('That trusted host no longer exists.');
            }
            array_splice($p['trusted'], (int) $n, 1);
            return $p;
        }, 'Trusted host removed.', ['index' => (int) $n]);
    }

    public function saveProviders(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $trust = array_map('strval', (array) $request->input('trust', []));
        $auto = array_map('strval', (array) $request->input('auto_update', []));
        return $this->change('mail.greylist_providers', static function (array $p) use ($trust, $auto): array {
            $providers = [];
            foreach (array_keys((array) $p['providers']) as $id) {
                $providers[$id] = ['trust' => in_array($id, $trust, true), 'auto_update' => in_array($id, $auto, true)];
            }
            $p['providers'] = (object) $providers;
            return $p;
        }, 'Mail providers saved.', ['trusted' => $trust]);
    }

    public function updateProviders(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            $this->agent->call('mail.greylist_update_providers', ['all' => true], 300);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BACK . '#providers', $e);
        }
        $this->audit->record('mail.greylist_update_providers', 'server', 'greylisting');
        return $this->redirectWithSuccess(self::BACK . '#providers', 'Provider address ranges updated from their SPF records.');
    }

    /** @return array{search: string, status: string, sort: string, dir: string, page: int, per: int} */
    public static function query(Request $request): array
    {
        $get = static fn (string $k): string => is_scalar($v = $request->query($k, '')) ? trim((string) $v) : '';
        $status = $get('status');
        $sort = $get('sort');
        $per = (int) $get('per');
        return [
            'search' => mb_substr($get('search'), 0, 200),
            'status' => isset(self::STATUSES[$status]) ? $status : '',
            'sort' => in_array($sort, self::SORTS, true) ? $sort : 'created',
            'dir' => $get('dir') === 'asc' ? 'asc' : 'desc',
            'page' => max(1, min(100000, (int) $get('page'))),
            'per' => in_array($per, [25, 50, 100, 250], true) ? $per : 50,
        ];
    }

    public function report(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $q = self::query($request);
        $report = null;
        $error = null;
        try {
            $report = json_decode((string) json_encode($this->agent->call('mail.greylist_report', [
                'search' => $q['search'], 'status' => $q['status'], 'sort' => $q['sort'], 'desc' => $q['dir'] === 'desc', 'page' => $q['page'], 'per' => $q['per'],
            ], 60)), true);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('mail/admin/greylist_report', ['tab' => 'greylist-report', 'report' => $report, 'q' => $q, 'agentError' => $error]);
    }

    public function cleanup(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $op = $request->string('op');
        $back = '/mail/admin/greylisting/report';
        $triplets = [];
        if ($op === 'delete') {
            foreach ((array) $request->input('sel', []) as $v) {
                $t = json_decode((string) $v, true);
                if (is_array($t) && count($t) === 3 && array_is_list($t)) {
                    $triplets[] = ['ip' => (string) $t[0], 'from' => (string) $t[1], 'to' => (string) $t[2]];
                }
            }
            if ($triplets === []) {
                return $this->redirectWithError($back, 'Select records first.');
            }
        } elseif (!in_array($op, ['purge', 'delete_all'], true)) {
            return $this->redirectWithError($back, 'Unknown operation.');
        }
        try {
            $res = (array) $this->agent->call('mail.greylist_cleanup', ['op' => $op, 'triplets' => array_slice($triplets, 0, 1000)], 60);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        $this->audit->record('mail.greylist_cleanup', 'server', 'greylisting', AuditLogger::SUCCESS, ['op' => $op, 'count' => count($triplets)]);
        return $this->redirectWithSuccess($back, (int) ($res['removed'] ?? 0) . ' record(s) removed.');
    }
}
