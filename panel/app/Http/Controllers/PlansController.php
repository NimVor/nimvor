<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\PlanService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

final class PlansController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly PlanService $plans,
        private readonly \NimVor\Services\FeatureService $features,
        private readonly \NimVor\Services\FfmpegService $ffmpeg,
        private readonly \NimVor\Services\ResourceLimitService $limits,
        private readonly \NimVor\Services\BandwidthLimitService $bandwidth,
        private readonly \NimVor\Services\DiskQuotaService $quotas,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $edit = (int) $request->query('edit', 0);
        return $this->render('plans/index', [
            'plans' => $this->plans->visible($this->user($request)),
            'edit' => $edit > 0 ? $this->plans->find($this->user($request), $edit) : null,
            'featureLists' => \NimVor\Hosting\Scope::isAll($this->user($request)) ? $this->features->lists() : [],
        ]);
    }

    public function store(Request $request): Response
    {
        $id = (int) $request->input('plan_id', 0);
        try {
            $this->plans->save($this->user($request), $id > 0 ? $id : null, $request->all());
        } catch (ValidationException $e) {
            return $this->backWithErrors('/plans' . ($id > 0 ? '?edit=' . $id : ''), $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/plans', $e);
        }
        // FFmpeg access follows the plan at once (else the scheduler does).
        try {
            $this->ffmpeg->reconcile();
        } catch (\NimVor\Agent\AgentException) {
        }
        // Monthly bandwidth blocks and disk quotas follow the new limits at once.
        try {
            $this->bandwidth->reconcile();
        } catch (\Throwable) {
        }
        try {
            $this->quotas->reconcile();
        } catch (\Throwable) {
        }
        // Resource limits apply to the plan's accounts at once.
        try {
            $changed = $this->limits->reconcile();
        } catch (\NimVor\Agent\AgentException $e) {
            return $this->redirectWithFailure('/plans', new UserFacingException('Plan saved, but its resource limits were not applied: ' . $e->getMessage(), '', 'They are applied again by the scheduler within minutes; check the agent.'));
        }
        return $this->redirectWithSuccess('/plans', 'Plan saved. Limits apply to new websites, databases, cron jobs and workers' . ($changed !== [] ? '; resource limits applied to ' . count($changed) . ' account(s)' : '') . '.');
    }

    public function destroy(Request $request, string $id): Response
    {
        try {
            $this->plans->delete($this->user($request), (int) $id);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/plans', $e);
        }
        return $this->redirectWithSuccess('/plans', 'Plan deleted.');
    }
}
