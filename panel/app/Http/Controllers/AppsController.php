<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\AppInstallerService;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\View\View;

final class AppsController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly AppInstallerService $apps,
        private readonly JobTracker $jobs,
        private readonly \WhaCP\Services\AppCatalogService $catalog,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        try {
            $this->apps->refresh($site);
        } catch (AgentException) {
        }
        return $this->render('sites/apps', [
            'site' => $this->site($request, $id),
            'installs' => $this->apps->history($site),
            'secret' => $this->session->pullFlash('secret'),
            'docroot' => AppInstallerService::relativeDocroot($site),
            'email' => (string) ($this->user($request)['email'] ?? ''),
            'catalog' => $this->catalog->forCustomer(),
            'installerEnabled' => $this->catalog->installerEnabled(),
        ]);
    }

    public function install(Request $request, string $id, string $app): Response
    {
        return $this->act($request, $id, 'apps', function (array $site) use ($request, $app): array {
            $res = $this->apps->install($site, $app, $request->all(), $this->user($request));
            $this->jobs->track($res['job'], 'Install ' . (AppInstallerService::APPS[$app]['title'] ?? $app), 'website', (string) $site['id'], $this->userId($request));
            if (isset($res['admin_password'])) {
                $this->session->flash('secret', ['title' => 'WordPress administrator', 'user' => $res['admin_user'], 'password' => $res['admin_password']]);
            }
            return [(AppInstallerService::APPS[$app]['title'] ?? $app) . ' is being installed.', $res['job']];
        });
    }
}
