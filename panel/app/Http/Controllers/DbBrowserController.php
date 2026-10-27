<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\DatabaseRepository;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\ServerContext;
use WhaCP\View\View;

/**
 * Web database browser (tables, rows, structure, SQL console).
 *
 * Every operation runs in the agent as the database's own browser account,
 * which has privileges on that database only; the panel checks that the
 * user may manage the database and records changes in the audit log.
 */
final class DbBrowserController extends Controller
{
    /** operation => [agent action, changes data] */
    private const OPS = [
        'tables' => ['db.browse_tables', false],
        'structure' => ['db.browse_structure', false],
        'rows' => ['db.browse_rows', false],
        'row' => ['db.browse_row', false],
        'save' => ['db.browse_save', true],
        'delete' => ['db.browse_delete', true],
        'query' => ['db.query', true],
        'alter' => ['db.browse_alter', true],
        'export_csv' => ['db.browse_export_csv', false],
        'import_csv' => ['db.browse_import_csv', true],
    ];
    private const FIELDS = [
        'table', 'offset', 'limit', 'order', 'desc', 'filter', 'search', 'sql', 'key', 'keys', 'values', 'insert',
        // Structure changes (the operation travels as alter_op: op names the browser operation).
        'alter_op', 'new_name', 'column', 'columns', 'name', 'after', 'kind', 'index_columns', 'ref_table', 'ref_columns', 'on_delete', 'on_update', 'primary', 'engine', 'dry_run',
        // CSV import.
        'csv', 'delimiter', 'header', 'mode',
    ];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly DatabaseRepository $repo,
        private readonly ServerContext $server,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    /**
     * The database name the user may browse: a managed database they can
     * see, or (administrators) any database on the server by name.
     *
     * @return array{name: string, id: int|null, website_id: int|null}|null
     */
    private function resolve(Request $request, ?string $id): ?array
    {
        $user = $this->user($request);
        if ($id !== null) {
            $db = $this->repo->database($this->server->id(), (int) $id, $user);
            return $db === null ? null : ['name' => (string) $db['name'], 'id' => (int) $db['id'], 'website_id' => $db['website_id'] !== null ? (int) $db['website_id'] : null];
        }
        $name = (string) $request->query('name', '');
        if (!Scope::isAll($user) || !preg_match('/^[A-Za-z0-9_]{1,64}$/', $name)) {
            return null;
        }
        return ['name' => $name, 'id' => null, 'website_id' => null];
    }

    public function browse(Request $request, ?string $id = null): Response
    {
        $db = $this->resolve($request, $id);
        if ($db === null) {
            return $this->render('errors/error', ['title' => 'Database not found', 'message' => 'This database does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $back = $db['website_id'] !== null ? $this->view->url('sites.databases', ['id' => $db['website_id']]) : $this->view->url('databases');
        $api = $db['id'] !== null ? $this->view->url('databases.browser.api', ['id' => $db['id']]) : $this->view->url('databases.browser.server_api') . '?name=' . rawurlencode($db['name']);
        return Response::html($this->view->render('databases/browser', [
            'api' => $api, 'database' => $db['name'], 'back' => $back,
        ], 'layouts/tool'));
    }

    public function serverBrowse(Request $request): Response
    {
        return $this->browse($request, null);
    }

    public function api(Request $request, ?string $id = null): Response
    {
        $db = $this->resolve($request, $id);
        if ($db === null) {
            return Response::json(['success' => false, 'error' => ['message' => 'Database not found.']], 404);
        }
        $op = $request->string('op');
        if (!isset(self::OPS[$op])) {
            return Response::json(['success' => false, 'error' => ['message' => 'Unknown operation.']], 400);
        }
        [$action, $changes] = self::OPS[$op];
        $params = ['database' => $db['name']];
        foreach (self::FIELDS as $f) {
            $v = $request->input($f);
            if ($v !== null) {
                $params[$f === 'alter_op' ? 'op' : $f] = match ($f) {
                    'offset', 'limit' => (int) $v,
                    'desc', 'insert', 'dry_run', 'header' => (bool) $v,
                    'table', 'order', 'sql', 'search', 'csv', 'alter_op', 'new_name', 'name', 'after', 'kind', 'ref_table', 'on_delete', 'on_update', 'engine', 'delimiter', 'mode' => is_scalar($v) ? (string) $v : '',
                    default => $v,
                };
            }
        }
        $target = $db['website_id'] !== null ? ['website', (string) $db['website_id']] : ['server', (string) $this->server->id()];
        try {
            $data = $this->agent->call($action, $params, 90);
        } catch (AgentException $e) {
            if ($changes) {
                $this->audit->record('database.browser_' . $op, $target[0], $target[1], AuditLogger::FAILURE, $this->auditContext($op, $db['name'], $params) + ['error' => $e->getMessage()]);
            }
            return Response::json(['success' => false, 'error' => $e->toArray()], $e->status() >= 400 && $e->status() < 600 ? $e->status() : 502);
        }
        if ($changes && !($op === 'alter' && !empty($params['dry_run']))) {
            $ctx = $this->auditContext($op, $db['name'], $params, (array) $data);
            if ($op === 'query' && isset($data['error'])) {
                $ctx['error'] = mb_substr((string) $data['error'], 0, 300);
            }
            $this->audit->record('database.browser_' . $op, $target[0], $target[1], isset($data['error']) ? AuditLogger::FAILURE : AuditLogger::SUCCESS, $ctx);
        }
        return Response::json(['success' => true, 'data' => $data]);
    }

    public function serverApi(Request $request): Response
    {
        return $this->api($request, null);
    }

    /**
     * What the audit log keeps: the database, table and statement (cut
     * short), never the full row values.
     *
     * @param array<string, mixed> $params
     * @param array<mixed> $data
     * @return array<string, mixed>
     */
    private function auditContext(string $op, string $db, array $params, array $data = []): array
    {
        $ctx = ['database' => $db];
        if (isset($params['table'])) {
            $ctx['table'] = mb_substr((string) $params['table'], 0, 64);
        }
        return $ctx + match ($op) {
            'query' => ['sql' => mb_substr((string) ($params['sql'] ?? ''), 0, 1000)],
            'save' => ['insert' => !empty($params['insert']), 'columns' => array_slice(array_map('strval', array_keys((array) ($params['values'] ?? []))), 0, 50), 'key' => $params['key'] ?? null],
            'delete' => ['rows' => count((array) ($params['keys'] ?? []))],
            'alter' => ['operation' => (string) ($params['op'] ?? ''), 'sql' => mb_substr((string) ($data['sql'] ?? ''), 0, 1000)],
            'import_csv' => ['bytes' => strlen((string) ($params['csv'] ?? '')), 'rows' => $data['rows'] ?? null, 'mode' => (string) ($params['mode'] ?? 'insert')],
            default => [],
        };
    }
}
