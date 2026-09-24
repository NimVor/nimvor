<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\DeployService;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Services\SettingsService;
use WhaCP\View\View;

final class DeployController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly DeployService $deploy,
        private readonly JobTracker $jobs,
        private readonly SettingsService $settings,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $cfg = $this->deploy->config($site);
        $keyError = null;
        if ($cfg !== null) {
            try {
                $this->deploy->refresh($site);
            } catch (AgentException) {
            }
            if ($cfg['public_key'] === '') {
                try {
                    $cfg['public_key'] = $this->deploy->publicKey($site);
                } catch (AgentException $e) {
                    $keyError = $e->getMessage();
                }
            }
        }
        $base = rtrim($this->settings->string('panel_url'), '/');
        if ($base === '') {
            $scheme = $request->isSecure() ? 'https' : 'http';
            $base = $scheme . '://' . $request->header('host', 'localhost');
        }
        return $this->render('sites/git', [
            'site' => $site,
            'cfg' => $cfg,
            'keyError' => $keyError,
            'history' => $this->deploy->history($site),
            'webhookUrl' => $cfg === null ? '' : $base . '/hooks/deploy/' . $cfg['webhook_token'],
            'webhookSecret' => $cfg === null ? '' : $this->deploy->webhookSecret($cfg),
        ]);
    }

    public function configure(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'git', function (array $site) use ($request): string {
            $this->deploy->configure($site, $request->all());
            return 'Repository settings saved. Add the deploy key to your Git host if the repository is private, then deploy.';
        });
    }

    public function run(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'git', function (array $site) use ($request): array {
            $job = $this->deploy->deploy($site, 'manual', $this->userId($request));
            $this->jobs->track($job, 'Deploy ' . $site['domain'], 'website', (string) $site['id'], $this->userId($request));
            return ['Deployment started.', $job];
        });
    }

    public function rotate(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'git', function (array $site): string {
            $this->deploy->rotateWebhook($site);
            return 'New webhook URL and secret generated. Update them on your Git host.';
        });
    }

    public function remove(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'git', function (array $site): string {
            $this->deploy->remove($site);
            return 'Repository disconnected. The deployed files were left in place.';
        });
    }

    /** Public, signed push webhook (no session, no CSRF). */
    public function webhook(Request $request, string $token): Response
    {
        $headers = [];
        foreach (['x-hub-signature-256', 'x-gitlab-token', 'x-github-event'] as $h) {
            $headers[$h] = $request->header($h);
        }
        [$status, $message] = $this->deploy->webhook($token, $headers, $request->rawBody());
        return Response::json(['success' => $status < 300, 'message' => $message], $status);
    }
}
