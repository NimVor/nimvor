<?php

declare(strict_types=1);

namespace WhaCP\Http\Middleware;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Security\Csrf;
use WhaCP\Services\AuditLogger;
use WhaCP\View\View;

/**
 * Rejects state-changing requests without a valid CSRF token. The token is
 * accepted from the "_token" form field or the X-CSRF-Token header.
 */
final class VerifyCsrfToken implements Middleware
{
    public function __construct(
        private readonly Csrf $csrf,
        private readonly AuditLogger $audit,
        private readonly View $view,
    ) {
    }

    public function process(Request $request, callable $next): Response
    {
        if ($request->isSafeMethod()) {
            return $next($request);
        }
        $token = $request->header('x-csrf-token');
        if ($token === '') {
            $field = $request->input('_token');
            $token = is_string($field) ? $field : '';
        }
        if (!$this->csrf->validate($token)) {
            $this->audit->record('security.csrf_rejected', 'route', $request->path(), AuditLogger::DENIED);
            $message = 'Your session has expired or the form is out of date. Reload the page and try again.';
            if ($request->wantsJson()) {
                return Response::json(['success' => false, 'error' => ['message' => $message]], 419);
            }
            return Response::html($this->view->render('errors/error', [
                'title' => 'Page expired',
                'message' => $message,
                'status' => 419,
            ], 'layouts/minimal'), 419);
        }
        return $next($request);
    }
}
