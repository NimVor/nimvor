<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Base for controllers of one website's tab (cron, workers, Git, backups):
 * the website is always looked up within the user's scope.
 */
abstract class SiteController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        protected readonly WebsiteRepository $sites,
        protected readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null */
    protected function site(Request $request, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($request));
    }

    protected function siteNotFound(): Response
    {
        return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
    }

    /**
     * Run $fn for the website and redirect to $tab with its message.
     * $fn may return [message, jobId] to open the job log.
     *
     * @param callable(array<string, mixed>): (string|array{0: string, 1: string}) $fn
     */
    protected function act(Request $request, string $id, string $tab, callable $fn): Response
    {
        $site = $this->site($request, $id);
        $to = '/sites/' . (int) $id . '/' . $tab;
        if ($site === null) {
            return $this->redirectWithError('/websites', 'Website not found.');
        }
        try {
            $res = $fn($site);
        } catch (ValidationException $e) {
            return $this->backWithErrors($to, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        if (is_array($res)) {
            $this->flashJob($res[1]);
            $res = $res[0];
        }
        return $this->redirectWithSuccess($to, $res);
    }
}
