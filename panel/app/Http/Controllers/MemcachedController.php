<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\JobTracker;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

final class MemcachedController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $status = null;
        $error = null;
        try {
            $status = (array) $this->agent->call('memcached.status');
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('memcached/index', ['status' => $status, 'agentError' => $error]);
    }

    /** @param callable(): (string|array{0: string, 1: string}) $fn */
    private function act(callable $fn): Response
    {
        try {
            $res = $fn();
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/memcached', $e);
        }
        if (is_array($res)) {
            $this->flashJob($res[1]);
            $res = $res[0];
        }
        return $this->redirectWithSuccess('/memcached', $res);
    }

    public function configure(Request $request): Response
    {
        return $this->act(function () use ($request): string {
            $listen = array_values(array_filter(array_map('trim', preg_split('/[\s,]+/', $request->string('listen')) ?: [])));
            $params = [
                'memory_mb' => (int) $request->input('memory_mb', 64),
                'max_conns' => (int) $request->input('max_conns', 1024),
                'listen' => $listen,
                'confirm_edit_package_config' => $request->input('confirm') === '1',
            ];
            $this->agent->call('memcached.configure', $params);
            $this->audit->record('memcached.configure', 'server', 'memcached', AuditLogger::SUCCESS, $params);
            return 'Memcached restarted with the new settings.';
        });
    }

    public function flush(Request $request): Response
    {
        return $this->act(function () use ($request): string {
            if (strtolower(trim($request->string('confirm'))) !== 'flush') {
                throw new UserFacingException('Type "flush" to confirm.');
            }
            $this->agent->call('memcached.flush', ['confirm' => 'flush']);
            $this->audit->record('memcached.flush', 'server', 'memcached', AuditLogger::SUCCESS);
            return 'All Memcached items were removed.';
        });
    }

    public function install(Request $request): Response
    {
        return $this->act(function () use ($request): array {
            $res = (array) $this->agent->call('packages.install', ['name' => 'memcached']);
            $job = (string) $res['job_id'];
            $this->jobs->track($job, 'Install memcached', 'server', 'memcached', $this->userId($request));
            $this->audit->record('packages.install', 'server', 'memcached', AuditLogger::SUCCESS, ['name' => 'memcached']);
            return ['Installing Memcached…', $job];
        });
    }

    public function restart(Request $request): Response
    {
        return $this->act(function (): string {
            $this->agent->call('services.control', ['unit' => 'memcached.service', 'op' => 'restart']);
            $this->audit->record('services.restart', 'service', 'memcached.service', AuditLogger::SUCCESS);
            return 'Memcached restarted.';
        });
    }
}
