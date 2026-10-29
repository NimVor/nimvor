<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;

/**
 * POST /api/v1/installs: the anonymous report a NimVor server's installer sends.
 * The body is JSON with a random id and the type of system; no address or name is
 * kept (the client's address is only counted for rate limiting, for an hour).
 * Counts are indicative: anyone can send a report.
 */
final class ApiController extends Controller
{
    private const OS = ['ubuntu', 'almalinux', 'rocky', 'rhel', 'debian', 'other'];
    private const ARCH = ['amd64', 'arm64', 'other'];

    public function install(Ctx $ctx): Response
    {
        if ($this->app->limiter->hit('install:ip:' . $ctx->ip(), 30, 3600)) {
            return Response::text("slow down\n", 'text/plain', 429)->header('Retry-After', '3600');
        }
        if (strlen($ctx->req->body) > 4096 || !str_contains((string) ($ctx->req->server['CONTENT_TYPE'] ?? ''), 'json')) {
            return Response::text("expected a small JSON body\n", 'text/plain', 400);
        }
        $d = json_decode($ctx->req->body, true);
        if (!is_array($d)) {
            return Response::text("expected a JSON object\n", 'text/plain', 400);
        }
        $id = is_string($d['id'] ?? null) ? $d['id'] : '';
        $version = is_string($d['version'] ?? null) ? $d['version'] : '';
        $os = is_string($d['os'] ?? null) ? strtolower($d['os']) : 'other';
        $osVersion = is_string($d['os_version'] ?? null) ? $d['os_version'] : '';
        $arch = is_string($d['arch'] ?? null) ? strtolower($d['arch']) : 'other';
        if (preg_match('/^[a-f0-9]{32}$/', $id) !== 1 || preg_match('/^[0-9]{1,3}(\.[0-9]{1,3}){1,2}(-[0-9A-Za-z.]{1,12})?$/', $version) !== 1 || preg_match('/^[0-9]{1,2}(\.[0-9]{1,2})?$/', $osVersion) !== 1) {
            return Response::text("invalid report\n", 'text/plain', 422);
        }
        $this->app->installs->record($id, $version, in_array($os, self::OS, true) ? $os : 'other', $osVersion, in_array($arch, self::ARCH, true) ? $arch : 'other');
        $this->app->cache->delete('stats:public');
        return (new Response(204))->header('Cache-Control', 'no-store');
    }
}
