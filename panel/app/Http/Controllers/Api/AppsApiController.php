<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\AppCatalogService;
use NimVor\Services\AppInstallerService;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;

/**
 * REST API v1: the application installer. Every endpoint calls AppInstallerService and AppCatalogService,
 * as the web interface does: only applications the token's user may install are offered, the installer
 * checks the website's type, creates the database in the same step, and an install into a folder that is
 * already being installed into is refused. A generated administrator password is returned once.
 */
final class AppsApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly AppInstallerService $apps,
        private readonly AppCatalogService $catalog,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    public function catalog(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $out = [];
            foreach ($this->catalog->forActor($this->user($r)) as $c) {
                $out[] = [
                    'category' => (string) $c['slug'], 'name' => (string) $c['name'],
                    'apps' => array_values(array_map(static fn (array $a): array => [
                        'slug' => (string) $a['slug'], 'name' => (string) $a['name'], 'description' => (string) $a['description'], 'needs_database' => (int) $a['needs_db'] === 1,
                    ], (array) $c['apps'])),
                ];
            }
            return self::ok(['installer_enabled' => $this->catalog->installerEnabled(), 'categories' => $out]);
        });
    }

    public function listInstalls(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            try {
                $this->apps->refresh($s);
            } catch (AgentException) {
                // The stored state is still correct; it is refreshed on the next read.
            }
            return self::ok(array_map(static fn (array $i): array => [
                'id' => (int) $i['id'], 'app' => (string) $i['app'], 'path' => (string) $i['path'], 'status' => (string) $i['status'],
                'version' => (string) ($i['version'] ?? ''), 'error' => (string) $i['error'], 'admin_user' => (string) $i['admin_user'],
                'database' => $i['database_name'] === null ? null : (string) $i['database_name'], 'job_id' => (string) $i['job_id'],
                'created_at' => $i['created_at'], 'finished_at' => $i['finished_at'],
            ], $this->apps->history($s)));
        });
    }

    public function plan(Request $r, string $id, string $app): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(fn (): Response => self::ok($this->apps->plan($s, $app, $this->user($r))));
    }

    public function install(Request $r, string $id, string $app): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $app): Response {
            $in = self::body($r);
            // The application may go on this website or one of its subdomains.
            $target = $s;
            $targetId = (int) ($in['target_id'] ?? 0);
            if ($targetId > 0 && $targetId !== (int) $s['id']) {
                $target = $this->site($r, (string) $targetId);
                if ($target === null || (int) ($target['parent_id'] ?? 0) !== (int) $s['id']) {
                    throw new UserFacingException('Choose this website or one of its subdomains.');
                }
            }
            $res = $this->apps->install($target, $app, $in, $this->user($r));
            $this->jobs->track($res['job'], 'Install ' . (AppInstallerService::APPS[$app]['title'] ?? $app), 'website', (string) $target['id'], (int) $this->user($r)['id']);
            $out = ['job_id' => $res['job'], 'website_id' => (int) $target['id']];
            if (isset($res['admin_password'])) {
                $out += ['admin_user' => $res['admin_user'], 'admin_password' => $res['admin_password']];
            }
            return self::ok($out, 202);
        });
    }
}
