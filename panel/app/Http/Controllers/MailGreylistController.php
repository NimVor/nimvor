<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\MailGreylistService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Greylisting (Email administration): settings, trusted hosts, trusted
 * mail providers and the triplet report. The agent keeps the configuration;
 * every change sends the whole of it.
 */
final class MailGreylistController extends Controller
{
    public const SORTS = MailGreylistService::SORTS;
    public const STATUSES = MailGreylistService::STATUSES;
    private const BACK = '/mail/admin/greylisting';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly MailGreylistService $grey,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Email administration is for server administrators.', 'status' => 403], 403);
    }

    /** @param callable(): void $apply */
    private function apply(callable $apply, string $done, string $to = self::BACK): Response
    {
        try {
            $apply();
        } catch (ValidationException $e) {
            return $this->redirectWithError($to, implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        return $this->redirectWithSuccess($to, $done);
    }

    public function index(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $state = null;
        $error = null;
        try {
            $state = $this->grey->state();
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
        return $this->apply(fn () => $this->grey->saveSettings($in), $in['enabled'] ? 'Greylisting settings saved; greylisting is on.' : 'Greylisting settings saved; greylisting is off.', self::BACK . '#greylist-settings');
    }

    public function saveTrusted(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $range = trim($request->string('range'));
        $index = $request->input('index');
        return $this->apply(fn () => $this->grey->saveTrusted($range, $request->string('comment'), is_string($index) && ctype_digit($index) ? (int) $index : null), 'Trusted host ' . $range . ' saved.', self::BACK . '#trusted');
    }

    public function deleteTrusted(Request $request, string $n): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        return $this->apply(function () use ($n): void {
            if (!ctype_digit($n)) {
                throw new UserFacingException('That trusted host no longer exists.');
            }
            $this->grey->deleteTrusted((int) $n);
        }, 'Trusted host removed.', self::BACK . '#trusted');
    }

    public function saveProviders(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $trust = array_map('strval', (array) $request->input('trust', []));
        $auto = array_map('strval', (array) $request->input('auto_update', []));
        return $this->apply(fn () => $this->grey->saveProviders($trust, $auto), 'Mail providers saved.', self::BACK . '#providers');
    }

    public function updateProviders(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        return $this->apply(fn () => $this->grey->updateProviders(), 'Provider address ranges updated from their SPF records.', self::BACK . '#providers');
    }

    /** @return array{search: string, status: string, sort: string, dir: string, page: int, per: int} */
    public static function query(Request $request): array
    {
        $get = static fn (string $k): string => is_scalar($v = $request->query($k, '')) ? trim((string) $v) : '';
        return MailGreylistService::normalizeQuery(['search' => $get('search'), 'status' => $get('status'), 'sort' => $get('sort'), 'dir' => $get('dir'), 'page' => (int) $get('page'), 'per' => (int) $get('per')]);
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
            $report = $this->grey->report($q);
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
            $removed = $this->grey->cleanup($op, $triplets);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess($back, $removed . ' record(s) removed.');
    }
}
