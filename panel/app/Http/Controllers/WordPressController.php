<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Authorizer;
use NimVor\Security\Permissions as P;
use NimVor\Services\ServerContext;
use NimVor\Services\WordPressService;
use NimVor\Services\WordPressToolsService;
use NimVor\Support\ValidationException;
use NimVor\Hosting\Scope;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

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
        private readonly WordPressToolsService $tools,
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
        $user = $this->user($request);
        $extra = ['protection' => null, 'security' => null, 'restorePoints' => [], 'copyTargets' => [], 'syncTargets' => []];
        if ($error === null) {
            foreach (['protection' => fn () => $this->tools->protection($site), 'security' => fn () => $this->tools->security($site, $install, 'status', [], $this->userId($request)),
                'restorePoints' => fn () => $this->tools->restorePoints($site, $install)] as $k => $fn) {
                try {
                    $extra[$k] = $fn();
                } catch (UserFacingException) {
                }
            }
            $extra['copyTargets'] = $this->tools->copyTargets($site, $user);
            $extra['syncTargets'] = array_values(array_filter($this->wp->installs($user), static fn (array $i): bool => (int) $i['id'] !== (int) $install['id']
                && in_array((int) $i['website_id'], array_map(static fn (array $s): int => (int) $s['id'], $extra['copyTargets']), true)));
        }
        return $this->render('sites/wordpress_show', $extra + [
            'vulns' => WordPressToolsService::storedVulnerabilities($install), 'isAdmin' => Scope::isAll($user),
            'dbId' => $install['db_name'] !== '' ? $this->dbId((string) $install['db_name'], $user) : null,
            'newDbPassword' => $this->session->pullFlash('wp_db_password'),
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

    /** The panel's ID of a database the user may open in the browser. */
    private function dbId(string $name, array $user): ?int
    {
        [$cond, $params] = Scope::condition($user, 'owner_id');
        $id = $this->wp->databaseId($name, $cond, $params);
        return $id;
    }

    /**
     * The toolkit's switches and tools (one form each, "tool" names it).
     */
    public function tool(Request $request, string $id, string $wp): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/wordpress/' . (int) $wp;
        $user = $this->user($request);
        $uid = $this->userId($request);
        $on = $request->string('enabled') === '1';
        try {
            $install = $this->wp->install($site, (int) $wp);
            $tool = $request->string('tool');
            switch ($tool) {
                case 'debug':
                    $items = $request->input('debug', []);
                    $msg = $this->tools->setDebug($site, $install, is_array($items) ? array_map('strval', $items) : [], $uid);
                    break;
                case 'cron':
                    $msg = $this->tools->setCronTakeover($site, $install, $on, $user);
                    break;
                case 'password_protection':
                    $msg = $this->tools->setPasswordProtection($site, $install, $on, $request->string('protect_user'), (string) $request->input('protect_password', ''), $uid);
                    break;
                case 'hotlink':
                    $msg = $this->tools->setHotlink($site, $on, preg_split('/[\s,]+/', $request->string('hotlink_allowed'), -1, PREG_SPLIT_NO_EMPTY) ?: [], $uid);
                    break;
                case 'vulnerability':
                    $msg = $this->tools->setVulnerabilityProtection($site, $install, $on, $user, Scope::isAll($user));
                    break;
                case 'vuln_check':
                    $info = $this->wp->info($site, $install)['info'];
                    $res = $this->tools->checkVulnerabilities($install, $info);
                    $msg = !$res['reachable'] && $res['items'] === [] ? '' : count($res['items']) . ' known vulnerabilit' . (count($res['items']) === 1 ? 'y' : 'ies') . ' found.';
                    if ($msg === '') {
                        return $this->redirectWithError($to . '#security', 'The vulnerability database (wpvulnerability.net) could not be reached from this server; try again later.');
                    }
                    return $this->redirectWithSuccess($to . '#security', $msg);
                case 'admin_password':
                    $msg = $this->tools->adminPassword($site, $install, (int) $request->string('admin_id'), (string) $request->input('admin_password', ''), (string) $request->input('admin_password_confirm', ''), $uid);
                    break;
                case 'db_password':
                    $this->session->flash('wp_db_password', $this->tools->dbPassword($site, $install, $uid));
                    return $this->redirectWithSuccess($to . '#database', 'The database password was changed in the database and in wp-config.php.');
                case 'security':
                    $op = $request->string('op') === 'rollback' ? 'rollback' : 'apply';
                    $m = $request->input('measures', []);
                    $measures = $request->string('recommended') === '1' ? WordPressToolsService::RECOMMENDED : (is_array($m) ? array_map('strval', $m) : []);
                    if ($measures === []) {
                        return $this->redirectWithError($to . '#security', 'Choose at least one measure.');
                    }
                    $this->tools->security($site, $install, $op, $measures, $uid);
                    return $this->redirectWithSuccess($to . '#security', ($op === 'apply' ? 'Security measures applied: ' : 'Rolled back: ') . count($measures) . ' measure(s).');
                case 'clone':
                    $this->flashJob($this->tools->cloneTo($site, $install, ['target_site' => $request->string('target_site'), 'target_path' => $request->string('target_path'),
                        'staging' => $request->string('staging') === '1', 'overwrite' => $request->string('overwrite') === '1'], $user));
                    return $this->redirectWithSuccess($to . '#staging', 'Cloning started; the copy appears in the WordPress list when it is done.');
                case 'sync':
                    $this->flashJob($this->tools->sync($site, $install, ['target' => $request->string('target'), 'files' => $request->string('files') === '1', 'database' => $request->string('database') === '1'], $user));
                    return $this->redirectWithSuccess($to . '#staging', 'Sync started. A restore point of the target is saved first.');
                case 'restore':
                    $this->flashJob($this->tools->restore($site, $install, $request->string('restore_point'), $user));
                    return $this->redirectWithSuccess($to . '#staging', 'Restoring started.');
                default:
                    return $this->redirectWithError($to, 'Unknown tool.');
            }
        } catch (ValidationException $e) {
            return $this->redirectWithError($to, implode(' ', $e->errors()) ?: $e->getMessage());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        return $this->redirectWithSuccess($to, $msg);
    }

    /** WordPress's debug log (the end of it). */
    public function logs(Request $request, string $id, string $wp): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        try {
            $install = $this->wp->install($site, (int) $wp);
            $log = $this->tools->debugLog($site, $install);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $site['id'] . '/wordpress/' . (int) $wp, $e);
        }
        return $this->render('sites/wordpress_logs', ['site' => $site, 'install' => $install, 'log' => $log]);
    }
}
