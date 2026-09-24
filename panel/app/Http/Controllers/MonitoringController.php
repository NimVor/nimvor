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
}
