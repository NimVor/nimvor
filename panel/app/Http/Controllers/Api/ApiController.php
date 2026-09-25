<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers\Api;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Middleware\ApiAuthenticate;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Repositories\DatabaseRepository;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\BackupService;
use WhaCP\Services\DatabaseService;
use WhaCP\Services\DeployService;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Services\SslService;
use WhaCP\Services\WebsiteService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * REST API v1. Every endpoint reuses the same services as the web UI, so
 * validation, plan limits, ownership scope, auditing and agent safety are
 * identical. Responses: {"success": true, "data": …} or
 * {"success": false, "error": {"code", "message", …}}.
 */
final class ApiController
{
    public function __construct(
        private readonly WebsiteRepository $sites,
        private readonly WebsiteService $websites,
        private readonly DatabaseRepository $dbRepo,
        private readonly DatabaseService $databases,
        private readonly SslService $ssl,
        private readonly BackupService $backups,
        private readonly DeployService $deploy,
        private readonly JobTracker $jobs,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
    ) {
    }

    /** @return array<string, mixed> */
    private function user(Request $r): array
    {
        return (array) $r->attribute('user', []);
    }

    private static function ok(mixed $data, int $status = 200): Response
    {
        return Response::json(['success' => true, 'data' => $data], $status);
    }

    /** @param callable(): Response $fn */
    private function guard(callable $fn): Response
    {
        try {
            return $fn();
        } catch (ValidationException $e) {
            return ApiAuthenticate::error(422, 'validation_failed', $e->getMessage(), ['fields' => $e->errors()]);
        } catch (AgentException $e) {
            return ApiAuthenticate::error(502, $e->errorCode() !== '' ? $e->errorCode() : 'agent_error', $e->getMessage(), ['reason' => $e->reason()]);
        } catch (UserFacingException $e) {
            return ApiAuthenticate::error($e->status() >= 400 ? $e->status() : 409, 'rejected', $e->getMessage(), ['reason' => $e->reason(), 'action' => $e->remedy()]);
        }
    }

