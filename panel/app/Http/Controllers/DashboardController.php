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
        private readonly ?\NimVor\Services\SitesHealthService $siteHealth = null,
        private readonly ?\NimVor\Services\DashboardWidgetsService $widgets = null,
        private readonly ?\NimVor\Dashboard\WidgetRegistry $registry = null,
        private readonly ?\NimVor\Dashboard\DashboardLayout $layout = null,
        private readonly ?\NimVor\Services\WebsiteMonitor $monitor = null,
        private readonly ?\NimVor\Dashboard\WidgetCache $cache = null,
        private readonly ?\NimVor\Services\AuditLogger $auditor = null,
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
        $tiles = $this->tiles($request);
        return $this->render('dashboard/index', [
            'tiles' => $tiles,
            'customised' => $this->layout !== null && $this->layout->isCustomised($this->userId($request)),
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
     * The widgets to draw, in the user's order: the ones that are cheap are drawn
     * with the page, the others are loaded after it.
     *
     * @return list<array{def: \NimVor\Dashboard\Widget, visible: bool, size: string, refresh: int, refreshChoice: ?int, html: ?string}>
     */
    private function tiles(Request $request): array
    {
        if ($this->registry === null || $this->layout === null || $this->widgets === null) {
            return [];
        }
        $user = $this->user($request);
        $out = [];
        foreach ($this->layout->resolve($this->userId($request), $this->registry->visibleTo(fn (string $p): bool => $this->view->can($p))) as $t) {
            $def = $t['widget'];
            $html = null;
            if ($t['visible'] && $def->inline) {
                $html = $this->frame($def, $this->widgets->widget($def->id, $user));
            }
            $out[] = ['def' => $def, 'visible' => $t['visible'], 'size' => $t['size'], 'refresh' => $t['refresh'], 'refreshChoice' => $t['refreshChoice'], 'html' => $html];
        }
        return $out;
    }

    /** @param array{state: string, data: array<string, mixed>, at: int} $result */
    private function frame(\NimVor\Dashboard\Widget $def, array $result): string
    {
        return $this->view->partial('dashboard/widget_card', ['def' => $def, 'result' => $result]);
    }

    /** One widget's HTML (the page loads and refreshes widgets with this). */
    public function widget(Request $request, string $id): Response
    {
        $def = $this->registry?->find($id);
        if ($def === null || $this->widgets === null) {
            return Response::json(['success' => false, 'error' => 'No such widget.'], 404);
        }
        // The data behind a widget is only for those who may see the widget at all.
        if (!$this->view->can($def->permission)) {
            return Response::json(['success' => false, 'error' => 'You may not see this widget.'], 403);
        }
        $result = $this->widgets->widget($def->id, $this->user($request));
        return Response::json(['success' => true, 'state' => $result['state'], 'html' => $this->frame($def, $result), 'at' => $result['at']]);
    }

    /**
     * The details behind a live tile (CPU, memory, disk, load), as the HTML of a small popover: the processor and each
     * core, the busiest processes, where memory and disk space go. Read-only; the same data as the monitoring page.
     */
    public function popover(Request $request, string $key): Response
    {
        if (!in_array($key, ['cpu', 'mem', 'memory', 'disk', 'load', 'server'], true) || $this->widgets === null) {
            return Response::json(['success' => false, 'error' => 'No such tile.'], 404);
        }
        // The server card (host, system, agent and the server tools) is the administrator's, shown from the top bar.
        if ($key === 'server') {
            if (!\NimVor\Hosting\Scope::isAll($this->user($request))) {
                return Response::json(['success' => false, 'error' => 'Only administrators see the server details.'], 403);
            }
            try {
                return Response::json(['success' => true, 'html' => $this->view->partial('dashboard/widgets/server_info', ['d' => $this->widgets->serverInfo()])]);
            } catch (AgentException $e) {
                return Response::json(['success' => false, 'error' => $e->getMessage()], 503);
            }
        }
        try {
            $live = (array) json_decode((string) json_encode($this->agent->call('system.live', [], 20)), true);
            $disk = $key === 'disk' ? $this->widgets->disk() : null;
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->getMessage()], 503);
        }
        return Response::json(['success' => true, 'html' => $this->view->partial('dashboard/popover', ['key' => $key, 'live' => $live, 'disk' => $disk])]);
    }

    /** Saves which widgets the user shows, in what order and at what size. */
    public function saveLayout(Request $request): Response
    {
        if ($this->registry === null || $this->layout === null) {
            return Response::redirect('/');
        }
        $order = $request->input('order', []);
        $widgets = $request->input('widgets', []);
        $this->layout->save(
            $this->userId($request),
            is_array($order) ? array_values(array_map('strval', $order)) : [],
            is_array($widgets) ? $widgets : [],
            $this->registry->visibleTo(fn (string $p): bool => $this->view->can($p)),
        );
        return $this->redirectWithSuccess('/', 'Your dashboard layout is saved.');
    }

    /** Drag and drop / resize on the dashboard: saves the new order and sizes of the shown widgets. */
    public function arrangeLayout(Request $request): Response
    {
        if ($this->registry === null || $this->layout === null) {
            return Response::json(['success' => false, 'error' => 'The dashboard layout is not available.'], 404);
        }
        $order = $request->input('order', []);
        $sizes = $request->input('sizes', []);
        $this->layout->arrange(
            $this->userId($request),
            is_array($order) ? array_values(array_map('strval', $order)) : [],
            is_array($sizes) ? $sizes : [],
            $this->registry->visibleTo(fn (string $p): bool => $this->view->can($p)),
        );
        return Response::json(['success' => true]);
    }

    /** Ranges the history charts offer: seconds and the most points drawn. */
    private const RANGES = ['1h' => [3600, 60], '6h' => [21600, 72], '24h' => [86400, 96], '7d' => [604800, 112]];

    /**
     * The monitoring history for the dashboard's charts: only what the monitor
     * measured (read from the panel's own records, no agent call). A value that
     * was not measured in a period is null.
     */
    public function series(Request $request): Response
    {
        $range = (string) $request->query('range', '24h');
        if (!isset(self::RANGES[$range])) {
            $range = '24h';
        }
        [$seconds, $points] = self::RANGES[$range];
        $now = time();
        $rows = $this->monitoring->series($seconds, $points, $now);
        $first = $this->monitoring->firstSampleAt();
        return Response::json(['success' => true, 'range' => $range, 'from' => $now - $seconds, 'to' => $now, 'since' => $first, 'points' => $rows]);
    }

    /** Checks the websites now (at most once a minute) and refreshes the widget. */
    public function checkWebsites(Request $request): Response
    {
        if ($this->monitor === null) {
            return Response::redirect('/');
        }
        try {
            $n = $this->monitor->check(false);
        } catch (AgentException) {
            return $this->redirectWithError('/', 'The websites could not be checked: the server\'s agent did not answer. Try again in a moment.');
        }
        if ($n === null) {
            return $this->redirectWithSuccess('/', 'The websites were checked a moment ago. Their results are on the dashboard.');
        }
        $this->cache?->forget('website_health');
        return $this->redirectWithSuccess('/', 'Checked ' . $n . ' website' . ($n === 1 ? '' : 's') . '.');
    }

    public function alertAck(Request $request, string $id): Response
    {
        return $this->alertAction($request, $id, 'ack');
    }

    public function alertResolve(Request $request, string $id): Response
    {
        return $this->alertAction($request, $id, 'resolve');
    }

    /** Acknowledges or closes an open alert. */
    public function alertAction(Request $request, string $id, string $do): Response
    {
        $alert = $this->monitoring->alert((int) $id);
        if ($alert === null || $alert['resolved_at'] !== null) {
            return $this->redirectWithError('/', 'That alert is not open any more.');
        }
        $uid = $this->userId($request);
        $done = $do === 'resolve' ? $this->monitoring->resolve((int) $id, $uid) : $this->monitoring->acknowledge((int) $id, $uid);
        $this->cache?->forget('alert_center');
        if ($done) {
            $this->auditor?->record('alert.' . ($do === 'resolve' ? 'resolve' : 'acknowledge'), 'alert', (string) $alert['rule'] . ($alert['subject'] !== '' ? ':' . $alert['subject'] : ''), \NimVor\Services\AuditLogger::SUCCESS, ['message' => (string) $alert['message']], $uid);
        }
        return $this->redirectWithSuccess('/', $do === 'resolve' ? 'The alert is closed. If the problem is still there it comes back as an acknowledged alert.' : 'The alert is acknowledged.');
    }

    /** Back to the default dashboard. */
    public function resetLayout(Request $request): Response
    {
        $this->layout?->reset($this->userId($request));
        return $this->redirectWithSuccess('/', 'The dashboard is back to its default layout.');
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
