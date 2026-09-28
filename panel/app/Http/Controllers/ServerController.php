<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\DiscoveryService;
use WhaCP\View\View;

final class ServerController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly DiscoveryService $discovery,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        try {
            $info = $this->agent->call('system.info');
            $metrics = $this->agent->call('system.metrics');
            $firewall = $this->agent->call('discovery.firewall');
        } catch (AgentException $e) {
            return $this->render('discovery/unavailable', ['title' => 'Server', 'error' => $e]);
        }
        $settings = null;
        $timezones = [];
        try {
            $settings = (array) $this->agent->call('server.settings');
            $timezones = (array) $this->agent->call('server.timezones');
        } catch (AgentException) {
        }
        return $this->render('server/index', [
            'info' => (array) $info,
            'metrics' => (array) $metrics,
            'firewall' => (array) $firewall,
            'settings' => $settings,
            'timezones' => $timezones,
        ]);
    }

    public function services(Request $request): Response
    {
        try {
            $services = $this->agent->call('services.status');
        } catch (AgentException $e) {
            return $this->render('discovery/unavailable', ['title' => 'Services', 'error' => $e]);
        }
        $whacp = null;
        try {
            $whacp = (array) json_decode((string) json_encode($this->agent->call('whacp.services')), true);
        } catch (AgentException) {
            // An older agent: the table below still lists its units.
        }
        return $this->render('server/services', ['services' => (array) $services, 'whacp' => $whacp]);
    }

    public function systemUsers(Request $request): Response
    {
        try {
            $inventory = $this->discovery->inventory(false, $this->userId($request));
        } catch (AgentException $e) {
            return $this->render('discovery/unavailable', ['title' => 'System accounts', 'error' => $e]);
        }
        $showSystem = $request->query('all') === '1';
        $users = array_values(array_filter(
            (array) ($inventory['data']['users'] ?? []),
            static fn (array $u): bool => $showSystem || empty($u['system']) || (int) $u['uid'] === 0,
        ));
        return $this->render('server/system_users', [
            'users' => $users,
            'showSystem' => $showSystem,
            'discoveredAt' => $inventory['created_at'],
        ]);
    }
}
