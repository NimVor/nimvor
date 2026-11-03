<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\PostgresRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Permissions as P;
use NimVor\Services\JobTracker;
use NimVor\Services\PostgresService;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * PostgreSQL: databases, users and privileges for customers (their own) and administrators (all), and the
 * server itself (install, adopt, service) for those who may manage it.
 */
final class PostgresController extends Controller
{
    private const BASE = '/postgres';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly PostgresService $pg,
        private readonly PostgresRepository $repo,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $user = $this->user($request);
        $status = null;
        $error = null;
        $live = null;
        try {
            $status = $this->pg->status();
            if ($status['installed'] && $status['running'] && $status['managed']) {
                $live = $this->pg->live();
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        $pg = $this->pg->pgServerId();
        $databases = $this->repo->databases($pg, $user);
        $roles = $this->repo->roles($pg, $user);
        $sizes = [];
        foreach ((array) ($live['databases'] ?? []) as $d) {
            $sizes[(string) $d['name']] = (array) $d;
        }
        foreach ($databases as &$d) {
            $d['size_bytes'] = (int) ($sizes[(string) $d['name']]['size_bytes'] ?? 0);
            $d['connections'] = (int) ($sizes[(string) $d['name']]['connections'] ?? 0);
            $d['live'] = isset($sizes[(string) $d['name']]);
        }
        unset($d);
        // Administrators also see what exists on the server without a record here (read-only).
        $unmanaged = [];
        if (Scope::isAll($user) && $live !== null) {
            $known = array_column($this->repo->databases($pg, ['role_scope' => 'all']), 'name');
            foreach ((array) ($live['databases'] ?? []) as $d) {
                $d = (array) $d;
                if (empty($d['system']) && !in_array((string) $d['name'], $known, true)) {
                    $unmanaged[] = $d;
                }
            }
        }
        $siteList = $this->sites->all($this->server->id(), $user);
        return $this->render('postgres/index', [
            'status' => $status, 'agentError' => $error, 'live' => $live, 'databases' => $databases, 'roles' => $roles,
            'unmanaged' => $unmanaged, 'prefix' => $this->pg->prefix($user), 'sites' => $siteList,
            'canServer' => $this->view->can(P::POSTGRES_MANAGE), 'isAdmin' => Scope::isAll($user),
        ]);
    }

    /** @return array<string, mixed>|null the database record, only when the signed-in account may use it */
    private function databaseOf(Request $request, mixed $id): ?array
    {
        return $this->repo->database($this->pg->pgServerId(), (int) $id, $this->user($request));
    }

    private function browseUrl(array $db, string $schema, string $table, array $extra = []): string
    {
        return self::BASE . '/browse?' . http_build_query(['db' => (int) $db['id'], 'schema' => $schema, 'table' => $table] + $extra);
    }

    /** The filters of a browse, from the query string (one column filter and a text search). */
    private function criteria(Request $request): array
    {
        $q = ['search' => mb_substr(trim((string) $request->query('q', '')), 0, 200)];
        $col = (string) $request->query('fc', '');
        $op = (string) $request->query('fo', '');
        if ($col !== '' && in_array($op, ['eq', 'ne', 'contains', 'null', 'notnull'], true)) {
            $q['filters'] = [['column' => $col, 'op' => $op, 'value' => (string) $request->query('fv', '')]];
        }
        $sort = (string) $request->query('sort', '');
        if ($sort !== '') {
            $q['sort'] = $sort;
            $q['desc'] = (string) $request->query('dir', '') === 'desc';
        }
        return $q;
    }

    public function browser(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->query('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $schema = (string) $request->query('schema', '');
        $table = (string) $request->query('table', '');
        $per = in_array((int) $request->query('per', 50), [25, 50, 100, 250], true) ? (int) $request->query('per', 50) : 50;
        $page = max(1, (int) $request->query('page', 1));
        $data = ['db' => $db, 'schema' => $schema, 'table' => $table, 'per' => $per, 'pageNo' => $page, 'q' => $request->query('q', ''),
            'fc' => (string) $request->query('fc', ''), 'fo' => (string) $request->query('fo', ''), 'fv' => (string) $request->query('fv', ''),
            'sort' => (string) $request->query('sort', ''), 'dir' => (string) $request->query('dir', 'asc'), 'result' => null, 'structure' => null, 'tables' => ['schemas' => [], 'tables' => []]];
        try {
            $data['tables'] = $this->pg->tables($db);
            if ($table !== '') {
                $data['result'] = $this->pg->browse($db, $schema, $table, $this->criteria($request) + ['offset' => ($page - 1) * $per, 'limit' => $per]);
                $data['structure'] = $this->pg->structure($db, $schema, $table);
            }
        } catch (UserFacingException $e) {
            $this->session->flash('error', $e->getMessage() . ($e->reason() !== '' ? ' ' . $e->reason() : ''));
            return Response::redirect($table !== '' ? $this->browseUrl($db, '', '') : self::BASE);
        }
        $data['canWrite'] = $this->view->can(P::DATABASES_MANAGE);
        return $this->render('postgres/browser', $data);
    }

    // ---- SQL editor -------------------------------------------------------------------------------

    public function sql(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->query('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $schema = [];
        $error = null;
        try {
            $schema = $this->pg->schemaMap($db);
        } catch (UserFacingException $e) {
            $error = $e;
        }
        return $this->render('postgres/sql', [
            'db' => $db, 'schema' => $schema, 'schemaError' => $error, 'history' => $this->pg->history($db, $this->userId($request)),
            'canWrite' => $this->view->can(P::DATABASES_MANAGE),
        ]);
    }

    /** JSON: run a statement; the answer carries the result (or the SQL error) and the refreshed history. */
    public function sqlRun(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return Response::json(['success' => false, 'error' => ['message' => 'Database not found.']], 404);
        }
        $mode = (string) $request->input('mode') === 'write' ? 'write' : 'read';
        if ($mode === 'write' && !$this->view->can(P::DATABASES_MANAGE)) {
            return Response::json(['success' => false, 'error' => ['message' => 'You may read this database, not change it.']], 403);
        }
        $sql = (string) $request->input('query');
        if (trim($sql) === '' || strlen($sql) > 100 * 1024) {
            return Response::json(['success' => false, 'error' => ['message' => 'Write a statement of at most 100 KB.']], 422);
        }
        $explain = (string) $request->input('explain');
        $qid = (string) $request->input('query_id');
        $opts = [
            'mode' => $mode, 'limit' => max(1, min(1000, (int) $request->input('limit', 500))), 'timeout' => max(1, min(60, (int) $request->input('timeout', 15))),
            'script' => filter_var($request->input('script'), FILTER_VALIDATE_BOOL), 'explain' => in_array($explain, ['plan', 'analyze'], true) ? $explain : '',
            'query_id' => preg_match('/^[a-f0-9]{16}$/', $qid) === 1 ? $qid : '',
        ];
        try {
            $res = $this->pg->runQuery($db, $sql, $opts, $this->userId($request));
        } catch (UserFacingException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], $e->status() >= 400 && $e->status() < 600 ? $e->status() : 502);
        }
        return Response::json(['success' => true, 'data' => ['result' => $res, 'history' => $this->pg->history($db, $this->userId($request))]]);
    }

