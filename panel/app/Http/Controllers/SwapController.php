<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\AuditLogger;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/**
 * Server → Swap: the swap memory in use, and creating, resizing or removing NimVor's swap file. The agent does the work
 * in a fixed order and puts the previous swap back when a step fails; the panel only asks and reports.
 */
final class SwapController extends Controller
{
    private const BACK = '/server/swap';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $status = null;
        $error = null;
        try {
            $status = (array) json_decode((string) json_encode($this->agent->call('swap.status')), true);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('server/swap', ['status' => $status, 'agentError' => $error]);
    }

    /** Create the swap file or change its size. */
    public function set(Request $request): Response
    {
        $mb = $this->sizeMb($request);
        if ($mb === null) {
            return $this->redirectWithError(self::BACK, 'Choose a size for the swap file.');
        }
        try {
            $this->agent->call('swap.set', ['size_mb' => $mb], 600);
        } catch (UserFacingException $e) {
            $this->audit->record('server.swap.set', 'server', (string) $this->server->id(), AuditLogger::FAILURE, ['size_mb' => $mb]);
            return $this->redirectWithFailure(self::BACK, $e);
        }
        $this->audit->record('server.swap.set', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['size_mb' => $mb]);
        return $this->redirectWithSuccess(self::BACK, 'Swap is now ' . format_bytes($mb * 1048576) . ' and starts again after a restart.');
    }

    /** Turn the swap file off and delete it. */
    public function remove(Request $request): Response
    {
        try {
            $this->agent->call('swap.remove', [], 600);
        } catch (UserFacingException $e) {
            $this->audit->record('server.swap.remove', 'server', (string) $this->server->id(), AuditLogger::FAILURE);
            return $this->redirectWithFailure(self::BACK, $e);
        }
        $this->audit->record('server.swap.remove', 'server', (string) $this->server->id(), AuditLogger::SUCCESS);
        return $this->redirectWithSuccess(self::BACK, 'The swap file was turned off and deleted.');
    }

    /** The size asked for: a preset in gigabytes, or a custom number of megabytes. */
    private function sizeMb(Request $request): ?int
    {
        $custom = trim((string) $request->input('custom_mb', ''));
        if ($custom !== '') {
            return ctype_digit($custom) ? (int) $custom : null;
        }
        $gb = trim((string) $request->input('size_gb', ''));
        return ctype_digit($gb) && (int) $gb > 0 ? (int) $gb * 1024 : null;
    }
}