    /** @param array<string, mixed> $s */
    private static function website(array $s): array
    {
        return [
            'id' => (int) $s['id'], 'domain' => $s['domain'], 'aliases' => array_values($s['aliases']), 'kind' => $s['kind'],
            'php_version' => $s['php_version'], 'document_root' => $s['document_root'], 'system_user' => $s['system_user'],
            'https' => (bool) $s['https'], 'enabled' => (bool) $s['enabled'], 'status' => $s['status'],
            'owner_id' => $s['owner_id'] === null ? null : (int) $s['owner_id'], 'created_at' => $s['created_at'],
        ];
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    private static function notFound(): Response
    {
        return ApiAuthenticate::error(404, 'not_found', 'Not found, or not visible to this token.');
    }

    /** @return array<string, mixed> */
    private static function body(Request $r): array
    {
        return $r->all();
    }

    public function openapi(Request $r): Response
    {
        $spec = (string) file_get_contents(dirname(__DIR__, 4) . '/resources/api/openapi.yaml');
        return new Response($spec, 200, ['Content-Type' => 'application/yaml; charset=utf-8']);
    }

    public function me(Request $r): Response
    {
        $u = $this->user($r);
        $token = (array) $r->attribute('api_token', []);
        return self::ok([
            'id' => (int) $u['id'], 'username' => $u['username'], 'role' => $u['role_name'], 'scope' => $u['role_scope'],
            'token' => ['name' => $token['name'] ?? '', 'ability' => $token['ability'] ?? '', 'expires_at' => $token['expires_at'] ?? null],
            'server_id' => $this->server->id(),
        ]);
    }

    public function serverInfo(Request $r): Response
    {
        return $this->guard(fn (): Response => self::ok([
            'info' => $this->agent->call('system.info'),
            'metrics' => $this->agent->call('system.metrics'),
        ]));
    }

    public function listWebsites(Request $r): Response
    {
        return self::ok(array_map([self::class, 'website'], $this->sites->all($this->server->id(), $this->user($r))));
    }

    public function showWebsite(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : self::ok(self::website($s));
    }

    public function createWebsite(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $in = self::body($r);
            if (isset($in['aliases']) && is_array($in['aliases'])) {
                $in['aliases'] = implode("\n", $in['aliases']);
            }
            $id = $this->websites->create($in, $this->user($r));
            return self::ok(self::website((array) $this->sites->find($this->server->id(), $id, null)), 201);
        });
    }

    public function setEnabled(Request $r, string $id, bool $enabled): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s, $enabled): Response {
            $this->websites->setEnabled($s, $enabled);
            return self::ok(self::website((array) $this->sites->find($this->server->id(), (int) $s['id'], null)));
        });
    }

    public function enableWebsite(Request $r, string $id): Response
    {
        return $this->setEnabled($r, $id, true);
    }

    public function disableWebsite(Request $r, string $id): Response
    {
        return $this->setEnabled($r, $id, false);
    }

    public function deleteWebsite(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $warnings = $this->websites->delete($s, $r->query('remove_files') === '1', $r->query('remove_user') === '1');
            return self::ok(['deleted' => (int) $s['id'], 'warnings' => $warnings]);
        });
    }

    public function issueSsl(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $this->ssl->issue($s, (string) ($in['email'] ?? ''), filter_var($in['include_aliases'] ?? true, FILTER_VALIDATE_BOOL), filter_var($in['staging'] ?? false, FILTER_VALIDATE_BOOL));
            return self::ok(self::website((array) $this->sites->find($this->server->id(), (int) $s['id'], null)));
        });
    }

    public function listBackups(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            $this->backups->refresh();
            return self::ok(array_map(static fn (array $b): array => [
                'id' => (int) $b['id'], 'backup_id' => $b['backup_id'], 'label' => $b['label'], 'status' => $b['status'], 'size' => (int) $b['size'],
                'encrypted' => $b['encrypted'], 'parts' => $b['parts'], 'created_at' => $b['created_at'], 'error' => $b['error'],
            ], $this->backups->list($s)));
        });
    }

    public function createBackup(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $job = $this->backups->createSite($s, [
                'files' => filter_var($in['files'] ?? true, FILTER_VALIDATE_BOOL),
                'databases' => filter_var($in['databases'] ?? true, FILTER_VALIDATE_BOOL),
                'encrypt' => filter_var($in['encrypt'] ?? true, FILTER_VALIDATE_BOOL),
                'label' => (string) ($in['label'] ?? ''),
            ], (int) $this->user($r)['id']);
            $this->jobs->track($job, 'Backup ' . $s['domain'], 'website', (string) $s['id'], (int) $this->user($r)['id']);
            return self::ok(['job_id' => $job], 202);
        });
    }

    public function deployWebsite(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $job = $this->deploy->deploy($s, 'api', (int) $this->user($r)['id']);
            $this->jobs->track($job, 'Deploy ' . $s['domain'], 'website', (string) $s['id'], (int) $this->user($r)['id']);
            return self::ok(['job_id' => $job], 202);
        });
    }

    public function listDatabases(Request $r): Response
    {
        return self::ok(array_map(static fn (array $d): array => [
            'id' => (int) $d['id'], 'name' => $d['name'], 'website_id' => $d['website_id'] === null ? null : (int) $d['website_id'], 'created_at' => $d['created_at'],
        ], $this->dbRepo->databases($this->server->id(), $this->user($r))));
    }

    public function createDatabase(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $res = $this->databases->create(self::body($r), $this->user($r));
            return self::ok($res, 201); // includes the generated password once, when a user was created
        });
    }

    public function job(Request $r, string $id): Response
    {
        $job = preg_match('/^[a-f0-9]{16}$/', $id) ? $this->jobs->find($id) : null;
        $u = $this->user($r);
        if ($job === null || !$this->jobs->visibleTo($job, $u)) {
            return self::notFound();
        }
        return $this->guard(fn (): Response => self::ok($this->jobs->poll($id, (int) $r->query('offset', 0)) + ['kind' => $job['kind']]));
    }
}
