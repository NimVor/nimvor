<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;
use NimVorSite\Services\DocsCatalog;
use NimVorSite\Services\GitHubException;
use NimVorSite\Support\Markdown;

/** The documentation, read from the repository on GitHub and shown inside the site. */
final class DocsController extends Controller
{
    /** @param array<string, string> $params */
    public function show(Ctx $ctx, array $params = []): Response
    {
        $slug = isset($params['a']) ? strtolower($params['a'] . (isset($params['b']) ? '/' . $params['b'] : '')) : 'index';
        $this->refresh();
        $page = $this->app->docs->bySlug($slug);
        if ($page === null) {
            return $this->app->docs->count() === 0 ? $ctx->render('docs/unavailable', ['repo' => $this->app->github->repoUrl()], 503) : $this->app->error($ctx, 404);
        }
        if ((int) $page['render_v'] !== Markdown::VERSION) {
            $this->app->docsService->rebuild();
            $page = $this->app->docs->bySlug($slug) ?? $page;
        }
        [$groups, $flat] = $this->menu();
        $at = array_search($slug, array_column($flat, 'slug'), true);
        return $ctx->render('docs/page', [
            'page' => $page,
            'toc' => (array) json_decode((string) $page['toc'], true),
            'groups' => $groups,
            'prev' => $at !== false && $at > 0 ? $flat[$at - 1] : null,
            'next' => $at !== false && $at < count($flat) - 1 ? $flat[$at + 1] : null,
            'slug' => $slug,
            'editUrl' => $this->app->github->repoUrl() . '/edit/' . rawurlencode((string) ($this->app->config['github']['branch'] ?? 'main')) . '/' . implode('/', array_map('rawurlencode', explode('/', (string) $page['source_path']))),
            'syncedAt' => $this->app->docsService->lastSync(),
        ]);
    }

    public function search(Ctx $ctx): Response
    {
        $q = trim($ctx->req->input('q'));
        $results = [];
        if (mb_strlen($q) >= 2 && mb_strlen($q) <= 80) {
            $this->refresh();
            foreach ($this->app->docs->search($q) as $row) {
                $results[] = ['slug' => $row['slug'], 'label' => $row['label'], 'title' => $row['title'], 'group' => $row['grp'], 'snippet' => $this->snippet((string) $row['body'], $q)];
            }
        }
        [$groups] = $this->menu();
        return $ctx->render('docs/search', ['q' => $q, 'results' => $results, 'groups' => $groups, 'slug' => '']);
    }

    /**
     * The sidebar's groups and the pages in reading order.
     *
     * @return array{0: array<string, list<array<string, mixed>>>, 1: list<array<string, mixed>>}
     */
    private function menu(): array
    {
        $by = [];
        foreach ($this->app->docs->menu() as $m) {
            $by[(string) $m['grp']][] = $m + ['url' => DocsCatalog::url((string) $m['slug'])];
        }
        $groups = [];
        foreach (DocsCatalog::GROUPS as $g) {
            if (isset($by[$g])) {
                usort($by[$g], static fn (array $a, array $b): int => [(int) $a['sort_order'], $a['slug']] <=> [(int) $b['sort_order'], $b['slug']]);
                $groups[$g] = $by[$g];
            }
        }
        return [$groups, array_merge(...array_values($groups ?: [[]]))];
    }

    /** First visit: fetch the pages now. Later: refresh them after the answer has gone out. */
    private function refresh(): void
    {
        $docs = $this->app->docsService;
        if (($this->app->config['docs_auto_sync'] ?? true) !== true || !$docs->due() || $this->app->cache->get('docs:backoff') !== null) {
            return;
        }
        $run = function (): void {
            if ($this->app->cache->increment('docs:lock', 300) !== 1) {
                return;
            }
            try {
                $this->app->docsService->sync();
            } catch (GitHubException $e) {
                error_log('nimvor-site docs sync: ' . $e->getMessage());
                $this->app->cache->set('docs:backoff', 1, 120);
            } finally {
                $this->app->cache->delete('docs:lock');
            }
        };
        if ($this->app->docs->count() === 0) {
            $run();
        } else {
            $this->app->after($run);
        }
    }

    private function snippet(string $body, string $q): string
    {
        $plain = preg_replace('/[`*_#>|]+|\[([^\]]*)\]\([^)]*\)/u', '$1 ', $body) ?? $body;
        $plain = preg_replace('/\s+/u', ' ', $plain) ?? $plain;
        $pos = mb_stripos($plain, $q);
        if ($pos === false) {
            return mb_substr($plain, 0, 160);
        }
        $from = max(0, $pos - 70);
        return ($from > 0 ? '…' : '') . mb_substr($plain, $from, 170) . '…';
    }
}
