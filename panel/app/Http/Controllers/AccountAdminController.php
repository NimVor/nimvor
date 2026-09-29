<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AccountAdminService;
use WhaCP\Services\PlanService;
use WhaCP\Services\TerminationService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/**
 * Hosting account administration: limit overrides, plan changes,
 * bandwidth over the limit, bulk termination.
 */
final class AccountAdminController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AccountAdminService $accounts,
        private readonly PlanService $plans,
        private readonly TerminationService $termination,
        private readonly \WhaCP\Services\ResourceLimitService $limits,
        private readonly \WhaCP\Services\BandwidthLimitService $bandwidth,
    ) {
        parent::__construct($view, $session);
    }

    /** Monthly bandwidth blocks follow a changed limit or counter at once (else the scheduler does). */
    private function bandwidthNow(): void
    {
        try {
            $this->bandwidth->reconcile();
        } catch (\Throwable) {
        }
    }

    private function edit(int $id): string
    {
        return '/users/' . $id . '/edit';
    }

    public function saveLimits(Request $request, string $id): Response
    {
        try {
            $account = $this->accounts->account((int) $id, $this->user($request), true);
            $this->accounts->saveOverrides($account, (array) $request->input('limits', []), $this->user($request));
        } catch (ValidationException $e) {
            return $this->redirectWithError($this->edit((int) $id) . '#account-limits', implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->edit((int) $id), $e);
        }
        $this->bandwidthNow();
        return $this->redirectWithSuccess($this->edit((int) $id) . '#account-limits', 'Limits saved. The plan itself is unchanged.');
    }

    public function plan(Request $request, string $id): Response
    {
        try {
            $account = $this->accounts->account((int) $id, $this->user($request), false);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/users', $e);
        }
        $plans = $this->plans->visible($this->user($request));
        $targetId = (int) $request->query('plan', '0');
        $target = null;
        foreach ($plans as $p) {
            if ((int) $p['id'] === $targetId) {
                $target = $p;
            }
        }
        return $this->render('accounts/plan', [
            'account' => $account, 'plans' => $plans, 'target' => $target,
            'current' => $account['plan_id'] !== null ? $this->plans->find($this->user($request), (int) $account['plan_id']) : null,
            'diff' => $target !== null ? $this->accounts->planDiff($account, $target) : [],
        ]);
    }

    public function changePlan(Request $request, string $id): Response
    {
        try {
            $account = $this->accounts->account((int) $id, $this->user($request), false);
            $plan = $this->accounts->changePlan($account, (int) $request->input('plan_id'), $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/users/' . (int) $id . '/plan', $e);
        }
        $this->bandwidthNow();
        // CPU, memory and other resource limits change at once (upgrade or downgrade).
        try {
            $this->limits->reconcile();
        } catch (\WhaCP\Agent\AgentException $e) {
            return $this->redirectWithFailure($this->edit((int) $id), new UserFacingException('The plan was changed, but its resource limits were not applied: ' . $e->getMessage(), '', 'The scheduler applies them again within minutes.'));
        }
        return $this->redirectWithSuccess($this->edit((int) $id), 'The account now has the “' . $plan['name'] . '” plan. Its limits apply from now on; nothing was removed.');
    }

    public function bandwidth(Request $request): Response
    {
        $all = $request->query('show', '') === 'all';
        return $this->render('accounts/bandwidth', ['rows' => $this->accounts->bandwidth(!$all), 'all' => $all, 'month' => gmdate('F Y')]);
    }

    public function resetBandwidth(Request $request, string $id): Response
    {
        $back = $request->string('return_to') === 'bandwidth' ? '/accounts/bandwidth' : $this->edit((int) $id);
        try {
            $account = $this->accounts->account((int) $id, $this->user($request), true);
            $mb = $this->accounts->resetBandwidth($account, $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        $this->bandwidthNow();
        return $this->redirectWithSuccess($back, 'Bandwidth of ' . $account['username'] . ' reset for ' . gmdate('F Y') . ' (' . number_format($mb) . ' MB were counted).');
    }

    /** @return list<int> */
    private static function ids(Request $request): array
    {
        return array_values(array_unique(array_filter(array_map('intval', (array) $request->input('ids', [])), static fn (int $i): bool => $i > 0)));
    }

    public function confirmTerminate(Request $request): Response
    {
        $ids = self::ids($request);
        if ($ids === []) {
            return $this->redirectWithError('/users', 'Select the accounts to terminate first.');
        }
        try {
            $accounts = $this->termination->preview($ids, $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/users', $e);
        }
        return $this->render('accounts/terminate', ['accounts' => $accounts, 'word' => TerminationService::CONFIRM_WORD, 'results' => null]);
    }

    public function terminate(Request $request): Response
    {
        $ids = self::ids($request);
        try {
            $results = $this->termination->terminate($ids, trim($request->string('confirm')), (int) $request->input('count'), $this->user($request));
        } catch (UserFacingException $e) {
            $this->session->flash('error', $e->getMessage());
            try {
                $accounts = $this->termination->preview($ids, $this->user($request));
            } catch (UserFacingException) {
                return $this->redirectWithFailure('/users', $e);
            }
            return $this->render('accounts/terminate', ['accounts' => $accounts, 'word' => TerminationService::CONFIRM_WORD, 'results' => null], 422);
        }
        return $this->render('accounts/terminate', ['accounts' => [], 'word' => TerminationService::CONFIRM_WORD, 'results' => $results]);
    }
}
