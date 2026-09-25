<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\ServerContext;
use WhaCP\View\View;

/**
 * Website file manager. All confinement is enforced by the agent (os.Root);
 * the panel checks that the user may access the website.
 */
final class FilesController extends Controller
{
    private const OPS = [
        'list' => ['files.list', false], 'read' => ['files.read', false], 'search' => ['files.search', false],
        'write' => ['files.write', true], 'mkdir' => ['files.mkdir', true], 'rename' => ['files.rename', true],
        'copy' => ['files.copy', true], 'delete' => ['files.delete', true], 'chmod' => ['files.chmod', true],
        'compress' => ['files.compress', true], 'extract' => ['files.extract', true],
    ];

    /** Parameters each operation may pass through to the agent. */
    private const FIELDS = ['path', 'content', 'expected_mtime', 'from', 'to', 'paths', 'mode', 'query', 'dest', 'overwrite'];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly \WhaCP\Services\JobTracker $jobs,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null */
    private function site(Request $request, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($request));
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $start = ltrim(substr((string) $site['document_root'], strlen((string) $site['root'])), '/');
        return Response::html($this->view->render('files/manager', ['site' => $site, 'start' => $start], 'layouts/tool'));
    }

    public function edit(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        $path = trim((string) $request->query('path', ''), '/');
        if ($site === null || $path === '' || str_contains('/' . $path . '/', '/../')) {
            return $this->render('errors/error', ['title' => 'File not found', 'message' => 'This file does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $folder = str_contains($path, '/') ? substr($path, 0, (int) strrpos($path, '/')) : '';
        return Response::html($this->view->render('files/editor', ['site' => $site, 'path' => $path, 'folder' => $folder], 'layouts/tool'));
    }

    public function api(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return Response::json(['success' => false, 'error' => ['message' => 'Website not found.']], 404);
        }
        $op = $request->string('op');
        if (!isset(self::OPS[$op])) {
            return Response::json(['success' => false, 'error' => ['message' => 'Unknown operation.']], 400);
        }
        [$action, $mutating] = self::OPS[$op];
        $params = ['domain' => $site['domain']];
        foreach (self::FIELDS as $f) {
            $v = $request->input($f);
            if ($v !== null) {
                $params[$f] = $v;
            }
        }
        try {
            $data = $this->agent->call($action, $params);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], $e->status() >= 400 && $e->status() < 600 ? $e->status() : 502);
        }
        if (is_array($data) && isset($data['job_id'])) {
            $this->jobs->track((string) $data['job_id'], ($op === 'compress' ? 'Compress in ' : 'Extract in ') . $site['domain'], 'website', (string) $site['id'], $this->userId($request));
        }
        if ($mutating) {
            $this->audit->record('files.' . $op, 'website', (string) $site['id'], AuditLogger::SUCCESS, [
                'domain' => $site['domain'],
                'path' => $params['path'] ?? ($params['from'] ?? '') . (isset($params['to']) ? ' → ' . $params['to'] : '') . (isset($params['dest']) ? ' → ' . $params['dest'] : ''),
                'paths' => isset($params['paths']) ? array_slice((array) $params['paths'], 0, 20) : null,
            ]);
        }
        return Response::json(['success' => true, 'data' => $data]);
    }

    public function upload(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        $chunk = $_FILES['chunk'] ?? null;
        if ($site === null || !is_array($chunk) || ($chunk['error'] ?? 1) !== UPLOAD_ERR_OK || !is_uploaded_file((string) $chunk['tmp_name'])) {
            return Response::json(['success' => false, 'error' => ['message' => 'Upload failed.']], 400);
        }
        $data = (string) file_get_contents((string) $chunk['tmp_name']);
        $final = $request->input('final') === '1';
        $path = $request->string('path');
        try {
            $res = $this->agent->call('files.upload', [
                'domain' => $site['domain'],
                'path' => $path,
                'offset' => (int) $request->input('offset', 0),
                'data' => base64_encode($data),
                'final' => $final,
            ]);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], 400);
        }
        if ($final) {
            $this->audit->record('files.upload', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'path' => $path, 'size' => $res['size'] ?? 0]);
        }
        return Response::json(['success' => true, 'data' => $res]);
    }

    public function download(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        $path = (string) $request->query('path', '');
        if ($site === null || $path === '') {
            return Response::html('Not found', 404);
        }
        try {
            $first = (array) $this->agent->call('files.download', ['domain' => $site['domain'], 'path' => $path, 'offset' => 0]);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id . '/files', $e);
        }
        $this->audit->record('files.download', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'path' => $path]);
        $name = preg_replace('/[^A-Za-z0-9._-]/', '_', basename($path)) ?: 'download';
        $agent = $this->agent;
        $domain = (string) $site['domain'];
        return Response::stream(static function () use ($first, $agent, $domain, $path): void {
            if (session_status() === PHP_SESSION_ACTIVE) {
                session_write_close(); // do not block other tabs during a long download
            }
            $chunk = $first;
            $offset = 0;
            while (true) {
                $bytes = base64_decode((string) ($chunk['data'] ?? ''), true) ?: '';
                echo $bytes;
                flush();
                $offset += strlen($bytes);
                if (!empty($chunk['eof']) || $bytes === '') {
                    break;
                }
                $chunk = (array) $agent->call('files.download', ['domain' => $domain, 'path' => $path, 'offset' => $offset]);
            }
        }, [
            'Content-Type' => 'application/octet-stream',
            'Content-Disposition' => 'attachment; filename="' . $name . '"',
            'Content-Length' => (string) (int) ($first['size'] ?? 0),
            'X-Content-Type-Options' => 'nosniff',
            'Cache-Control' => 'no-store',
        ]);
    }
}
