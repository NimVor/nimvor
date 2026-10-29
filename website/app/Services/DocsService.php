<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Repositories\Docs;
use NimVorSite\Repositories\State;
use NimVorSite\Support\Markdown;

/** Keeps the documentation pages in step with the repository on GitHub. */
final class DocsService
{
    public const STATE_KEY = 'docs:synced';
    /** A sync is due after this many seconds. */
    public const MAX_AGE = 6 * 3600;

    public function __construct(private readonly Docs $docs, private readonly State $state, private readonly GitHub $github, private readonly string $branch)
    {
    }

    /** True when there is nothing to show yet or the pages are older than MAX_AGE. */
    public function due(): bool
    {
        $age = $this->state->age(self::STATE_KEY);
        return $age === null || $age > self::MAX_AGE || $this->docs->count() === 0;
    }

    /**
     * Fetches what changed, removes what is gone and renders every page again.
     *
     * @return array{changed: int, removed: int, total: int}
     * @throws GitHubException when GitHub cannot be read; the pages already stored stay as they were
     */
    public function sync(): array
    {
        $have = $this->docs->shas();
        $keep = [];
        $changed = 0;
        foreach ($this->github->markdownFiles() as $f) {
            $entry = DocsCatalog::entry($f['path']);
            if ($entry === null) {
                continue;
            }
            $keep[] = $f['path'];
            if (($have[$f['path']] ?? '') === $f['sha']) {
                continue;
            }
            $this->docs->saveSource($f['path'], $f['sha'], $entry, $this->github->raw($f['path']));
            $changed++;
        }
        if ($keep === []) {
            throw new GitHubException('No published documentation was found in the repository.');
        }
        $removed = $this->docs->deleteExcept($keep);
        $this->rebuild();
        $this->state->set(self::STATE_KEY, gmdate('c'));
        return ['changed' => $changed, 'removed' => $removed, 'total' => count($keep)];
    }

    /** Renders every stored page again (links to pages that came or went are rewritten). */
    public function rebuild(): void
    {
        $slugs = $this->docs->slugs();
        $repo = $this->github->repoUrl();
        $branch = $this->branch;
        foreach ($this->docs->allWithBody() as $row) {
            $from = (string) $row['source_path'];
            $md = new Markdown(static fn (string $t): string => DocsCatalog::resolve($from, $t, $slugs, $repo, $branch));
            $r = $md->render((string) $row['body']);
            $this->docs->saveRendered((int) $row['id'], $r['title'] !== '' ? $r['title'] : basename($from, '.md'), $r['html'], $r['toc'], Markdown::VERSION);
        }
    }

    public function lastSync(): ?string
    {
        return $this->state->get(self::STATE_KEY);
    }
}
