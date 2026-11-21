<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\AuditLogger;
use NimVor\Services\JobTracker;
use NimVor\Services\ScreenshotService;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/** The picture of a website's home page on its card: served, refreshed over AJAX, and the browser it needs. */
final class SiteThumbnailController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly ScreenshotService $shots,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null */
    private function site(Request $request, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($request));
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return new Response('', 404);
        }
        try {
            $bytes = $this->shots->image($site);
        } catch (AgentException) {
            $bytes = null;
        }
        if ($bytes === null) {
            return new Response('', 404);
        }
        // The address carries the time the picture was taken, so a new one is a new address.
        return new Response($bytes, 200, ['Content-Type' => 'image/jpeg', 'Cache-Control' => 'private, max-age=86400', 'X-Content-Type-Options' => 'nosniff']);
    }

    /** Takes a new picture; answers JSON for the card's refresh button. */
    public function refresh(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return Response::json(['success' => false, 'error' => 'This website does not exist or you do not have access to it.'], 404);
        }
        try {
            $at = $this->shots->capture($site);
        } catch (UserFacingException $e) {
            $error = $e->getMessage() . ($e->reason() !== '' ? ' ' . $e->reason() : '');
            // What the browser printed tells an administrator why it stopped (a missing library, a refused page).
            if ($e instanceof AgentException && $e->details() !== '' && $this->view->can(\NimVor\Security\Permissions::SERVER_MANAGE)) {
                $error .= "\n\nThe browser said:\n" . mb_substr($e->details(), -800);
            }
            return Response::json(['success' => false, 'error' => $error], $e instanceof AgentException ? 502 : 429);
        }
        return Response::json(['success' => true, 'taken_at' => $at, 'url' => $this->view->url('sites.thumbnail', ['id' => (int) $site['id']]) . '?v=' . rawurlencode((string) strtotime($at))]);
    }

    /** An administrator installs the browser the pictures need. */
    public function install(Request $request): Response
    {
        try {
            $job = $this->shots->install();
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/websites', $e);
        }
        $this->jobs->track($job, 'Install the browser for website pictures', 'server', (string) $this->server->id(), $this->userId($request));
        $this->audit->record('screenshot.install', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, []);
        $this->flashJob($job);
        return $this->redirectWithSuccess('/websites', 'Installing the browser for website pictures (about 300 MB). When it is done, press the refresh icon on a website card.');
    }
}
