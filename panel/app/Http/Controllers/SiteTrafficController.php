<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\SiteTrafficService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/** A website's Traffic protection page: traffic limits. */
final class SiteTrafficController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SiteTrafficService $traffic,
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

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $data = ['limits' => [], 'status' => 429];
        $error = null;
        try {
            $data = $this->traffic->limits($site);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/traffic', [
            'site' => $site, 'tab' => 'traffic', 'sub' => 'limits', 'agentError' => $error,
            'limits' => $data['limits'], 'limitStatus' => $data['status'],
            'max' => SiteTrafficService::MAX_LIMITS, 'statuses' => SiteTrafficService::STATUSES,
        ]);
    }

    public function saveLimit(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (array $site): string => $this->traffic->save($site, [
            'name' => $request->string('name'), 'match' => $request->string('match'), 'path' => $request->string('path'),
            'rate' => $request->input('rate'), 'unit' => $request->string('unit'), 'burst' => $request->input('burst', 0),
            'per_ua' => $request->input('per_ua') === '1',
        ], $this->userId($request)));
    }

    public function removeLimit(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (array $site): string => $this->traffic->remove($site, $request->string('name'), $this->userId($request)));
    }

    public function toggleLimit(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (array $site): string => $this->traffic->toggle($site, $request->string('name'), $request->input('enabled') === '1', $this->userId($request)));
    }

    public function saveStatus(Request $request, string $id): Response
    {
        return $this->act($request, $id, fn (array $site): string => $this->traffic->setStatus($site, (int) $request->input('status', 0), $this->userId($request)));
    }

    /** @param callable(array<string, mixed>): string $fn */
    private function act(Request $request, string $id, callable $fn): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/traffic';
        try {
            return $this->redirectWithSuccess($to, $fn($site));
        } catch (ValidationException $e) {
            return $this->redirectWithError($to, implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }
}
