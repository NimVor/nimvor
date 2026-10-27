<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\HealthService;
use WhaCP\View\View;

/** Server → Health: every check in one place, each linking to its fix. */
final class ServerHealthController extends Controller
{
    public function __construct(View $view, SessionInterface $session, private readonly HealthService $health)
    {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $result = $this->health->run();
        return $this->render('server/health', ['result' => $result, 'summary' => HealthService::summary($result), 'checkedAt' => gmdate('Y-m-d H:i')]);
    }
}
