<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class ProblemsTest extends SiteTestCase
{
    private const ISSUES = 'https://api.github.com/repos/NimVor/nimvor/issues';

    /** @return array<string, mixed> */
    private function issue(int $n, string $state, array $over = []): array
    {
        return $over + ['number' => $n, 'title' => 'Issue ' . $n, 'state' => $state, 'labels' => [], 'assignees' => [], 'comments' => 0, 'user' => ['login' => 'someone'], 'created_at' => '2026-10-20T10:00:00Z', 'updated_at' => '2026-10-21T10:00:00Z', 'closed_at' => $state === 'closed' ? '2026-10-25T10:00:00Z' : null];
    }

    public function testIssuesAreSortedIntoReportedInProgressAndResolved(): void
    {
        $this->http->on(self::ISSUES, 200, [
            $this->issue(1, 'open'),
            $this->issue(2, 'open', ['assignees' => [['login' => 'dev']]]),
            $this->issue(3, 'open', ['labels' => [['name' => 'In progress']]]),
            $this->issue(4, 'closed'),
            $this->issue(5, 'closed', ['state_reason' => 'not_planned']),
            $this->issue(6, 'open', ['pull_request' => ['url' => 'x']]),
            $this->issue(7, 'open', ['labels' => [['name' => 'bug']], 'comments' => 3, 'title' => '<b>Crash</b> on install']),
        ]);
        $res = $this->get('/resources/problems');
        self::assertSame(200, $res->status);
        $b = $res->body;
        self::assertStringContainsString('Issue 1', $b);
        self::assertStringContainsString('Issue 2', $b);
        self::assertStringContainsString('Issue 4', $b);
        self::assertStringNotContainsString('Issue 6', $b, 'a pull request is not a problem');
        self::assertStringContainsString('&lt;b&gt;Crash&lt;/b&gt; on install', $b, 'titles are escaped');
        self::assertStringContainsString('closed (not planned)', $b);
        self::assertStringContainsString('3 comments', $b);
        self::assertStringContainsString('2 of 6 reported problems are resolved', $b);
        self::assertStringContainsString('https://github.com/NimVor/nimvor/issues/new/choose', $b, 'reporting happens on GitHub');
        $board = $this->app->problems->board();
        self::assertSame([2, 3], array_column($board['progress'], 'number'));
        self::assertSame([1, 7], array_column($board['reported'], 'number'));
        self::assertSame([4, 5], array_column($board['resolved'], 'number'));
    }

    public function testOneColumnCanBeShownInFull(): void
    {
        $rows = [];
        for ($i = 1; $i <= 12; $i++) {
            $rows[] = $this->issue($i, 'open');
        }
        $this->http->on(self::ISSUES, 200, $rows);
        self::assertStringNotContainsString('Issue 12', $this->get('/resources/problems')->body, 'the board shows the first few');
        self::assertStringContainsString('Issue 12', $this->get('/resources/problems?status=reported')->body);
        self::assertSame(200, $this->get('/resources/problems?status=bogus')->status);
    }

    public function testGitHubIsReadAtMostOnceInTenMinutes(): void
    {
        $this->http->on(self::ISSUES, 200, [$this->issue(1, 'open')]);
        $this->get('/resources/problems');
        $this->get('/resources/problems');
        self::assertSame(1, $this->http->asked('/issues'));
    }

    public function testWhenGitHubFailsTheLastGoodListIsShownAsOld(): void
    {
        $this->http->on(self::ISSUES, 200, [$this->issue(1, 'open')]);
        $this->app->problems->board();
        $this->app->cache->delete('problems:fresh');
        $this->http->down = true;
        $b = $this->get('/resources/problems')->body;
        self::assertStringContainsString('Issue 1', $b);
        self::assertStringContainsString('GitHub could not be reached; this is the list as it was', $b);
    }

    public function testWithNothingStoredAndGitHubDownThePageStillWorks(): void
    {
        $this->http->down = true;
        $res = $this->get('/resources/problems');
        self::assertSame(200, $res->status);
        self::assertStringContainsString('GitHub could not be reached, so there is nothing to show yet', $res->body);
        self::assertStringContainsString('Report a problem on GitHub', $res->body);
    }

    public function testCommunityCardsOpenPagesOnThisSite(): void
    {
        $b = $this->get('/resources/community')->body;
        foreach (['/resources/problems', '/docs/contributing', '/docs/code-of-conduct', '/docs/security-policy'] as $href) {
            self::assertStringContainsString('href="' . $href . '"', $b, $href);
        }
        self::assertStringNotContainsString('/blob/main/CONTRIBUTING.md', $b);
    }
}
