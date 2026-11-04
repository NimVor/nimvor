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
        $all = $this->pg->serverIds();
        $servers = $this->pg->servers();
        $remote = array_values(array_filter($servers, static fn (array $s): bool => (string) $s['connection_type'] === 'remote'));
        $names = array_column($servers, 'name', 'id');
        $databases = $this->repo->databases($all, $user);
        $roles = $this->repo->roles($all, $user);
        $sizes = [];
        foreach ((array) ($live['databases'] ?? []) as $d) {
            $sizes[(string) $d['name']] = (array) $d;
        }
        foreach ($databases as &$d) {
            $isLocal = (int) $d['pg_server_id'] === $pg;
            $d['size_bytes'] = $isLocal ? (int) ($sizes[(string) $d['name']]['size_bytes'] ?? 0) : 0;
            $d['connections'] = $isLocal ? (int) ($sizes[(string) $d['name']]['connections'] ?? 0) : 0;
            $d['live'] = $isLocal && isset($sizes[(string) $d['name']]);
            $d['server_name'] = (string) ($names[(int) $d['pg_server_id']] ?? '');
            $d['remote'] = !$isLocal;
        }
        unset($d);
        foreach ($roles as &$r) {
            $r['server_name'] = (string) ($names[(int) $r['pg_server_id']] ?? '');
        }
        unset($r);
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
            'servers' => Scope::isAll($user) ? $servers : [], 'remoteServers' => $remote, 'sslModes' => PostgresService::SSL_MODES,
        ]);
    }

    /** @return array<string, mixed>|null the database record, only when the signed-in account may use it */
    private function databaseOf(Request $request, mixed $id): ?array
    {
        return $this->repo->database($this->pg->serverIds(), (int) $id, $this->user($request));
    }

    /** A page in the full-height tool layout, like the MySQL database browser. */
    private function tool(string $template, array $data): Response
    {
        return Response::html($this->view->render($template, $data, 'layouts/tool'));
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
        $tab = (string) $request->query('tab', 'browse');
        if (in_array($tab, ['overview', 'objects', 'routines', 'sequences'], true) || ($table === '' && $request->query('tab') === null)) {
            return $this->databaseSections($request, $db, in_array($tab, ['objects', 'routines', 'sequences'], true) ? $tab : 'overview');
        }
        if ($table === '' || !in_array($tab, ['structure', 'insert'], true)) {
            $tab = 'browse';
        }
        $data = ['db' => $db, 'schema' => $schema, 'table' => $table, 'tab' => $tab, 'per' => $per, 'pageNo' => $page, 'q' => $request->query('q', ''),
            'fc' => (string) $request->query('fc', ''), 'fo' => (string) $request->query('fo', ''), 'fv' => (string) $request->query('fv', ''),
            'sort' => (string) $request->query('sort', ''), 'dir' => (string) $request->query('dir', 'asc'), 'result' => null, 'structure' => null, 'details' => null, 'partitions' => null, 'tables' => ['schemas' => [], 'tables' => []]];
        try {
            $data['tables'] = $this->pg->tables($db);
            if ($table !== '') {
                $data['result'] = $this->pg->browse($db, $schema, $table, $this->criteria($request) + ['offset' => ($page - 1) * $per, 'limit' => $per]);
                $data['structure'] = $this->pg->structure($db, $schema, $table);
                if ($tab === 'structure') {
                    $data['details'] = $this->pg->details($db, $schema, $table);
                    $data['partitions'] = $this->pg->partitions($db, $schema, $table);
                }
            }
        } catch (UserFacingException $e) {
            $this->session->flash('error', $e->getMessage() . ($e->reason() !== '' ? ' ' . $e->reason() : ''));
            return Response::redirect($table !== '' ? $this->browseUrl($db, '', '') : self::BASE);
        }
        $data['canWrite'] = $this->view->can(P::DATABASES_MANAGE);
        return $this->tool('postgres/browser', $data);
    }

    /** The database-level sections: the overview and the list of objects (tables, views, ...) with their statistics. */
    private function databaseSections(Request $request, array $db, string $tab): Response
    {
        $q = [
            'schema' => mb_substr((string) $request->query('schema', ''), 0, 63), 'kind' => (string) $request->query('kind', ''),
            'search' => mb_substr(trim((string) $request->query('q', '')), 0, 100), 'sort' => (string) $request->query('sort', 'name'),
            'dir' => (string) $request->query('dir', 'asc') === 'desc' ? 'desc' : 'asc',
        ];
        $per = in_array((int) $request->query('per', 50), [25, 50, 100, 200], true) ? (int) $request->query('per', 50) : 50;
        $page = max(1, (int) $request->query('page', 1));
        $data = ['db' => $db, 'schema' => '', 'table' => '', 'tab' => $tab, 'tables' => ['schemas' => [], 'tables' => []], 'overview' => null,
            'objects' => null, 'routines' => null, 'definition' => null, 'sequences' => null, 'error' => null, 'f' => $q, 'per' => $per, 'pageNo' => $page, 'canWrite' => $this->view->can(P::DATABASES_MANAGE)];
        try {
            $data['tables'] = $this->pg->tables($db);
            if ($tab === 'overview') {
                $data['overview'] = $this->pg->dbOverview($db);
            } elseif ($tab === 'routines') {
                $data['routines'] = $this->pg->routines($db, $q['schema'], $q['search']);
                $data['definition'] = ctype_digit((string) $request->query('def', '')) ? $this->pg->routineDefinition($db, (int) $request->query('def')) : null;
            } elseif ($tab === 'sequences') {
                $data['sequences'] = $this->pg->sequences($db);
            } else {
                $data['objects'] = $this->pg->objects($db, $q + ['offset' => ($page - 1) * $per, 'limit' => $per]);
            }
        } catch (UserFacingException $e) {
            $data['error'] = $e;
        }
        return $this->tool('postgres/' . $tab, $data);
    }

    /** @return array<string, mixed> a column description from a form's fields */
    private function columnFrom(array $f): array
    {
        $kind = (string) ($f['default_kind'] ?? 'none');
        return [
            'name' => trim((string) ($f['name'] ?? '')), 'type' => trim((string) ($f['type'] ?? '')), 'not_null' => !empty($f['not_null']),
            'primary' => !empty($f['primary']), 'identity' => !empty($f['identity']),
            'has_default' => $kind === 'literal' || $kind === 'function', 'default' => (string) ($f['default'] ?? ''), 'default_fn' => $kind === 'function',
        ];
    }

    /** @return list<string> */
    private function csv(string $v): array
    {
        return array_values(array_filter(array_map('trim', explode(',', $v)), static fn (string $x): bool => $x !== ''));
    }

    /** @return array<string, string>|null a column / operator / value condition from the form, or null when empty */
    private function predicateFrom(Request $request): ?array
    {
        $col = trim((string) $request->input('pcol', ''));
        $op = (string) $request->input('pop', '');
        if ($col === '' || $op === '') {
            return null;
        }
        return ['column' => $col, 'op' => $op, 'value' => (string) $request->input('pval', ''), 'value2' => (string) $request->input('pval2', '')];
    }

    /** One structure change from a form: create / rename / duplicate / comment / move a table, or change a column. */
    public function alter(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $op = (string) $request->input('op', '');
        $schema = trim((string) $request->input('schema', ''));
        $table = trim((string) $request->input('table', ''));
        $params = ['op' => $op, 'schema' => $schema, 'table' => $table];
        $tab = 'structure';
        switch ($op) {
            case 'create_table':
                $cols = [];
                foreach ((array) $request->input('cols', []) as $c) {
                    if (is_array($c) && trim((string) ($c['name'] ?? '')) !== '') {
                        $cols[] = $this->columnFrom($c);
                    }
                }
                $params['columns'] = $cols;
                $params['if_not_exists'] = false;
                $tab = 'browse';
                break;
            case 'rename_table':
            case 'duplicate_table':
                $params['new_name'] = trim((string) $request->input('new_name', ''));
                $params['with_data'] = $op === 'duplicate_table' && (string) $request->input('with_data', '') !== '';
                break;
            case 'set_schema':
                $params['new_schema'] = trim((string) $request->input('new_schema', ''));
                break;
            case 'comment_table':
                $params['comment'] = (string) $request->input('comment', '');
                break;
            case 'add_column':
                $params['column'] = $this->columnFrom((array) $request->input('col', []));
                break;
            case 'drop_column':
            case 'rename_column':
            case 'alter_column':
                $params['name'] = trim((string) $request->input('name', ''));
                $params['new_name'] = trim((string) $request->input('new_name', ''));
                $params['cascade'] = (string) $request->input('cascade', '') !== '';
                $params['extra'] = ['alter_what' => (string) $request->input('alter_what', ''), 'value' => (string) $request->input('value', ''), 'kind' => (string) $request->input('value_kind', '')];
                break;
            case 'create_index':
                $icols = [];
                foreach ((array) $request->input('icols', []) as $c) {
                    if (is_array($c) && trim((string) ($c['name'] ?? '')) !== '') {
                        $icols[] = ['name' => trim((string) $c['name']), 'order' => (string) ($c['order'] ?? ''), 'nulls' => (string) ($c['nulls'] ?? '')];
                    }
                }
                $params['name'] = trim((string) $request->input('name', ''));
                $params['extra'] = [
                    'method' => (string) $request->input('method', 'btree'), 'unique' => (string) $request->input('unique', '') !== '', 'concurrent' => (string) $request->input('concurrent', '') !== '',
                    'index_columns' => $icols, 'include' => $this->csv((string) $request->input('include', '')), 'predicate' => $this->predicateFrom($request),
                ];
                break;
            case 'drop_index':
            case 'rename_index':
            case 'reindex_index':
            case 'drop_constraint':
            case 'validate_constraint':
            case 'rename_constraint':
            case 'enable_trigger':
            case 'disable_trigger':
            case 'drop_trigger':
                $params['name'] = trim((string) $request->input('name', ''));
                $params['new_name'] = trim((string) $request->input('new_name', ''));
                $params['cascade'] = (string) $request->input('cascade', '') !== '';
                $params['extra'] = ['concurrent' => (string) $request->input('concurrent', '') !== ''];
                break;
            case 'create_partition':
            case 'attach_partition':
            case 'detach_partition':
                $params['new_name'] = trim((string) $request->input('new_name', ''));
                $params['name'] = trim((string) $request->input('name', ''));
                $params['extra'] = ['concurrent' => (string) $request->input('concurrent', '') !== '', 'bound' => [
                    'kind' => (string) $request->input('bound_kind', ''), 'from' => $this->csv((string) $request->input('bound_from', '')), 'to' => $this->csv((string) $request->input('bound_to', '')),
                    'values' => $this->csv((string) $request->input('bound_values', '')), 'modulus' => (int) $request->input('bound_modulus', 0), 'remainder' => (int) $request->input('bound_remainder', 0),
                ]];
                $params['schema'] = $schema;
                break;
            case 'add_constraint':
                $params['name'] = trim((string) $request->input('name', ''));
                $params['extra'] = [
                    'kind' => (string) $request->input('kind', ''), 'columns' => $this->csv((string) $request->input('columns', '')), 'predicate' => $this->predicateFrom($request),
                    'ref_schema' => trim((string) $request->input('ref_schema', $schema)) ?: $schema, 'ref_table' => trim((string) $request->input('ref_table', '')), 'ref_columns' => $this->csv((string) $request->input('ref_columns', '')),
                    'on_delete' => (string) $request->input('on_delete', ''), 'on_update' => (string) $request->input('on_update', ''),
                    'not_valid' => (string) $request->input('not_valid', '') !== '', 'deferrable' => (string) $request->input('deferrable', '') !== '',
                ];
                break;
            default:
                return $this->redirectWithError($this->browseUrl($db, $schema, $table), 'That change is not available.');
        }
        if ($op === 'drop_column' && trim((string) $request->input('confirm', '')) !== $params['name']) {
            return $this->redirectWithError($this->browseUrl($db, $schema, $table, ['tab' => 'structure']), 'Type the column name to confirm dropping it. Nothing was changed.');
        }
        // After a rename or a move the table has a new address.
        $target = $params['new_name'] ?? '';
        try {
            $this->pg->alter($db, $params, $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($this->browseUrl($db, $schema, $table, $table === '' ? [] : ['tab' => $tab]), $e);
        }
        if ($op === 'rename_table' && $target !== '') {
            $table = $target;
        } elseif ($op === 'set_schema') {
            $schema = (string) ($params['new_schema'] ?? $schema);
        }
        $extra = $op === 'create_table' || $table === '' ? [] : ['tab' => $tab];
        return $this->redirectWithSuccess($this->browseUrl($db, $schema, $table, $extra), PostgresService::ALTER_OPS[$op] . '.');
    }

    /** Several tables / views at once (maintenance, empty, drop) from the Objects list. */
    public function objectsBulk(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $back = self::BASE . '/browse?' . http_build_query(['db' => (int) $db['id'], 'tab' => 'objects']);
        $items = [];
        foreach ((array) $request->input('sel', []) as $raw) {
            $pair = json_decode((string) $raw, true);
            if (is_array($pair) && count($pair) === 2 && is_string($pair[0] ?? null) && is_string($pair[1] ?? null)) {
                $items[] = [$pair[0], $pair[1]];
            }
        }
        $op = (string) $request->input('op', '');
        try {
            $out = $this->pg->objectsBulk($db, $op, array_slice($items, 0, 100), [
                'cascade' => (string) $request->input('cascade', '') !== '', 'restart_identity' => (string) $request->input('restart_identity', '') !== '',
            ], trim((string) $request->input('confirm', '')), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        $failed = array_values(array_filter((array) ($out['results'] ?? []), static fn ($r): bool => empty($r['ok'])));
        $ok = (int) ($out['succeeded'] ?? 0);
        if ($failed === []) {
            return $this->redirectWithSuccess($back, PostgresService::BULK_OPS[$op] . ': done for ' . $ok . ' object' . ($ok === 1 ? '' : 's') . '.');
        }
        $why = implode('; ', array_map(static fn ($r): string => (string) $r['schema'] . '.' . (string) $r['name'] . ' - ' . (string) ($r['error'] ?? 'failed'), array_slice($failed, 0, 5)));
        return $this->redirectWithError($back, PostgresService::BULK_OPS[$op] . ': ' . $ok . ' done, ' . count($failed) . ' failed. ' . $why . (count($failed) > 5 ? '; and ' . (count($failed) - 5) . ' more.' : ''));
    }

    /** Drop one routine, addressed by the OID the list showed. */
    public function routineDrop(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $back = self::BASE . '/browse?' . http_build_query(['db' => (int) $db['id'], 'tab' => 'routines']);
        try {
            $this->pg->routineDrop($db, (int) $request->input('oid', 0), (string) $request->input('label', ''), (string) $request->input('cascade', '') !== '', trim((string) $request->input('confirm', '')), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess($back, 'Routine dropped.');
    }

    /** Create / change / set the value of / sync / drop a sequence. */
    public function sequenceAlter(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $back = self::BASE . '/browse?' . http_build_query(['db' => (int) $db['id'], 'tab' => 'sequences']);
        $num = static function (mixed $v): ?int {
            $v = trim((string) $v);
            return $v !== '' && preg_match('/^-?\d{1,18}$/', $v) === 1 ? (int) $v : null;
        };
        $params = ['op' => (string) $request->input('op', ''), 'schema' => trim((string) $request->input('schema', '')), 'name' => trim((string) $request->input('name', '')), 'cascade' => (string) $request->input('cascade', '') !== ''];
        foreach (['value', 'start', 'increment', 'min', 'max', 'cache'] as $f) {
            $n = $num($request->input($f));
            if ($n !== null) {
                $params[$f] = $n;
            }
        }
        $cycle = (string) $request->input('cycle', '');
        if ($cycle !== '') {
            $params['cycle'] = $cycle === 'yes';
        }
        if ($params['op'] === 'drop' && trim((string) $request->input('confirm', '')) !== $params['name']) {
            return $this->redirectWithError($back, 'Type the sequence name to confirm dropping it. Nothing was changed.');
        }
        try {
            $this->pg->sequenceAlter($db, $params, $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess($back, PostgresService::SEQ_OPS[$params['op']] . '.');
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
        $tables = ['schemas' => [], 'tables' => []];
        try {
            $tables = $this->pg->tables($db);
        } catch (UserFacingException) {
            // The table list on the left stays empty; the editor itself does not need it.
        }
        return $this->tool('postgres/sql', [
            'db' => $db, 'schema' => $schema, 'schemaError' => $error, 'history' => $this->pg->history($db, $this->userId($request)),
            'canWrite' => $this->view->can(P::DATABASES_MANAGE), 'tables' => $tables,
            'selSchema' => (string) $request->query('schema', ''), 'selTable' => (string) $request->query('table', ''),
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

    // ---- backups and monitoring -------------------------------------------------------------------

    public function backups(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->query('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $list = [];
        $error = null;
        try {
            $list = $this->pg->backups($db);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('postgres/backups', [
            'db' => $db, 'backups' => $list, 'error' => $error, 'schedule' => $this->pg->schedule($db),
            'canManage' => $this->view->can(P::DATABASES_MANAGE),
        ]);
    }

    public function backupCreate(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $back = self::BASE . '/backups?db=' . (int) $db['id'];
        try {
            $job = $this->pg->createBackup($db, $this->userId($request));
            $this->jobs->track($job, 'Back up PostgreSQL database ' . $db['name'], 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob($job);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess($back, 'Backing up ' . $db['name'] . '…');
    }

    public function backupRestore(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $back = self::BASE . '/backups?db=' . (int) $db['id'];
        try {
            $job = $this->pg->restoreBackup($db, $request->string('file'), $this->userId($request));
            $this->jobs->track($job, 'Restore PostgreSQL database ' . $db['name'], 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob($job);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess($back, 'Restoring ' . $db['name'] . '…');
    }

    public function backupDelete(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $back = self::BASE . '/backups?db=' . (int) $db['id'];
        try {
            $this->pg->deleteBackup($db, $request->string('file'), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess($back, 'Backup deleted.');
    }

    public function scheduleSave(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $back = self::BASE . '/backups?db=' . (int) $db['id'];
        try {
            $this->pg->saveSchedule($db, $request->string('frequency'), (int) $request->input('hour'), (int) $request->input('keep'), $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors($back, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        return $this->redirectWithSuccess($back, 'Automatic backups saved.');
    }

    public function scheduleDelete(Request $request): Response
    {
        $db = $this->databaseOf($request, $request->input('db'));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        $this->pg->deleteSchedule($db, $this->userId($request));
        return $this->redirectWithSuccess(self::BASE . '/backups?db=' . (int) $db['id'], 'Automatic backups turned off.');
    }

    /** Server health for administrators (postgres.manage). */
    public function monitor(Request $request): Response
    {
        $data = null;
        $error = null;
        $servers = $this->pg->servers();
        $chosen = (int) $request->query('server', 0);
        $ids = array_map(static fn (array $s): int => (int) $s['id'], $servers);
        $chosen = in_array($chosen, $ids, true) ? $chosen : $this->pg->pgServerId();
        try {
            $data = $this->pg->monitor($chosen);
        } catch (UserFacingException $e) {
            $error = $e;
        }
        return $this->render('postgres/monitor', ['m' => $data, 'error' => $error, 'servers' => $servers, 'chosen' => $chosen]);
    }

    // ---- registered servers (postgres.manage) -----------------------------------------------------

    public function serverCreate(Request $request): Response
    {
        try {
            $this->pg->registerServer($request->all(), $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors(self::BASE, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'The PostgreSQL server was registered.');
    }

    public function serverTest(Request $request): Response
    {
        try {
            $version = $this->pg->testServer((int) $request->input('id'), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'The server answered' . ($version !== '' ? ' (PostgreSQL ' . $version . ').' : '.'));
    }

    public function serverRemove(Request $request): Response
    {
        try {
            $this->pg->removeServer((int) $request->input('id'), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'The server was removed from NimVor. Nothing on it was changed.');
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
        $role = $this->repo->role($this->pg->serverIds(), (int) $request->input('id'), $this->user($request));
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
        $pg = $this->pg->serverIds();
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
        $db = $this->repo->database($this->pg->serverIds(), (int) $request->input('id'), $this->user($request));
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
        $role = $this->repo->role($this->pg->serverIds(), (int) $request->input('id'), $this->user($request));
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
