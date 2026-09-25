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
 * Redis key browser. Administrators browse every key; on a website the
 * browser is confined (by the agent) to that website's key prefix, the
 * same prefix its Redis ACL user is limited to.
 */
final class RedisBrowserController extends Controller
{
    private const OPS = ['keys' => ['redis.keys', false], 'get' => ['redis.key_get', false], 'edit' => ['redis.key_edit', true], 'delete' => ['redis.key_delete', true]];
    private const FIELDS = ['db', 'pattern', 'cursor', 'key', 'keys', 'op', 'type', 'field', 'value', 'score', 'index', 'ttl', 'new_key'];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    public static function prefixFor(array $site): string
    {
        return strtolower((string) $site['system_user']) . ':';
    }

    public function browse(Request $request): Response
    {
        return Response::html($this->view->render('redis/browser', [
            'api' => $this->view->url('redis.browser.api'), 'prefix' => '', 'fixedPrefix' => false,
            'back' => $this->view->url('redis'), 'backLabel' => 'Redis', 'heading' => 'Redis keys',
        ], 'layouts/tool'));
    }

    public function siteBrowse(Request $request, string $id): Response
    {
        $site = $this->sites->find($this->server->id(), (int) $id, $this->user($request));
        if ($site === null) {
            return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        return Response::html($this->view->render('redis/browser', [
            'api' => $this->view->url('sites.redis.api', ['id' => (int) $site['id']]), 'prefix' => self::prefixFor($site), 'fixedPrefix' => true,
            'back' => $this->view->url('sites.show', ['id' => (int) $site['id']]), 'backLabel' => (string) $site['domain'], 'heading' => 'Redis keys of ' . $site['domain'],
        ], 'layouts/tool'));
    }

    public function api(Request $request): Response
    {
        $prefix = $request->string('prefix');
        return $this->dispatch($request, strlen($prefix) <= 200 ? $prefix : '', 'server', (string) $this->server->id());
    }

    public function siteApi(Request $request, string $id): Response
    {
        $site = $this->sites->find($this->server->id(), (int) $id, $this->user($request));
        if ($site === null) {
            return Response::json(['success' => false, 'error' => ['message' => 'Website not found.']], 404);
        }
        return $this->dispatch($request, self::prefixFor($site), 'website', (string) $site['id']);
    }

    private function dispatch(Request $request, string $prefix, string $targetType, string $targetId): Response
    {
        $action = $request->string('action');
        if (!isset(self::OPS[$action])) {
            return Response::json(['success' => false, 'error' => ['message' => 'Unknown operation.']], 400);
        }
        [$agentAction, $mutating] = self::OPS[$action];
        $params = ['prefix' => $prefix, 'db' => 0];
        foreach (self::FIELDS as $f) {
            $v = $request->input($f);
            if ($v !== null) {
                $params[$f] = in_array($f, ['db', 'index', 'ttl'], true) ? (int) $v : $v;
            }
        }
        try {
            $data = $this->agent->call($agentAction, $params);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], $e->status() >= 400 && $e->status() < 600 ? $e->status() : 502);
        }
        if ($mutating) {
            $keys = array_map(static fn ($k): string => mb_substr((string) base64_decode((string) $k), 0, 200), (array) ($params['keys'] ?? [$params['key'] ?? '']));
            $this->audit->record('redis.' . ($action === 'delete' ? 'key_delete' : 'key_' . ($params['op'] ?? 'edit')), $targetType, $targetId, AuditLogger::SUCCESS, [
                'db' => $params['db'], 'keys' => array_slice($keys, 0, 20),
            ]);
        }
        return Response::json(['success' => true, 'data' => $data]);
    }
}
