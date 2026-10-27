<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Core\Config;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\ServerContext;
use WhaCP\Services\ShellService;
use WhaCP\Services\TerminalService;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/**
 * Web terminal: a website account's shell (its owner, a reseller or an
 * administrator, with the plan's SSH feature and the account's shell
 * access on) and, for administrators, the open sessions and an optional
 * root shell.
 */
final class TerminalController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly TerminalService $terminal,
        private readonly WebsiteRepository $websites,
        private readonly ServerContext $server,
        private readonly ShellService $shell,
        private readonly Config $config,
    ) {
        parent::__construct($view, $session);
    }

    /**
     * The terminal page's Content-Security-Policy: the panel's, with a
     * per-response nonce for the style elements xterm.js creates (it sets
     * colours and cell sizes that way). Scripts stay 'self' only.
     */
    private function page(string $template, array $data): Response
    {
        $nonce = rtrim(strtr(base64_encode(random_bytes(18)), '+/', '-_'), '=');
        return $this->render($template, $data + ['styleNonce' => $nonce])->withHeader(
            'Content-Security-Policy',
            "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-{$nonce}'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"
        );
    }

    private function clientIp(Request $request): string
    {
        return $request->ip((array) $this->config->get('security.trusted_proxies', []));
    }

    /** @return array{0: int, 1: int} */
    private function size(Request $request): array
    {
        return [max(10, min(500, (int) $request->input('cols', 80))), max(5, min(200, (int) $request->input('rows', 24)))];
    }

    private function jsonError(\Throwable $e): Response
    {
        $status = $e instanceof UserFacingException ? $e->status() : 502;
        $status = $status >= 400 && $status < 600 ? $status : 400;
        $body = ['success' => false, 'error' => ['message' => $e->getMessage(), 'unlock' => $status === 428]];
        return Response::json($body, $status);
    }

    /** @return array<string, mixed>|null */
    private function status(): ?array
    {
        try {
            return $this->terminal->status();
        } catch (AgentException) {
            return null;
        }
    }

    public function site(Request $request, string $id): Response
    {
        $site = $this->websites->find($this->server->id(), (int) $id, $this->user($request));
        if ($site === null) {
            return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $mode = null;
        $error = null;
        $status = $this->status();
        try {
            foreach ($this->shell->status()['accounts'] as $a) {
                if ($a['user'] === $site['system_user']) {
                    $mode = (string) $a['effective'];
                }
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        $user = $this->user($request);
        return $this->page('sites/terminal', [
            'site' => $site, 'tab' => 'terminal', 'agentError' => $error, 'status' => $status, 'mode' => $mode,
            'unlocked' => $this->terminal->unlocked(), 'needsCode' => $this->terminal->needsCode((int) $user['id']),
            'openUrl' => '/sites/' . (int) $site['id'] . '/terminal/open', 'idle' => $this->terminal->idleMinutes(),
        ]);
    }

    public function unlock(Request $request): Response
    {
        try {
            $this->terminal->unlock($this->user($request), (string) $request->input('password', ''), trim((string) $request->input('code', '')));
        } catch (UserFacingException $e) {
            return $this->jsonError($e);
        }
        return Response::json(['success' => true, 'data' => ['minutes' => (int) (TerminalService::UNLOCK_SECONDS / 60)]]);
    }

    public function openSite(Request $request, string $id): Response
    {
        $site = $this->websites->find($this->server->id(), (int) $id, $this->user($request));
        if ($site === null) {
            return Response::json(['success' => false, 'error' => ['message' => 'This website does not exist or you do not have access to it.']], 404);
        }
        [$cols, $rows] = $this->size($request);
        try {
            $t = $this->terminal->openForSite($site, $this->user($request), $this->clientIp($request), $cols, $rows);
        } catch (UserFacingException $e) {
            return $this->jsonError($e);
        }
        return Response::json(['success' => true, 'data' => $t]);
    }

    /** Administrators: open sessions, root shell. */
    public function index(Request $request): Response
    {
        $user = $this->user($request);
        if (!Scope::isAll($user)) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Only administrators see every terminal.', 'status' => 403], 403);
        }
        $error = null;
        $status = null;
        try {
            $status = $this->terminal->status();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->page('terminal/index', [
            'status' => $status, 'agentError' => $error, 'isAdmin' => ($user['role_name'] ?? '') === 'admin',
            'unlocked' => $this->terminal->unlocked(), 'needsCode' => $this->terminal->needsCode((int) $user['id']),
            'openUrl' => '/terminal/root/open',
        ]);
    }

    public function openRoot(Request $request): Response
    {
        [$cols, $rows] = $this->size($request);
        try {
            $t = $this->terminal->openRoot($this->user($request), $this->clientIp($request), $cols, $rows);
        } catch (UserFacingException $e) {
            return $this->jsonError($e);
        }
        return Response::json(['success' => true, 'data' => $t]);
    }

    public function close(Request $request, string $id): Response
    {
        try {
            $n = $this->terminal->close($id, $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/terminal', $e);
        }
        return $n > 0 ? $this->redirectWithSuccess('/terminal', 'Terminal session closed.') : $this->redirectWithError('/terminal', 'That session had already ended.');
    }
}
