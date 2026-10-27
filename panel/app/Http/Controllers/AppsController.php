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
            'targets' => $this->targets($site),
        ]);
    }

    public function install(Request $request, string $id, string $app): Response
    {
        // The application may go on this website or one of its subdomains; the
        // install runs against the chosen target's own document root, and the
        // page redirects to that target so its progress is shown.
        $targetId = $this->chosenTarget($request, (int) $id);
        return $this->act($request, (string) $targetId, 'apps', function (array $site) use ($request, $app, $id, $targetId): array {
            if ($targetId !== (int) $id && (int) ($site['parent_id'] ?? 0) !== (int) $id) {
                throw new \WhaCP\Support\UserFacingException('Choose this website or one of its subdomains.');
            }
            $res = $this->apps->install($site, $app, $request->all(), $this->user($request));
            $this->jobs->track($res['job'], 'Install ' . (AppInstallerService::APPS[$app]['title'] ?? $app), 'website', (string) $site['id'], $this->userId($request));
            if (isset($res['admin_password'])) {
                $this->session->flash('secret', ['title' => 'WordPress administrator', 'user' => $res['admin_user'], 'password' => $res['admin_password']]);
            }
            return [(AppInstallerService::APPS[$app]['title'] ?? $app) . ' is being installed.', $res['job']];
        });
    }

    /** The requested install target, or this website when none/invalid was chosen. */
    private function chosenTarget(Request $request, int $id): int
    {
        $raw = (int) ($request->input('target_id') ?? 0);
        return $raw > 0 ? $raw : $id;
    }

    /**
     * Where an application may be installed from this page: the website
     * itself and each of its subdomains (each installs into its own document
     * root). Addon domains are separate websites with their own Apps page.
     *
     * @param array<string, mixed> $site
     * @return list<array{id: int, domain: string, docroot: string, is_php: bool}>
     */
    private function targets(array $site): array
    {
        $entry = static fn (array $s): array => [
            'id' => (int) $s['id'],
            'domain' => (string) $s['domain'],
            'docroot' => AppInstallerService::relativeDocroot($s),
            'is_php' => $s['kind'] === 'php' && $s['php_version'] !== '',
        ];
        $targets = [$entry($site)];
        foreach ($this->sites->children($this->server->id(), (int) $site['id']) as $child) {
            // Subdomains end with ".<parent domain>"; addon domains do not.
            if (str_ends_with((string) $child['domain'], '.' . $site['domain'])) {
                $targets[] = $entry($child);
            }
        }
        return $targets;
    }
}
