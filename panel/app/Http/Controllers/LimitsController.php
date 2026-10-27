<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\ResourceLimitService;
use WhaCP\View\View;

/**
 * Resource limits (cgroups) of hosting accounts: what each account's plan
 * asks for, what the server applies and the current use.
 */
final class LimitsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly ResourceLimitService $limits,
        private readonly \WhaCP\Database\Connection $db,
        private readonly \WhaCP\Services\DiskQuotaService $quotas,
        private readonly \WhaCP\Services\JobTracker $jobs,
        private readonly \WhaCP\Services\ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Resource limits are managed by server administrators.', 'status' => 403], 403);
        }
        $status = null;
        $error = null;
        try {
            $status = $this->limits->status();
        } catch (AgentException $e) {
            $error = $e;
        }
        $desired = $this->limits->desired();
        $names = [];
        foreach ($this->db->fetchAll('SELECT u.id, u.username, p.name AS plan FROM users u LEFT JOIN plans p ON p.id = u.plan_id') as $u) {
            $names['acct' . $u['id']] = ['user_id' => (int) $u['id'], 'username' => (string) $u['username'], 'plan' => (string) ($u['plan'] ?? '')];
        }
        $quota = null;
        try {
            $quota = $this->quotas->status();
        } catch (AgentException) {
            // An older agent, or not reachable (shown above).
        }
        $quotaJob = (string) $this->db->scalar("SELECT job_id FROM agent_jobs WHERE server_id = ? AND kind = 'Turn on disk quotas' ORDER BY created_at DESC LIMIT 1", [$this->server->id()]);
        return $this->render('limits/index', ['status' => $status, 'desired' => $desired, 'names' => $names, 'agentError' => $error,
            'quota' => $quota, 'quotaRows' => $this->quotas->rows(), 'quotaJob' => $quotaJob]);
    }

    /** Turn disk quotas on for the websites' file system (a background job). */
    public function enableQuota(Request $request): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/limits', 'Disk quotas are managed by server administrators.');
        }
        try {
            $job = $this->quotas->enable($this->user($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/limits#disk-quotas', $e);
        }
        $this->jobs->track($job, 'Turn on disk quotas', 'server', (string) $this->server->id(), (int) $this->user($request)['id']);
        return $this->redirectWithSuccess('/limits#disk-quotas', 'Turning disk quotas on. Follow the log below; the plans\' disk space is enforced once they are on.');
    }

    public function apply(Request $request): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/limits', 'Resource limits are managed by server administrators.');
        }
        try {
            $lines = $this->limits->reconcile();
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/limits', $e);
        }
        return $this->redirectWithSuccess('/limits', $lines === [] ? 'Everything already matches the plans.' : implode(' · ', $lines));
    }
}
