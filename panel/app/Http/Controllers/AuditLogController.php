<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\AuditLogRepository;
use NimVor\View\View;

final class AuditLogController extends Controller
{
    private const PER_PAGE = 50;

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AuditLogRepository $audit,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $filters = [
            'action' => mb_substr(trim((string) $request->query('action', '')), 0, 100),
            'result' => in_array($request->query('result'), ['success', 'failure', 'denied'], true) ? (string) $request->query('result') : '',
            'user' => mb_substr(trim((string) $request->query('user', '')), 0, 100),
            'q' => mb_substr(trim((string) $request->query('q', '')), 0, 100),
        ];
        $page = max(1, (int) $request->query('page', 1));
        $result = $this->audit->search($filters, $page, self::PER_PAGE);
        return $this->render('audit/index', [
            'rows' => $result['rows'],
            'total' => $result['total'],
            'page' => $page,
            'pages' => max(1, (int) ceil($result['total'] / self::PER_PAGE)),
            'filters' => $filters,
        ]);
    }
}
