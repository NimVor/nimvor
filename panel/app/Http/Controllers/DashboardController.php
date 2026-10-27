<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\AuditLogRepository;
use NimVor\Security\Permissions;
use NimVor\Services\DiscoveryService;
use NimVor\Services\SettingsService;
use NimVor\View\View;

final class DashboardController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly DiscoveryService $discovery,
        private readonly SettingsService $settings,
        private readonly AuditLogRepository $audit,
        private readonly \NimVor\Services\MonitoringService $monitoring,
        private readonly \NimVor\Services\NotificationService $notifications,
        private readonly ?\NimVor\Repositories\UserRepository $users = null,
        private readonly ?\NimVor\Repositories\WebsiteRepository $websites = null,
        private readonly ?\NimVor\Services\PlanLimits $plans = null,
        private readonly ?\NimVor\Services\ServerContext $server = null,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        // Hosting customers start from their websites.
        if ($this->view->simpleLayout()) {
            return Response::redirect('/websites');
        }
        // Resellers: their customers, websites and plan, not the server.
        $user = $this->user($request);
        if (($user['role_scope'] ?? 'all') === 'own' && $this->users !== null && $this->websites !== null && $this->plans !== null && $this->server !== null) {
            $sites = $this->websites->all($this->server->id(), $user);
            return $this->render('dashboard/reseller', [
                'customers' => $this->users->manageableBy($user),
                'sites' => $sites,
                'usage' => $this->plans->usage((int) $user['id']),
                'recent' => $this->notifications->list($user, [], 6),
            ]);
        }
        // An administrator's first visit opens the setup wizard (once).
        if ($this->view->can(Permissions::SERVER_MANAGE) && !$this->settings->bool('setup_wizard_seen') && $this->settings->string('server_setup') === '') {
            return Response::redirect('/setup');
        }
        $info = null;
        $summary = null;
        $agentError = null;
        try {
            $info = $this->agent->call('system.info');
            $inventory = $this->discovery->inventory(false, $this->userId($request));
            $summary = DiscoveryService::summary($inventory['data'], $this->settings->int('ssl_warning_days'));
            $summary['discovered_at'] = $inventory['created_at'];
        } catch (AgentException $e) {
            $agentError = $e;
        }
        return $this->render('dashboard/index', [
            'info' => is_array($info) ? $info : null,
            'summary' => $summary,
            'agentError' => $agentError,
            'refreshSeconds' => $this->settings->int('dashboard_refresh_seconds'),
            'setupCard' => $this->view->can(Permissions::SERVER_MANAGE) && !$this->settings->bool('setup_wizard_done'),
            'recentAudit' => $this->view->can(Permissions::AUDIT_VIEW) ? $this->audit->recent(6) : [],
            'alerts' => $this->view->can(Permissions::MONITORING_VIEW) ? $this->monitoring->openAlerts() : [],
            'failures' => $this->view->can(Permissions::NOTIFICATIONS_VIEW)
                ? $this->notifications->list($this->user($request), ['unread' => true, 'severity' => 'error'], 5) : [],
        ]);
    }

    /**
     * Live metrics for the dashboard (polled with fetch()).
     */
    public function live(Request $request): Response
    {
        try {
            $payload = [
                'success' => true,
                'metrics' => $this->agent->call('system.metrics'),
                'history' => $this->agent->call('system.metrics_history'),
                'services' => $this->view->can(Permissions::SERVICES_VIEW) ? $this->agent->call('services.status') : [],
            ];
            return Response::json($payload);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], 503);
        }
    }
}
