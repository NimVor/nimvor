<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\DiscoveryService;
use WhaCP\Services\ImportService;
use WhaCP\Services\JobTracker;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class ImportController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly ImportService $imports,
        private readonly DiscoveryService $discovery,
        private readonly JobTracker $jobs,
        private readonly UserRepository $users,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null */
    private function found(string $id, int $userId): ?array
    {
        return DiscoveryService::website($this->discovery->inventory(false, $userId)['data'], $id);
    }

    public function preview(Request $request, string $id): Response
    {
        try {
            $found = $this->found($id, $this->userId($request));
        } catch (AgentException $e) {
            return $this->render('discovery/unavailable', ['title' => 'Import', 'error' => $e]);
        }
        if ($found === null) {
            return $this->redirectWithError('/websites', 'That website is no longer in the discovery snapshot. Run discovery again.');
        }
        $input = ['system_user' => $request->query('system_user', ''), 'php_version' => $request->query('php_version', '')];
        $plan = $preview = $planErrors = null;
        $error = null;
        try {
            $plan = $this->imports->plan($found, $input);
            $preview = $this->imports->preview($plan);
        } catch (ValidationException $e) {
            $planErrors = $e->errors();
        } catch (UserFacingException $e) {
            $error = $e;
        }
        return $this->render('websites/import', [
            'found' => $found, 'plan' => $plan, 'preview' => $preview, 'planErrors' => $planErrors, 'agentError' => $error,
            'input' => $input, 'owners' => Scope::isAll($this->user($request)) ? $this->users->all() : [],
        ]);
    }

    public function run(Request $request, string $id): Response
    {
        $back = '/websites/' . rawurlencode($id) . '/import';
        if ($request->input('confirm') !== '1') {
            return $this->redirectWithError($back, 'Confirm that you reviewed the plan.');
        }
        try {
            $found = $this->found($id, $this->userId($request)) ?? throw new UserFacingException('That website is no longer in the discovery snapshot.');
            $plan = $this->imports->plan($found, $request->all());
            $owner = filter_var($request->input('owner_id'), FILTER_VALIDATE_INT) ?: null;
            [$siteId, $job] = $this->imports->run($plan, $this->user($request), Scope::isAll($this->user($request)) ? $owner : $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors($back, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        $this->jobs->track($job, 'Import ' . $found['domain'], 'website', (string) $siteId, $this->userId($request));
        $this->flashJob($job);
        return $this->redirectWithSuccess('/sites/' . $siteId, 'Import started. The old website keeps serving visitors until the switch at the end.');
    }
}
