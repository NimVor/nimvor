<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\RedirectsService;
use NimVor\Services\ServerContext;
use NimVor\Services\WebsiteService;
use NimVor\Support\ValidationException;

/**
 * REST API v1: a website's subdomains, addon domains and redirects. Every endpoint calls WebsiteService or
 * RedirectsService, as the web interface does: names are validated, plan limits apply, the web server
 * configuration is checked before it is reloaded and a refused change is undone. A subdomain or addon
 * domain is a website of its own; to remove one use `DELETE /websites/{id}` with its id.
 */
final class DomainsApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly WebsiteService $websites,
        private readonly RedirectsService $redirects,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    /**
     * @param array<string, mixed> $c
     * @return array<string, mixed>
     */
    private static function childOut(array $c): array
    {
        return [
            'id' => (int) $c['id'], 'domain' => (string) $c['domain'], 'aliases' => array_values((array) $c['aliases']), 'kind' => (string) $c['kind'],
            'document_root' => (string) $c['document_root'], 'https' => (bool) $c['https'], 'enabled' => (bool) $c['enabled'], 'created_at' => $c['created_at'],
        ];
    }

    /**
     * @param array<string, mixed> $parent
     * @return list<array<string, mixed>>
     */
    private function children(array $parent, bool $sub): array
    {
        $suffix = '.' . $parent['domain'];
        $rows = array_filter($this->sites->children($this->server->id(), (int) $parent['id']), static fn (array $c): bool => str_ends_with((string) $c['domain'], $suffix) === $sub);
        return array_values(array_map([self::class, 'childOut'], $rows));
    }

    public function listSubdomains(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : self::ok($this->children($s, true));
    }

    public function listAddons(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : self::ok($this->children($s, false));
    }

    public function createSubdomain(Request $r, string $id): Response
    {
        return $this->createChild($r, $id, 'subdomain');
    }

    public function createAddon(Request $r, string $id): Response
    {
        return $this->createChild($r, $id, 'addon');
    }

    private function createChild(Request $r, string $id, string $type): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $type): Response {
            $in = self::body($r);
            $res = $this->websites->createChild($s, $type, [
                'subdomain' => (string) ($in['subdomain'] ?? ''), 'domain' => (string) ($in['domain'] ?? ''),
                'dns' => filter_var($in['dns'] ?? false, FILTER_VALIDATE_BOOL), 'www' => filter_var($in['www'] ?? false, FILTER_VALIDATE_BOOL),
            ], $this->user($r));
            $child = (array) $this->sites->find($this->server->id(), $res['id'], null);
            return self::ok(self::childOut($child) + ['dns_record_added' => $res['dns']], 201);
        });
    }

    public function listRedirects(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        return $s === null ? self::notFound() : self::ok($this->redirects->list($s));
    }

    public function addRedirect(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $in = self::body($r);
            $this->redirects->add($s, ['from' => (string) ($in['from'] ?? ''), 'to' => (string) ($in['to'] ?? ''), 'code' => $in['code'] ?? 301]);
            $from = '/' . ltrim(trim((string) ($in['from'] ?? '')), '/');
            foreach ($this->redirects->list((array) $this->sites->find($this->server->id(), (int) $s['id'], null)) as $x) {
                if ($x['from'] === $from) {
                    return self::ok($x, 201);
                }
            }
            return self::ok(['from' => $from], 201);
        });
    }

    public function deleteRedirect(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $from = trim((string) ($r->query('from') ?? self::body($r)['from'] ?? ''));
            if ($from === '') {
                throw new ValidationException(['from' => 'Name the path to stop redirecting, as the list shows it, for example /old-page.']);
            }
            foreach ($this->redirects->list($s) as $x) {
                if ($x['from'] === $from) {
                    $this->redirects->remove($s, $from);
                    return self::ok(['deleted' => $from]);
                }
            }
            return self::notFound();
        });
    }
}
