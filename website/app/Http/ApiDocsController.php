<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;
use NimVorSite\Services\GitHubException;
use NimVorSite\Support\ApiSchema;
use NimVorSite\Support\Markdown;

/** The panel API reference, built from the OpenAPI description in the repository. */
final class ApiDocsController extends Controller
{
    public function overview(Ctx $ctx): Response
    {
        $spec = $this->load();
        if ($spec === null) {
            return $ctx->render('api/unavailable', ['repo' => $this->app->github->repoUrl()], 503);
        }
        $md = (new Markdown())->render((string) ($spec['info']['description'] ?? ''));
        $count = 0;
        foreach ($this->app->apiSpec->tags() as $t) {
            $count += count($t['operations']);
        }
        return $ctx->render('api/overview', [
            'tags' => $this->app->apiSpec->tags(),
            'intro' => $md['html'],
            'version' => (string) ($spec['info']['version'] ?? ''),
            'count' => $count,
            'errors' => $this->errors($spec),
            'current' => '',
            'q' => '',
            'syncedAt' => $this->app->state->get('api:synced'),
        ]);
    }

    /** @param array<string, string> $params */
    public function tag(Ctx $ctx, array $params = []): Response
    {
        $spec = $this->load();
        if ($spec === null) {
            return $ctx->render('api/unavailable', ['repo' => $this->app->github->repoUrl()], 503);
        }
        $slug = strtolower((string) ($params['tag'] ?? ''));
        $tags = $this->app->apiSpec->tags();
        foreach ($tags as $t) {
            if ($t['slug'] === $slug) {
                return $ctx->render('api/tag', ['tag' => $t, 'tags' => $tags, 'schema' => new ApiSchema($spec), 'current' => $slug, 'q' => '', 'markdown' => static fn (string $s): string => (new Markdown())->render($s)['html']]);
            }
        }
        return $this->app->error($ctx, 404);
    }

    public function search(Ctx $ctx): Response
    {
        $spec = $this->load();
        $q = trim($ctx->req->input('q'));
        $hits = [];
        if ($spec !== null && mb_strlen($q) >= 2 && mb_strlen($q) <= 80) {
            $needle = mb_strtolower($q);
            foreach ($this->app->apiSpec->tags() as $t) {
                foreach ($t['operations'] as $op) {
                    $hay = mb_strtolower($op['method'] . ' ' . $op['path'] . ' ' . ($op['summary'] ?? '') . ' ' . ($op['description'] ?? '') . ' ' . ($op['operationId'] ?? '') . ' ' . ($op['x-permission'] ?? ''));
                    if (str_contains($hay, $needle)) {
                        $hits[] = ['tag' => $t, 'op' => $op];
                    }
                }
            }
        }
        return $ctx->render('api/search', ['q' => $q, 'hits' => $hits, 'tags' => $this->app->apiSpec->tags(), 'current' => '']);
    }

    /**
     * The error responses shared by all operations.
     *
     * @param array<string, mixed> $spec
     * @return list<array{name: string, description: string, example: string}>
     */
    private function errors(array $spec): array
    {
        $out = [];
        foreach ((array) ($spec['components']['responses'] ?? []) as $name => $r) {
            $ex = $r['content']['application/json']['example'] ?? null;
            $out[] = ['name' => (string) $name, 'description' => (string) ($r['description'] ?? ''), 'example' => $ex === null ? '' : (string) json_encode($ex, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE)];
        }
        return $out;
    }

    /** @return array<string, mixed>|null the description; fetched now on the first visit, refreshed after the answer later */
    private function load(): ?array
    {
        $svc = $this->app->apiSpec;
        if (($this->app->config['docs_auto_sync'] ?? true) === true && $svc->due() && $this->app->cache->get('apispec:backoff') === null) {
            $run = function (): void {
                if ($this->app->cache->increment('apispec:lock', 300) !== 1) {
                    return;
                }
                try {
                    $this->app->apiSpec->sync();
                    $this->app->state->set('api:synced', gmdate('c'));
                } catch (GitHubException $e) {
                    error_log('nimvor-site api reference sync: ' . $e->getMessage());
                    $this->app->cache->set('apispec:backoff', 1, 120);
                } finally {
                    $this->app->cache->delete('apispec:lock');
                }
            };
            if (!$svc->has()) {
                $run();
            } else {
                $this->app->after($run);
            }
        }
        return $svc->spec();
    }
}
