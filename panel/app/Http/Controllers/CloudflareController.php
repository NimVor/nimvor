<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\CloudflareService;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/**
 * Server → Cloudflare: connect an API token and manage the free-plan CDN/DNS
 * of the account's zones. Administrators only (server.view / server.manage).
 */
final class CloudflareController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly CloudflareService $cf,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $account = $this->cf->account();
        $zones = [];
        $zone = null;
        $error = null;
        if ($account !== null) {
            try {
                $zones = $this->cf->zones();
                $want = (string) $request->query('zone', '');
                if ($want !== '') {
                    $zone = $this->cf->zone($want);
                }
            } catch (UserFacingException $e) {
                $error = $e->getMessage();
            }
        }
        return $this->render('server/cloudflare', [
            'tab' => 'cloudflare', 'account' => $account, 'zones' => $zones, 'zone' => $zone,
            'cfError' => $error, 'sslModes' => CloudflareService::SSL_MODES,
        ]);
    }

    public function connect(Request $request): Response
    {
        try {
            $this->cf->connect((string) $request->input('token'), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithError('/cloudflare', $e->getMessage());
        }
        return $this->redirectWithSuccess('/cloudflare', 'Cloudflare connected.');
    }

    public function disconnect(Request $request): Response
    {
        $this->cf->disconnect($this->userId($request));
        return $this->redirectWithSuccess('/cloudflare', 'Cloudflare disconnected.');
    }

    public function proxy(Request $request): Response
    {
        return $this->zoneAction($request, fn (string $zone) => $this->cf->setProxied($zone, (string) $request->input('record'), $request->input('proxied') === '1', $this->userId($request)), 'DNS record updated.');
    }

    public function ssl(Request $request): Response
    {
        return $this->zoneAction($request, fn (string $zone) => $this->cf->setSslMode($zone, (string) $request->input('mode'), $this->userId($request)), 'SSL mode updated.');
    }

    public function alwaysHttps(Request $request): Response
    {
        return $this->zoneAction($request, fn (string $zone) => $this->cf->setAlwaysHttps($zone, $request->input('on') === '1', $this->userId($request)), 'Always Use HTTPS updated.');
    }

    public function purge(Request $request): Response
    {
        return $this->zoneAction($request, fn (string $zone) => $this->cf->purge($zone, $this->userId($request)), 'Cache purged.');
    }

    /** @param callable(string): void $fn */
    private function zoneAction(Request $request, callable $fn, string $ok): Response
    {
        $zone = (string) $request->input('zone');
        $to = '/cloudflare?zone=' . urlencode($zone);
        try {
            $fn($zone);
        } catch (UserFacingException $e) {
            return $this->redirectWithError($to, $e->getMessage());
        }
        return $this->redirectWithSuccess($to, $ok);
    }
}
