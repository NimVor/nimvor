<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\DatabaseRepository;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\DatabaseService;
use WhaCP\Services\DiscoveryService;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class DatabasesController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly DatabaseRepository $repo,
        private readonly DatabaseService $service,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly DiscoveryService $discovery,
        private readonly JobTracker $jobs,
        private readonly \WhaCP\Services\SiteOverview $overview,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $user = $this->user($request);
        $data = $this->common($user, null);
        if (Scope::isAll($user)) {
            try {
                $inv = $this->discovery->inventory(false, $this->userId($request))['data'];
                $data['server'] = (array) ($inv['databases'] ?? []);
                $data['redis'] = (array) ($inv['redis'] ?? []);
            } catch (AgentException) {
            }
        }
        return $this->render('databases/index', $data);
    }

    public function site(Request $request, string $id): Response
    {
        $user = $this->user($request);
        $site = $this->sites->find($this->server->id(), (int) $id, $user);
        if ($site === null) {
            return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        return $this->render('databases/site', $this->common($user, $site) + ['site' => $site, 'tab' => 'databases']);
    }

    /**
     * @param array<string, mixed> $user
     * @param array<string, mixed>|null $site
     * @return array<string, mixed>
     */
    private function common(array $user, ?array $site): array
    {
        $live = $this->service->live();
        $sizes = [];
        foreach ((array) ($live['databases'] ?? []) as $d) {
            $sizes[(string) $d['name']] = $d;
        }
        $managed = $this->repo->databases($this->server->id(), $user, $site === null ? null : (int) $site['id']);
        $managedNames = array_column($this->repo->databases($this->server->id(), ['role_scope' => 'all']), 'name');
        $unmanaged = [];
        if (Scope::isAll($user) && $site === null) {
            foreach ($sizes as $name => $d) {
                if (!in_array($name, $managedNames, true)) {
                    $unmanaged[] = $d;
                }
            }
        }
        return [
            'remoteHosts' => $this->service->remoteHostsAllowed($user),
            'managed' => $managed,
            'dbUsers' => $this->repo->users($this->server->id(), $user),
            'sizes' => $sizes,
            'live' => $live,
            'unmanaged' => $unmanaged,
            'websites' => $this->sites->all($this->server->id(), $user),
            'prefix' => $this->service->prefix($user),
            'secret' => $this->session->pullFlash('secret'),
            'serverIp' => $this->overview->serverIp(),
        ];
    }

    private function back(Request $request): string
    {
        $to = (string) $request->input('return_to', '/databases');
        return preg_match('#^/(databases|sites/[0-9]+/databases)$#', $to) ? $to : '/databases';
    }

    public function store(Request $request): Response
    {
        $input = $request->all();
        try {
            $res = $this->service->create($input, $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors($this->back($request), $e, $input);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        if (isset($res['password'])) {
            $this->session->flash('secret', ['title' => 'Database user created', 'user' => $res['user'], 'password' => $res['password']]);
        }
        return $this->redirectWithSuccess($this->back($request), 'Database created.');
    }

    public function drop(Request $request, string $id): Response
    {
        $db = $this->repo->database($this->server->id(), (int) $id, $this->user($request));
        if ($db === null) {
            return $this->redirectWithError($this->back($request), 'Database not found.');
        }
        try {
            $this->service->drop($db, $request->string('confirm'));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        return $this->redirectWithSuccess($this->back($request), 'Database ' . $db['name'] . ' was deleted.');
    }

    public function dump(Request $request, string $id): Response
    {
        $db = $this->repo->database($this->server->id(), (int) $id, $this->user($request));
        if ($db === null) {
            return $this->redirectWithError($this->back($request), 'Database not found.');
        }
        try {
            $job = $this->service->dump($db);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        $this->jobs->track($job, 'Dump ' . $db['name'], 'database', (string) $db['id'], $this->userId($request));
        $this->flashJob($job);
        return $this->redirectWithSuccess($this->back($request), 'Dump started. The file is written to the server backup directory.');
    }

    public function grant(Request $request, string $id): Response
    {
        $db = $this->repo->database($this->server->id(), (int) $id, $this->user($request));
        $dbUser = $this->repo->user($this->server->id(), (int) $request->input('user_id'), $this->user($request));
        if ($db === null || $dbUser === null) {
            return $this->redirectWithError($this->back($request), 'Database or user not found.');
        }
        $priv = $request->string('privileges');
        try {
            $this->service->grant($db, $dbUser, $priv === 'none' ? null : $priv);
        } catch (ValidationException $e) {
            return $this->redirectWithError($this->back($request), implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        return $this->redirectWithSuccess($this->back($request), 'Privileges updated.');
    }

    /** Import a .sql file of the database's website (uploaded or already there) as a job. */
    public function import(Request $request, string $id): Response
    {
        $user = $this->user($request);
        $db = $this->repo->database($this->server->id(), (int) $id, $user);
        $site = $db !== null && $db['website_id'] !== null ? $this->sites->find($this->server->id(), (int) $db['website_id'], $user) : null;
        if ($db === null || $site === null) {
            return $this->redirectWithError($this->back($request), 'Only a database of one of your websites can be imported into.');
        }
        try {
            $job = $this->service->importFromSite($db, $site, $request->string('path'), $request->input('uploaded') === '1');
        } catch (ValidationException $e) {
            return $this->redirectWithError($this->back($request), implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        $this->jobs->track($job, 'Import into ' . $db['name'], 'database', (string) $db['id'], $this->userId($request));
        $this->flashJob($job);
        return $this->redirectWithSuccess($this->back($request), 'Import into ' . $db['name'] . ' started.');
    }

    /** Give a database user access to one of the account's databases (the database is chosen in the form). */
    public function assign(Request $request, string $id): Response
    {
        $dbUser = $this->repo->user($this->server->id(), (int) $id, $this->user($request));
        $db = $this->repo->database($this->server->id(), (int) $request->input('database_id'), $this->user($request));
        if ($db === null || $dbUser === null) {
            return $this->redirectWithError($this->back($request), 'Database or user not found.');
        }
        try {
            $this->service->grant($db, $dbUser, $request->string('privileges', 'all'));
        } catch (ValidationException $e) {
            return $this->redirectWithError($this->back($request), implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        return $this->redirectWithSuccess($this->back($request), $dbUser['username'] . ' may now use ' . $db['name'] . '.');
    }

    /** Let a database user also connect from another host (remote access). */
    public function addHost(Request $request, string $id): Response
    {
        $dbUser = $this->repo->user($this->server->id(), (int) $id, $this->user($request));
        if ($dbUser === null) {
            return $this->redirectWithError($this->back($request), 'Database user not found.');
        }
        try {
            $res = $this->service->addHost($dbUser, $request->all(), $this->user($request));
        } catch (ValidationException $e) {
            return $this->redirectWithError($this->back($request), implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        $host = DatabaseService::hostFrom($request->all(), $this->user($request));
        $this->session->flash('secret', ['title' => 'Remote access added: ' . $res['user'] . ' from ' . ($host === '%' ? 'any host' : $host), 'user' => $res['user'], 'password' => $res['password']]);
        return Response::redirect($this->back($request));
    }

    public function createUser(Request $request): Response
    {
        $input = $request->all();
        try {
            $res = $this->service->createUser($input, $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors($this->back($request), $e, $input);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        $this->session->flash('secret', ['title' => 'Database user created', 'user' => $res['user'], 'password' => $res['password']]);
        return Response::redirect($this->back($request));
    }

    public function password(Request $request, string $id): Response
    {
        $dbUser = $this->repo->user($this->server->id(), (int) $id, $this->user($request));
        if ($dbUser === null) {
            return $this->redirectWithError($this->back($request), 'User not found.');
        }
        try {
            $pw = $this->service->resetPassword($dbUser, $request->input('store_password') === '1');
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        $this->session->flash('secret', ['title' => 'New password', 'user' => $dbUser['username'], 'password' => $pw]);
        return Response::redirect($this->back($request));
    }

    public function reveal(Request $request, string $id): Response
    {
        $dbUser = $this->repo->user($this->server->id(), (int) $id, $this->user($request));
        $pw = $dbUser === null ? null : $this->service->revealPassword($dbUser);
        if ($pw === null) {
            return $this->redirectWithError($this->back($request), 'No stored password for this user. Reset it to get a new one.');
        }
        $this->session->flash('secret', ['title' => 'Stored password', 'user' => $dbUser['username'], 'password' => $pw]);
        return Response::redirect($this->back($request));
    }

    public function deleteUser(Request $request, string $id): Response
    {
        $dbUser = $this->repo->user($this->server->id(), (int) $id, $this->user($request));
        if ($dbUser === null) {
            return $this->redirectWithError($this->back($request), 'User not found.');
        }
        try {
            $this->service->dropUser($dbUser);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->back($request), $e);
        }
        return $this->redirectWithSuccess($this->back($request), 'Database user removed.');
    }
}
