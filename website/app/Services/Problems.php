<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Cache\Cache;

/**
 * The public problems board: the repository's issues sorted into reported, in
 * progress and resolved. Read from GitHub at most every ten minutes; when GitHub
 * cannot be reached the last good copy is shown, marked as old.
 */
final class Problems
{
    public const FRESH = 600;
    private const KEEP = 14 * 86400;
    /** An open issue with one of these labels, or an assignee, is being worked on. */
    private const PROGRESS_LABELS = '/^(in[ -]?progress|wip|confirmed|accepted|planned|fix(ing| in progress)?|working on it)$/i';

    public function __construct(private readonly GitHub $github, private readonly Cache $cache)
    {
    }

    /**
     * @return array{reported: list<array<string, mixed>>, progress: list<array<string, mixed>>, resolved: list<array<string, mixed>>, fetched_at: ?string, old: bool, error: ?string}
     */
    public function board(): array
    {
        $fresh = $this->cache->get('problems:fresh');
        if (is_array($fresh)) {
            return $fresh;
        }
        try {
            $board = $this->classify($this->github->issues());
            $board['fetched_at'] = gmdate('c');
            $this->cache->set('problems:fresh', $board, self::FRESH);
            $this->cache->set('problems:last', $board, self::KEEP);
            return $board;
        } catch (GitHubException $e) {
            error_log('nimvor-site problems: ' . $e->getMessage());
            $last = $this->cache->get('problems:last');
            if (is_array($last)) {
                $last['old'] = true;
                $last['error'] = $e->getMessage();
                // Do not ask GitHub again for a minute: a failing GitHub should not slow every visit.
                $this->cache->set('problems:fresh', $last, 60);
                return $last;
            }
            $empty = ['reported' => [], 'progress' => [], 'resolved' => [], 'fetched_at' => null, 'old' => true, 'error' => $e->getMessage()];
            $this->cache->set('problems:fresh', $empty, 60);
            return $empty;
        }
    }

    /**
     * @param list<array<string, mixed>> $issues
     * @return array{reported: list<array<string, mixed>>, progress: list<array<string, mixed>>, resolved: list<array<string, mixed>>, fetched_at: ?string, old: bool, error: ?string}
     */
    public function classify(array $issues): array
    {
        $board = ['reported' => [], 'progress' => [], 'resolved' => [], 'fetched_at' => null, 'old' => false, 'error' => null];
        foreach ($issues as $i) {
            if ($i['state'] === 'closed') {
                $board['resolved'][] = $i;
                continue;
            }
            $working = (int) $i['assignees'] > 0;
            foreach ((array) $i['labels'] as $l) {
                $working = $working || preg_match(self::PROGRESS_LABELS, (string) $l) === 1;
            }
            $board[$working ? 'progress' : 'reported'][] = $i;
        }
        usort($board['resolved'], static fn (array $a, array $b): int => strcmp((string) ($b['closed_at'] ?? ''), (string) ($a['closed_at'] ?? '')));
        return $board;
    }
}
