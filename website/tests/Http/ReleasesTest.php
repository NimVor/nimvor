<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Services\Releases;
use NimVorSite\Tests\Support\SiteTestCase;

final class ReleasesTest extends SiteTestCase
{
    private const CHANGELOG_URL = 'https://raw.githubusercontent.com/NimVor/nimvor/main/CHANGELOG.md';
    private const RELEASES_URL = 'https://api.github.com/repos/NimVor/nimvor/releases';

    private const CHANGELOG = <<<'MD'
# Changelog

## [Unreleased]

### Added

- Something on the way.

## [1.2.0] — 2026-11-03

A **big** one: PostgreSQL and `faster` pages. See [the docs](docs/postgres.md).

### Added

- **PostgreSQL** manager with
  a second line of the same change.
- Page cache.
- A <script>alert(1)</script> in a change.

### Fixed

- A crash on [Ubuntu](https://ubuntu.com).

### Security

- A table browser fix.

## [1.1.0] — 2026-10-29

### Changed

- Quieter logs.

```
## [9.9.9] — 2099-01-01
```

## [1.0.0-rc1] — 2026-09-25

### Added

- First candidate.

### Added: hosting services

- Plans.
MD;

    private function feed(): void
    {
        $this->http->on(self::CHANGELOG_URL, 200, self::CHANGELOG);
        $this->http->on(self::RELEASES_URL, 200, [
            ['tag_name' => 'v1.2.0', 'draft' => false, 'prerelease' => false, 'published_at' => '2026-11-03T10:00:00Z', 'assets' => [
                ['name' => 'nimvor-1.2.0-linux-amd64.tar.gz', 'size' => 11850504, 'browser_download_url' => 'https://github.com/NimVor/nimvor/releases/download/v1.2.0/nimvor-1.2.0-linux-amd64.tar.gz'],
                ['name' => 'evil', 'size' => 1, 'browser_download_url' => 'https://evil.example/x'],
            ]],
            ['tag_name' => 'v1.1.0', 'draft' => false, 'prerelease' => false, 'published_at' => '2026-10-29T10:00:00Z', 'assets' => []],
            ['tag_name' => 'v1.3.0', 'draft' => true, 'assets' => []],
        ]);
    }

    public function testTheChangelogIsParsedIntoReleases(): void
    {
        $r = Releases::parse(self::CHANGELOG);
        self::assertSame(['Unreleased', '1.2.0', '1.1.0', '1.0.0-rc1'], array_column($r, 'version'), 'a code fence is not a release');
        $one = $r[1];
        self::assertSame('2026-11-03', $one['date']);
        self::assertStringStartsWith('A **big** one', $one['summary']);
        self::assertSame(['added', 'fixed', 'security'], array_column($one['sections'], 'key'));
        self::assertSame('**PostgreSQL** manager with a second line of the same change.', $one['sections'][0]['items'][0]);
        self::assertSame(5, $one['count']);
        self::assertSame(['added', 'x-added-hosting-services'], array_column($r[3]['sections'], 'key'));
    }

    public function testKindFollowsTheVersionNumber(): void
    {
        self::assertSame('major', Releases::kind('2.0.0'));
        self::assertSame('minor', Releases::kind('1.8.0'));
        self::assertSame('patch', Releases::kind('1.8.1'));
        self::assertSame('pre', Releases::kind('1.0.0-rc1'));
    }

    public function testTheListShowsOneLinePerChangeAndThePageShowsAll(): void
    {
        $this->feed();
        $svc = $this->app->releases;
        self::assertSame('<strong>PostgreSQL</strong> manager.', $svc->inline('**PostgreSQL** manager.'));
        self::assertSame('Traffic report per website (Websites → Traffic report).', $svc->brief('**Traffic report per website (Websites → Traffic report).** Requests per hour, refused requests and more.'));
        self::assertSame('Version 1.2 is out.', $svc->brief('Version 1.2 is out. A lot more text follows in the same change.'));
        self::assertSame('Uses <code>a.b</code> first.', $svc->brief('Uses `a.b` first. Then more.'), 'a dot inside code is not the end of the sentence');
        $long = $svc->brief(str_repeat('word ', 80));
        self::assertLessThanOrEqual(160, mb_strlen(strip_tags($long)));
        self::assertStringEndsWith('…', $long);
        self::assertStringNotContainsString('<script>', $svc->brief('A <script>alert(1)</script> that is long enough to be cut somewhere here.'));
    }

    public function testTheListShowsEveryReleaseWithItsDownloads(): void
    {
        $this->feed();
        $res = $this->get('/resources/releases');
        self::assertSame(200, $res->status);
        $b = $res->body;
        self::assertStringContainsString('Latest release', $b);
        self::assertStringContainsString('1.2.0', $b);
        self::assertStringContainsString('3 November 2026', $b);
        self::assertStringContainsString('<strong>big</strong>', $b, 'the summary keeps its emphasis');
        self::assertStringContainsString('nimvor-1.2.0-linux-amd64.tar.gz', $b);
        self::assertStringContainsString('11.3 MB', $b);
        self::assertStringNotContainsString('evil.example', $b, 'only downloads on github.com are linked');
        self::assertStringContainsString('1.0.0-rc1', $b);
        self::assertStringContainsString('Pre-release', $b);
        self::assertStringContainsString('Coming next', $b);
        self::assertStringContainsString('Something on the way.', $b);
        self::assertStringContainsString('included in the next release', $b, 'a version without a GitHub release says so');
        self::assertStringNotContainsString('9.9.9', $b);
    }

    public function testChangesAreEscaped(): void
    {
        $this->feed();
        $b = $this->get('/resources/releases/1.2.0')->body;
        self::assertStringNotContainsString('<script>alert(1)</script>', $b);
        self::assertStringContainsString('&lt;script&gt;alert(1)&lt;/script&gt;', $b);
    }

    public function testOneReleaseHasItsOwnPage(): void
    {
        $this->feed();
        $res = $this->get('/resources/releases/1.2.0');
        self::assertSame(200, $res->status);
        $b = $res->body;
        self::assertStringContainsString('NimVor 1.2.0', $b);
        self::assertStringContainsString('<strong>PostgreSQL</strong> manager with a second line of the same change.', $b);
        self::assertStringContainsString('A crash on <a href="https://ubuntu.com"', $b);
        self::assertStringContainsString('https://github.com/NimVor/nimvor/blob/main/docs/postgres.md', $b, 'a relative link points into the repository');
        self::assertStringContainsString('--version 1.2.0', $b, 'the install command is for this version');
        self::assertStringContainsString('compare/v1.1.0...v1.2.0', $b);
        self::assertStringContainsString('/resources/releases/1.1.0', $b, 'the older release is one click away');
        self::assertSame(404, $this->get('/resources/releases/9.9.9')->status);
        self::assertSame(200, $this->get('/resources/releases/1.0.0-rc1')->status);
    }

    public function testASeriesCanBeChosen(): void
    {
        $this->feed();
        $b = $this->get('/resources/releases?series=1.1')->body;
        self::assertStringContainsString('id="v1.1.0"', $b);
        self::assertStringNotContainsString('id="v1.2.0"', $b);
    }

    public function testTheLastGoodCopyIsShownWhenGitHubIsDown(): void
    {
        $this->feed();
        $this->get('/resources/releases');
        $this->app->cache->delete('releases:fresh');
        $this->http->down = true;
        $b = $this->get('/resources/releases')->body;
        self::assertStringContainsString('1.2.0', $b);
        self::assertStringContainsString('GitHub could not be reached', $b);
    }

    public function testNothingToShowIsSaidPlainly(): void
    {
        $this->http->down = true;
        $res = $this->get('/resources/releases');
        self::assertSame(200, $res->status);
        self::assertStringContainsString('nothing to show yet', $res->body);
    }

    public function testTheChangelogStillShowsWhenOnlyTheDownloadsFail(): void
    {
        $this->http->on(self::CHANGELOG_URL, 200, self::CHANGELOG);
        $this->http->on(self::RELEASES_URL, 500, 'boom');
        $b = $this->get('/resources/releases')->body;
        self::assertStringContainsString('1.2.0', $b);
        self::assertStringContainsString('included in the next release', $b);
    }

    public function testFeaturesPageAnnouncesTheLatestRelease(): void
    {
        $this->feed();
        $b = $this->get('/features')->body;
        self::assertStringContainsString('New in 1.2.0', $b);
        self::assertStringContainsString('/resources/releases/1.2.0', $b);
        self::assertStringContainsString('Moving to NimVor', $b);
        self::assertStringContainsString('Website protection', $b);
        self::assertStringContainsString('PostgreSQL', $b);
    }

    public function testFeaturesPageWorksWithoutGitHub(): void
    {
        $this->http->down = true;
        $res = $this->get('/features');
        self::assertSame(200, $res->status);
        self::assertStringNotContainsString('New in ', $res->body);
    }

    public function testSitemapListsTheReleases(): void
    {
        $this->feed();
        $x = $this->get('/sitemap.xml')->body;
        self::assertStringContainsString('/resources/releases</loc>', $x);
        self::assertStringContainsString('/resources/releases/1.2.0</loc>', $x);
    }

    public function testTheFooterHasTheCopyrightLineWithTheCurrentYear(): void
    {
        $b = $this->get('/')->body;
        self::assertStringContainsString('<span>&copy; Copyright ' . gmdate('Y') . ' NimVor</span>', $b);
        self::assertStringContainsString('Powered by <a href="https://tplix.dev" target="_blank" rel="noopener noreferrer">TPLix</a> &middot; Hosted on <a href="https://nimvor.com" target="_blank" rel="noopener noreferrer">NimVor</a>', $b);
        self::assertStringContainsString('href="/resources/releases"', $b, 'the footer links to the releases page');
    }
}
