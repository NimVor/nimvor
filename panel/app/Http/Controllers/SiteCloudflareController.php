<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\CloudflareSiteService;
use NimVor\Services\ServerContext;
use NimVor\View\View;

/**
 * Site → Cloudflare: a customer connects their own Cloudflare token and
 * manages the free-plan settings for just their website's zone.
 */
final class SiteCloudflareController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly CloudflareSiteService $cf,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    public function index(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        return $this->render('sites/cloudflare', [
            'site' => $site,
            'tab' => 'cloudflare',
            'status' => $this->cf->status($site),
            'sslModes' => CloudflareSiteService::SSL_MODES,
        ]);
    }

    public function connect(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'cloudflare', function (array $site) use ($request): string {
            $this->cf->connect($site, $request->string('token'), $this->userId($request));
            return 'Cloudflare connected for ' . $site['domain'] . '.';
        });
    }

    public function disconnect(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'cloudflare', function (array $site) use ($request): string {
            $this->cf->disconnect($site, $this->userId($request));
            return 'Cloudflare disconnected.';
        });
    }

    public function ssl(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'cloudflare', function (array $site) use ($request): string {
            $this->cf->setSsl($site, $request->string('mode'), $this->userId($request));
            return 'SSL/TLS mode updated.';
        });
    }

    public function alwaysHttps(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'cloudflare', function (array $site) use ($request): string {
            $this->cf->setAlwaysHttps($site, $request->input('on') === '1', $this->userId($request));
            return 'Always Use HTTPS updated.';
        });
    }

    public function proxy(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'cloudflare', function (array $site) use ($request): string {
            $this->cf->setProxied($site, $request->string('record'), $request->input('on') === '1', $this->userId($request));
            return 'Proxy setting updated.';
        });
    }

    public function purge(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'cloudflare', function (array $site) use ($request): string {
            $this->cf->purge($site, $this->userId($request));
            return 'Cloudflare cache purged.';
        });
    }
}
