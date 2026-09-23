<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\JobTracker;
use WhaCP\View\View;

/**
 * JSON polling endpoint for background job logs.
 */
final class JobsController extends Controller
{
    public function __construct(View $view, SessionInterface $session, private readonly JobTracker $jobs)
    {
        parent::__construct($view, $session);
    }

    public function show(Request $request, string $id): Response
    {
        $job = preg_match('/^[a-f0-9]{16}$/', $id) ? $this->jobs->find($id) : null;
        $user = $this->user($request);
        $scopeAll = ($user['role_scope'] ?? 'all') === 'all';
        if ($job === null || (!$scopeAll && (int) $job['user_id'] !== (int) $user['id'])) {
            return Response::json(['success' => false, 'error' => ['message' => 'Job not found.']], 404);
        }
        try {
            $res = $this->jobs->poll($id, (int) $request->query('offset', 0));
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], 503);
        }
        return Response::json(['success' => true] + $res + ['kind' => $job['kind']]);
    }
}
