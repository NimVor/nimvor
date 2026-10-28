<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\HealthService;
use NimVor\View\View;

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
