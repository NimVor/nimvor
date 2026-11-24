<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Hosting\Scope;
use NimVor\Http\Middleware\ApiAuthenticate;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\AccessListService;
use NimVor\Support\ValidationException;

/**
 * REST API v1: the server-wide allow/deny list (addresses, browsers and URLs refused or let through for every website on
 * the server), for server administrators. It calls AccessListService with no website, as the web interface does: every
 * entry is validated, the web server configuration is written and checked before it is reloaded, and a refused change is
 * undone. A website's own list is under /websites/{id}/access-list.
 */
final class ServerAccessListApiController
{
    use ApiHelpers;

    public function __construct(private readonly AccessListService $lists)
    {
    }

    private function adminOnly(Request $r): ?Response
    {
        return Scope::isAll($this->user($r)) ? null : ApiAuthenticate::error(403, 'forbidden', 'The server-wide access list is for server administrators.');
    }

    private function uid(Request $r): int
    {
        return (int) ($this->user($r)['id'] ?? 0);
    }

    /** @return array<string, mixed> */
    private function listOut(): array
    {
        $d = $this->lists->read(null);
        return ['status' => (int) $d['status'], 'max' => AccessListService::MAX_GLOBAL, 'rules' => $d['rules']];
    }

    /** @param callable(): Response $fn */
    private function run(Request $r, callable $fn): Response
    {
        return $this->adminOnly($r) ?? $this->guard($fn);
    }

    public function show(Request $r): Response
    {
        return $this->run($r, fn (): Response => self::ok($this->listOut()));
    }

    public function add(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $in = self::body($r);
            $rule = ['kind' => (string) ($in['kind'] ?? ''), 'action' => (string) ($in['action'] ?? ''), 'value' => (string) ($in['value'] ?? ''), 'note' => (string) ($in['note'] ?? '')];
            $this->lists->add(null, $rule, $this->uid($r));
            return self::ok($this->listOut(), 201);
        });
    }

    public function delete(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $kind = (string) ($r->query('kind') ?? self::body($r)['kind'] ?? '');
            $value = (string) ($r->query('value') ?? self::body($r)['value'] ?? '');
            if ($kind === '' || $value === '') {
                throw new ValidationException(['kind' => 'Name the entry by its kind and value, as the list shows them.']);
            }
            $this->lists->remove(null, $kind, $value, $this->uid($r));
            return self::ok($this->listOut());
        });
    }

    public function setStatus(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $this->lists->setStatus(null, (int) (self::body($r)['status'] ?? 0), $this->uid($r));
            return self::ok($this->listOut());
        });
    }

    public function import(Request $r): Response
    {
        return $this->run($r, function () use ($r): Response {
            $in = self::body($r);
            $mode = (string) ($in['mode'] ?? 'merge');
            if (!in_array($mode, ['merge', 'replace'], true)) {
                throw new ValidationException(['mode' => 'Use merge or replace.']);
            }
            $this->lists->import(null, (string) ($in['text'] ?? ''), $mode === 'replace', $this->uid($r));
            return self::ok($this->listOut());
        });
    }

    /** The list as text, one entry per line, in the format `import` reads. */
    public function export(Request $r): Response
    {
        return $this->run($r, fn (): Response => self::ok(['text' => $this->lists->export(null)]));
    }
}
