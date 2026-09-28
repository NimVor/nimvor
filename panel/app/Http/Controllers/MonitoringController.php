<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\MonitoringService;
use WhaCP\View\View;

final class MonitoringController extends Controller
{
    public const RANGES = ['24h' => 86400, '7d' => 604800, '30d' => 2592000];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly MonitoringService $monitoring,
        private readonly \WhaCP\Agent\AgentClient $agent,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $range = (string) $request->query('range', '24h');
        if (!isset(self::RANGES[$range])) {
            $range = '24h';
        }
        return $this->render('monitoring/index', [
            'range' => $range,
            'series' => $this->monitoring->series(self::RANGES[$range]),
            'open' => $this->monitoring->openAlerts(),
            'recent' => $this->monitoring->recentAlerts(),
        ]);
    }

    /** Live statistics (server administrators: all accounts' processes). */
    public function live(Request $request): Response
    {
        if (!\WhaCP\Hosting\Scope::isAll($this->user($request))) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Live statistics are for server administrators.', 'status' => 403], 403);
        }
        return $this->render('monitoring/live', []);
    }

    public function liveData(Request $request): Response
    {
        if (!\WhaCP\Hosting\Scope::isAll($this->user($request))) {
            return Response::json(['error' => 'Access denied.'], 403);
        }
        try {
            return Response::json((array) json_decode((string) json_encode($this->agent->call('system.live', [], 20)), true));
        } catch (\WhaCP\Agent\AgentException $e) {
            return Response::json(['error' => $e->getMessage()], 502);
        }
    }
}
