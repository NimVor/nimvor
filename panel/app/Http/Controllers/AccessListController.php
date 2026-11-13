<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\AccessListService;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/** Allow and deny lists of addresses, browsers and URLs: per website and server-wide. */
final class AccessListController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AccessListService $lists,
        private readonly WebsiteRepository $websites,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null */
    private function site(Request $request, string $id): ?array
    {
        return $this->websites->find($this->server->id(), (int) $id, $this->user($request));
    }

    private function notFound(): Response
    {
        return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
    }

    /** The page of a website, or (null site) of the whole server. @param array<string, mixed>|null $site */
    private function page(?array $site): Response
    {
        $data = ['rules' => [], 'status' => 403];
        $error = null;
        try {
            $data = $this->lists->read($site);
        } catch (AgentException $e) {
            $error = $e;
        }
        $base = $site === null ? '/security/access-lists' : '/sites/' . (int) $site['id'] . '/access-lists';
        $view = $site === null ? 'security/access_lists' : 'sites/access_lists';
        return $this->render($view, [
            'site' => $site, 'tab' => 'accesslists', 'agentError' => $error, 'base' => $base,
            'rules' => $data['rules'], 'status' => $data['status'],
            'max' => $site === null ? AccessListService::MAX_GLOBAL : AccessListService::MAX_SITE,
            'kinds' => AccessListService::KINDS, 'actions' => AccessListService::ACTIONS, 'statuses' => AccessListService::STATUSES,
        ]);
    }

    public function showSite(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        return $site === null ? $this->notFound() : $this->page($site);
    }

    public function showGlobal(Request $request): Response
    {
        return $this->page(null);
    }

    public function addSite(Request $request, string $id): Response
    {
        return $this->actSite($request, $id, fn (array $s): string => $this->lists->add($s, $this->ruleInput($request), $this->userId($request)));
    }

    public function removeSite(Request $request, string $id): Response
    {
        return $this->actSite($request, $id, fn (array $s): string => $this->lists->remove($s, $request->string('kind'), $request->string('value'), $this->userId($request)));
    }

    public function statusSite(Request $request, string $id): Response
    {
        return $this->actSite($request, $id, fn (array $s): string => $this->lists->setStatus($s, (int) $request->input('status', 0), $this->userId($request)));
    }

    public function importSite(Request $request, string $id): Response
    {
        return $this->actSite($request, $id, fn (array $s): string => $this->lists->import($s, $request->string('text'), $request->input('mode') === 'replace', $this->userId($request)));
    }

    public function exportSite(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        return $this->download($site, 'nimvor-' . preg_replace('/[^a-z0-9.-]/', '_', (string) $site['domain']) . '-access-list.txt');
    }

    public function addGlobal(Request $request): Response
    {
        return $this->actGlobal(fn (): string => $this->lists->add(null, $this->ruleInput($request), $this->userId($request)));
    }

    public function removeGlobal(Request $request): Response
    {
        return $this->actGlobal(fn (): string => $this->lists->remove(null, $request->string('kind'), $request->string('value'), $this->userId($request)));
    }

    public function statusGlobal(Request $request): Response
    {
        return $this->actGlobal(fn (): string => $this->lists->setStatus(null, (int) $request->input('status', 0), $this->userId($request)));
    }

    public function importGlobal(Request $request): Response
    {
        return $this->actGlobal(fn (): string => $this->lists->import(null, $request->string('text'), $request->input('mode') === 'replace', $this->userId($request)));
    }

    public function exportGlobal(Request $request): Response
    {
        return $this->download(null, 'nimvor-server-access-list.txt');
    }

    /** @return array<string, mixed> */
    private function ruleInput(Request $request): array
    {
        return ['kind' => $request->string('kind'), 'action' => $request->string('action'), 'value' => $request->string('value'), 'note' => $request->string('note')];
    }

    /** @param array<string, mixed>|null $site */
    private function download(?array $site, string $name): Response
    {
        try {
            $body = $this->lists->export($site);
        } catch (AgentException $e) {
            return $this->redirectWithError($site === null ? '/security/access-lists' : '/sites/' . (int) $site['id'] . '/access-lists', 'The list could not be read from the server.');
        }
        return new Response($body, 200, ['Content-Type' => 'text/plain; charset=utf-8', 'Content-Disposition' => 'attachment; filename="' . $name . '"', 'Cache-Control' => 'no-store']);
    }

    /** @param callable(array<string, mixed>): string $fn */
    private function actSite(Request $request, string $id, callable $fn): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        return $this->run('/sites/' . (int) $site['id'] . '/access-lists', fn (): string => $fn($site));
    }

    /** @param callable(): string $fn */
    private function actGlobal(callable $fn): Response
    {
        return $this->run('/security/access-lists', $fn);
    }

    /** @param callable(): string $fn */
    private function run(string $to, callable $fn): Response
    {
        try {
            return $this->redirectWithSuccess($to, $fn());
        } catch (ValidationException $e) {
            return $this->redirectWithError($to, implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }
}
