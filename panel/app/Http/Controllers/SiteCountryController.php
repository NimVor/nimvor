<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Countries;
use NimVor\Services\ServerContext;
use NimVor\Services\SiteCountryService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/** A website's country restriction. */
final class SiteCountryController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SiteCountryService $countries,
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
        $cur = ['mode' => 'off', 'codes' => [], 'status' => 403];
        $error = null;
        try {
            $cur = $this->countries->current($site);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/countries', [
            'site' => $site, 'tab' => 'countries', 'agentError' => $error, 'cur' => $cur,
            'modes' => SiteCountryService::MODES, 'all' => Countries::ALL, 'loaded' => $this->countries->databaseLoaded(),
        ]);
    }

    public function save(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/countries';
        $codes = $request->input('countries', []);
        try {
            return $this->redirectWithSuccess($to, $this->countries->save($site, $request->string('mode'), is_array($codes) ? array_map('strval', $codes) : [], $this->userId($request)));
        } catch (ValidationException $e) {
            return $this->redirectWithError($to, implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }
}
