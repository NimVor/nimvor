<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\RuntimeService;
use NimVor\Services\ServerContext;
use NimVor\Services\WebsiteService;
use NimVor\Services\WorkerService;
use NimVor\View\View;

/**
 * Site → Runtime: the runtime version and app for a Node.js/Python website.
 * The two routes (sites.nodejs / sites.python) share this controller so each
 * is gated by its own feature; the page adapts to the site's app type.
 */
final class SiteRuntimeController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly WebsiteService $websites,
        private readonly WorkerService $workers,
        private readonly RuntimeService $runtimes,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    public function index(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $type = WebsiteService::appType($site);
        $prefix = $type === 'python' ? 'python' : 'node';
        $app = null;
        foreach ($this->workers->forSite($site) as $w) {
            if (($w['name'] ?? '') === 'app') {
                $app = $w;
            }
        }
        $versions = array_values(array_filter($this->runtimes->selectable(), static fn (string $r): bool => str_starts_with($r, $prefix . ':')));
        return $this->render('sites/runtime', [
            'site' => $site, 'tab' => $type === 'python' ? 'python' : 'nodejs', 'appType' => $type,
            'app' => $app, 'versions' => $versions,
        ]);
    }

    public function update(Request $request, string $id): Response
    {
        $type = WebsiteService::appType($this->site($request, $id) ?? []);
        $to = '/sites/' . (int) $id . '/' . ($type === 'python' ? 'python' : 'nodejs');
        return $this->act($request, $id, $type === 'python' ? 'python' : 'nodejs', function (array $site) use ($request): string {
            $runtime = $request->string('app_runtime');
            $this->websites->setRuntime($site, $runtime);
            foreach ($this->workers->forSite($site) as $w) {
                if (($w['name'] ?? '') === 'app') {
                    try {
                        $this->workers->changeRuntime($site, $w, $runtime);
                    } catch (AgentException) {
                    }
                }
            }
            return 'Runtime set to ' . $runtime . '.';
        });
    }
}