    public function sqlCancel(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        $qid = (string) $request->input('query_id');
        if ($db === null || preg_match('/^[a-f0-9]{16}$/', $qid) !== 1) {
            return Response::json(['success' => false, 'error' => ['message' => 'Nothing to cancel.']], 404);
        }
        try {
            return Response::json(['success' => true, 'data' => ['cancelled' => $this->pg->cancelQuery($db, $qid)]]);
        } catch (UserFacingException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], 502);
        }
    }

    /** The whole filtered table as CSV (up to 50,000 rows), NULL as an empty cell. */
    public function export(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->query('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $schema = (string) $request->query('schema', '');
        $table = (string) $request->query('table', '');
        $fh = fopen('php://temp', 'w+');
        try {
            $offset = 0;
            $header = false;
            while ($offset < 50000) {
                $page = $this->pg->browse($db, $schema, $table, $this->criteria($request) + ['offset' => $offset, 'limit' => 500]);
                if (!$header) {
                    fputcsv($fh, array_map(static fn ($c): string => (string) $c['name'], (array) $page['columns']), ',', '"', '');
                    $header = true;
                }
                $rows = (array) $page['rows'];
                foreach ($rows as $row) {
                    fputcsv($fh, array_map(static fn ($v): string => $v === null ? '' : (string) $v, (array) $row), ',', '"', '');
                }
                $offset += 500;
                if (count($rows) < 500) {
                    break;
                }
            }
        } catch (UserFacingException $e) {
            fclose($fh);
            return $this->redirectWithFailure($this->browseUrl($db, $schema, $table), $e);
        }
        rewind($fh);
        $csv = (string) stream_get_contents($fh);
        fclose($fh);
        $name = trim(preg_replace('/[^A-Za-z0-9._-]+/', '_', $db['name'] . '.' . $schema . '.' . $table) ?? 'export', '_.') ?: 'export';
        $this->pg->auditExport($db, $schema, $table);
        return new Response($csv, 200, ['Content-Type' => 'text/csv; charset=utf-8', 'Content-Disposition' => 'attachment; filename="' . $name . '.csv"', 'Cache-Control' => 'no-store']);
    }

    /** @param callable(array<string, mixed>, string, string): void $fn */
    private function rowAction(Request $request, string $done, callable $fn): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $schema = $request->string('schema');
        $table = $request->string('table');
        $back = $this->browseUrl($db, $schema, $table, ['page' => max(1, (int) $request->input('page', 1))]);
        try {
            $fn($db, $schema, $table);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess($back, $done);
    }

    public function rowInsert(Request $request): Response
    {
        return $this->rowAction($request, 'Row added.', function (array $db, string $schema, string $table) use ($request): void {
            $this->pg->insertRow($db, $schema, $table, PostgresService::values((array) $request->input('mode', []), (array) $request->input('value', [])));
        });
    }

    public function rowUpdate(Request $request): Response
    {
        return $this->rowAction($request, 'Row saved.', function (array $db, string $schema, string $table) use ($request): void {
            $this->pg->updateRow($db, $schema, $table, array_map('strval', (array) $request->input('key', [])), PostgresService::values((array) $request->input('mode', []), (array) $request->input('value', [])));
        });
    }

    public function rowDelete(Request $request): Response
    {
        return $this->rowAction($request, 'Row deleted.', function (array $db, string $schema, string $table) use ($request): void {
            $this->pg->deleteRow($db, $schema, $table, array_map('strval', (array) $request->input('key', [])));
        });
    }

    public function install(Request $request): Response
    {
        try {
            $job = $this->pg->install($this->userId($request));
            $this->jobs->track($job, 'Install PostgreSQL', 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob($job);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'Installing PostgreSQL…');
    }

    public function adopt(Request $request): Response
    {
        try {
            $this->pg->adopt($request->string('harden') === '1', $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'NimVor now manages the existing PostgreSQL server.');
    }

    public function service(Request $request): Response
    {
        $op = $request->string('op');
        if (!in_array($op, ['start', 'stop', 'restart', 'reload'], true)) {
            return $this->redirectWithError(self::BASE, 'Unknown service operation.');
        }
        try {
            $this->pg->service($op, $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'PostgreSQL: ' . $op . ' done.');
    }

    public function createDatabase(Request $request): Response
    {
        try {
            $r = $this->pg->createDatabase($request->all(), $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors(self::BASE, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        if (isset($r['password'])) {
            $this->session->flash('secret', ['title' => 'Database user created', 'user' => $r['user'], 'password' => $r['password']]);
        }
        return $this->redirectWithSuccess(self::BASE, 'Database created.');
    }

    public function createUser(Request $request): Response
    {
        try {
            $r = $this->pg->createUser($request->all(), $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors(self::BASE, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        $this->session->flash('secret', ['title' => 'Database user created', 'user' => $r['user'], 'password' => $r['password']]);
        return $this->redirectWithSuccess(self::BASE, 'Database user created.');
    }

    public function password(Request $request): Response
    {
        $role = $this->repo->role($this->pg->pgServerId(), (int) $request->input('id'), $this->user($request));
        if ($role === null) {
            return $this->redirectWithError(self::BASE, 'Database user not found.');
        }
        try {
            $pw = $this->pg->resetPassword($role, $request->string('store_password') === '1');
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        $this->session->flash('secret', ['title' => 'New password', 'user' => $role['name'], 'password' => $pw]);
        return $this->redirectWithSuccess(self::BASE, 'The password was changed.');
    }

    public function grant(Request $request): Response
    {
        $user = $this->user($request);
        $pg = $this->pg->pgServerId();
        $db = $this->repo->database($pg, (int) $request->input('database_id'), $user);
        $role = $this->repo->role($pg, (int) $request->input('role_id'), $user);
        if ($db === null || $role === null) {
            return $this->redirectWithError(self::BASE, 'Database or user not found.');
        }
        try {
            $this->pg->setGrant($db, $role, $request->string('level'));
        } catch (ValidationException $e) {
            return $this->backWithErrors(self::BASE, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'Access updated.');
    }

    public function dropDatabase(Request $request): Response
    {
        $db = $this->repo->database($this->pg->pgServerId(), (int) $request->input('id'), $this->user($request));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        try {
            $this->pg->dropDatabase($db, $request->string('confirm'));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'Database deleted.');
    }

    public function dropUser(Request $request): Response
    {
        $role = $this->repo->role($this->pg->pgServerId(), (int) $request->input('id'), $this->user($request));
        if ($role === null) {
            return $this->redirectWithError(self::BASE, 'Database user not found.');
        }
        try {
            $this->pg->dropUser($role);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'Database user deleted.');
    }
}
