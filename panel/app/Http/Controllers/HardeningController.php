<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\HardeningService;
use WhaCP\Services\SecurityService;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/** Security → Compiler Access, SMTP Restrictions, Shell Fork Bomb Protection, Traceroute (administrators). */
final class HardeningController extends Controller
{
    private const PAGES = ['compilers', 'smtp', 'forkbomb', 'traceroute'];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly HardeningService $hardening,
        private readonly SecurityService $security,
    ) {
        parent::__construct($view, $session);
    }

    public function compilers(Request $request): Response
    {
        return $this->page($request, 'compilers');
    }

    public function smtp(Request $request): Response
    {
        return $this->page($request, 'smtp');
    }

    public function forkbomb(Request $request): Response
    {
        return $this->page($request, 'forkbomb');
    }

    public function traceroute(Request $request): Response
    {
        return $this->page($request, 'traceroute');
    }

    public function saveCompilers(Request $request): Response
    {
        return $this->act($request, 'compilers', fn (): string => $this->hardening->setCompilers(
            $request->input('restricted') === '1',
            array_values(array_map('strval', (array) $request->input('users', []))),
            $this->user($request),
        ));
    }

    public function saveSmtp(Request $request): Response
    {
        return $this->act($request, 'smtp', fn (): string => $this->hardening->setSmtp($request->input('enabled') === '1', $this->user($request)));
    }

    public function saveForkbomb(Request $request): Response
    {
        return $this->act($request, 'forkbomb', fn (): string => $this->hardening->setForkBomb($request->input('enabled') === '1', $this->user($request)));
    }

    public function saveTraceroute(Request $request): Response
    {
        return $this->act($request, 'traceroute', fn (): string => $this->hardening->setTraceroute($request->input('enabled') === '1', $this->user($request)));
    }

    private function page(Request $request, string $page): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Only administrators manage server hardening.', 'status' => 403], 403);
        }
        $status = null;
        $error = null;
        try {
            $status = $this->hardening->status();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('security/' . $page, [
            'tab' => $page,
            'pending' => $this->security->pending(),
            'agentError' => $error,
            'status' => $status,
            'accounts' => $page === 'compilers' ? $this->hardening->accounts() : [],
        ]);
    }

    private function act(Request $request, string $page, callable $fn): Response
    {
        $to = '/security/' . (in_array($page, self::PAGES, true) ? $page : 'compilers');
        try {
            return $this->redirectWithSuccess($to, $fn());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }
}
