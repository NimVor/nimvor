<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Security\Authorizer;
use WhaCP\Security\Permissions as P;
use WhaCP\Services\ServerContext;
use WhaCP\Services\WordPressService;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/**
 * WordPress toolkit: all installations the user may see (/wordpress) and a
 * website's installations with their overview and management.
 */
final class WordPressController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly WordPressService $wp,
        private readonly Authorizer $authorizer,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    public function index(Request $request): Response
    {
        $filter = (string) $request->query('filter', '');
        $filter = in_array($filter, ['outdated', 'issues'], true) ? $filter : '';
        $user = $this->user($request);
        return $this->render('wordpress/index', [
            'installs' => $this->wp->installs($user, null, $filter),
            'all' => $filter === '' ? null : $this->wp->installs($user),
            'filter' => $filter,
            'canManage' => $this->authorizer->can($user, P::WEBSITES_MANAGE),
        ]);
    }

    public function scanAll(Request $request): Response
    {
        try {
            $res = $this->wp->scan($this->sites->all($this->server->id(), $this->user($request)), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/wordpress', $e);
        }
        return $this->scanned('/wordpress', $res);
    }

    /** @param array{found: int, errors: list<string>, scanned: int} $res */
    private function scanned(string $to, array $res): Response
    {
        $msg = $res['scanned'] . ' website folder(s) scanned: ' . $res['found'] . ' WordPress installation(s) found.';
        if ($res['errors'] !== []) {
            return $this->redirectWithError($to, $msg . ' Not scanned: ' . implode('; ', array_slice($res['errors'], 0, 5)));
        }
        return $this->redirectWithSuccess($to, $msg);
    }

    public function siteList(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $installs = $this->wp->installs($this->user($request), (int) $site['id']);
        if (count($installs) === 1 && $request->query('list') === null) {
            return Response::redirect('/sites/' . (int) $site['id'] . '/wordpress/' . (int) $installs[0]['id']);
        }
        return $this->render('sites/wordpress', ['site' => $site, 'installs' => $installs, 'canManage' => $this->authorizer->can($this->user($request), P::WEBSITES_MANAGE)]);
    }

    public function scanSite(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/wordpress';
        try {
            $sites = array_values(array_filter(
                $this->sites->all($this->server->id(), $this->user($request)),
                static fn (array $s): bool => (int) $s['id'] === (int) $site['id'] || str_starts_with((string) $s['root'], rtrim((string) $site['root'], '/') . '/')
            ));
            $res = $this->wp->scan($sites, $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to . '?list=1', $e);
        }
        return $this->scanned($to, $res);
    }

    public function show(Request $request, string $id, string $wp): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        try {
            $install = $this->wp->install($site, (int) $wp);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $site['id'] . '/wordpress?list=1', $e);
        }
        $data = null;
        $error = null;
        try {
            $data = $this->wp->info($site, $install, $this->session->pullFlash('wp_force') === '1');
            $install = $this->wp->install($site, (int) $wp);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/wordpress_show', [
            'site' => $site, 'install' => $install, 'info' => $data['info'] ?? null, 'checks' => $data['checks'] ?? [], 'agentError' => $error,
            'others' => count($this->wp->installs($this->user($request), (int) $site['id'])) > 1,
            'canManage' => $this->authorizer->can($this->user($request), P::WEBSITES_MANAGE),
            'verify' => $this->session->pullFlash('wp_verify'),
        ]);
    }

    public function check(Request $request, string $id, string $wp): Response
    {
        $this->session->flash('wp_force', '1');
        return Response::redirect('/sites/' . (int) $id . '/wordpress/' . (int) $wp);
    }

    public function manage(Request $request, string $id, string $wp): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/wordpress/' . (int) $wp;
        try {
            $install = $this->wp->install($site, (int) $wp);
            $items = $request->input('items', []);
            $res = $this->wp->manage($site, $install, $request->string('op'), is_array($items) ? array_values($items) : [(string) $items], $request->string('value'), $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        if (isset($res['job'])) {
            $this->flashJob($res['job']);
        }
        if (isset($res['files'])) {
            $this->session->flash('wp_verify', $res['files']);
        }
        return $this->redirectWithSuccess($to, $res['message']);
    }

    public function login(Request $request, string $id, string $wp): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/wordpress/' . (int) $wp;
        if (!$site['https']) {
            return $this->redirectWithError($to, 'One-click sign-in needs HTTPS on the website (the sign-in cookie would travel unencrypted). Issue a certificate under SSL / HTTPS, or sign in at /wp-admin/.');
        }
        try {
            $url = $this->wp->loginUrl($site, $this->wp->install($site, (int) $wp), (int) $request->string('admin_id'), $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        return Response::away($url);
    }
}
