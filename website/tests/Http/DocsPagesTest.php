<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class DocsPagesTest extends SiteTestCase
{
    private const TREE = 'https://api.github.com/repos/NimVor/nimvor/git/trees/main';
    private const RAW = 'https://raw.githubusercontent.com/NimVor/nimvor/main/';

    private function github(): void
    {
        $files = [
            'docs/README.md' => "# NimVor documentation\n\n| Section | Contents |\n|---|---|\n| [Installation](installation/README.md) | Install it |\n| [Security](security/README.md) | Stay safe |\n",
            'docs/installation/README.md' => "# Installation\n\nRun `install.sh`. See [security](../security/README.md#threats) and [the script](../../installer/install.sh).\n\n## Requirements\n\n- Ubuntu\n\n### Network ports\n\n```bash\ncurl -fsSL https://nimvor.com/install | sudo bash\n```\n",
            'docs/security/README.md' => "# Security\n\n## Threats\n\nNone worth mentioning. <script>alert(1)</script>\n",
            'docs/security/installer-review.md' => "# Installer review\n\nFindings.\n",
            'docs/release/1.0.0-ga-report.md' => "# Internal\n",
            'SECURITY.md' => "# Security Policy\n\nReport it privately.\n",
            'README.md' => "# Repo readme\n",
        ];
        $tree = [];
        foreach ($files as $path => $body) {
            $sha = sha1($path . $body);
            $tree[] = ['path' => $path, 'type' => 'blob', 'sha' => $sha];
            $this->http->on(self::RAW . $path, 200, $body);
        }
        $tree[] = ['path' => 'docs', 'type' => 'tree', 'sha' => 'x'];
        $this->http->on(self::TREE, 200, ['tree' => $tree]);
    }

    public function testFirstVisitFetchesTheDocumentationAndShowsItInTheSiteDesign(): void
    {
        $this->github();
        $res = $this->get('/docs/installation');
        self::assertSame(200, $res->status);
        self::assertStringContainsString('<h1 id="installation">Installation</h1>', $res->body);
        self::assertStringContainsString('class="doc-side"', $res->body, 'sidebar');
        self::assertStringContainsString('aria-current="page">Installation</a>', $res->body);
        self::assertStringContainsString('On this page', $res->body);
        self::assertStringContainsString('href="#requirements"', $res->body, 'table of contents');
        self::assertStringContainsString('href="#network-ports"', $res->body);
        self::assertStringContainsString('<pre class="codeblock"><code class="language-bash">', $res->body);
        self::assertStringContainsString('Edit this page on GitHub', $res->body);
        self::assertStringContainsString('https://github.com/NimVor/nimvor/edit/main/docs/installation/README.md', $res->body);
        self::assertSame(5, $this->rows('docs_pages'), 'the release report and the repository readme are not published');
        self::assertStringNotContainsString('<script>alert', $res->body);
    }

    public function testLinksStayInsideTheSiteWhereThePageExists(): void
    {
        $this->github();
        $body = $this->get('/docs/installation')->body;
        self::assertStringContainsString('<a href="/docs/security#threats">security</a>', $body);
        self::assertStringContainsString('href="https://github.com/NimVor/nimvor/blob/main/installer/install.sh"', $body, 'a file that is not documentation goes to GitHub');
        $index = $this->get('/docs')->body;
        self::assertStringContainsString('<a href="/docs/installation">Installation</a>', $index);
        self::assertStringContainsString('<a href="/docs/security">Security</a>', $index);
    }

    public function testSidebarGroupsPreviousAndNext(): void
    {
        $this->github();
        $body = $this->get('/docs/installation')->body;
        self::assertStringContainsString('Getting started', $body);
        self::assertStringContainsString('How it works', $body);
        self::assertStringContainsString('<a class="sub" href="/docs/security/installer-review"', $body);
        self::assertMatchesRegularExpression('#class="doc-pager".*<small>Previous</small>Overview.*<small>Next</small>#s', $body);
        self::assertSame(200, $this->get('/docs/security/installer-review')->status);
        self::assertSame(200, $this->get('/docs/security-policy')->status);
    }

    public function testUnknownPagesAreNotFound(): void
    {
        $this->github();
        $this->get('/docs');
        self::assertSame(404, $this->get('/docs/nope')->status);
        self::assertSame(404, $this->get('/docs/release/1.0.0-ga-report')->status);
        self::assertSame(404, $this->get('/docs/a/b/c')->status);
    }

    public function testSearchFindsTextAndEscapesTheQuery(): void
    {
        $this->github();
        $this->get('/docs');
        $res = $this->get('/docs/search?q=' . rawurlencode('Network ports'));
        self::assertSame(200, $res->status);
        self::assertStringContainsString('1 page match', $res->body);
        self::assertStringContainsString('href="/docs/installation"', $res->body);
        $none = $this->get('/docs/search?q=' . rawurlencode('<script>zzz'))->body;
        self::assertStringContainsString('Nothing in the documentation matches', $none);
        self::assertStringNotContainsString('<script>zzz', $none);
        self::assertStringContainsString('Type at least two characters', $this->get('/docs/search?q=a')->body);
    }

    public function testWhenGitHubIsDownAndNothingIsStoredTheVisitorGetsAnExplanation(): void
    {
        $this->http->down = true;
        $res = $this->get('/docs');
        self::assertSame(503, $res->status);
        self::assertStringContainsString('could not fetch the documentation', $res->body);
        $this->get('/docs');
        self::assertSame(1, $this->http->asked('git/trees'), 'a failing GitHub is not asked again for a couple of minutes');
    }

    public function testStoredPagesKeepWorkingWhenGitHubIsDown(): void
    {
        $this->github();
        $this->get('/docs');
        $this->app->db->run("UPDATE site_state SET updated_at = DATE_SUB(UTC_TIMESTAMP(), INTERVAL 2 DAY) WHERE k = 'docs:synced'");
        $this->http->down = true;
        $res = $this->get('/docs/installation');
        $this->app->finish();
        self::assertSame(200, $res->status);
        self::assertStringContainsString('Requirements', $res->body);
    }

    public function testChangedFilesAreFetchedAgainAndRemovedOnesDisappear(): void
    {
        $this->github();
        $this->get('/docs');
        $before = $this->http->asked('raw.githubusercontent.com');
        $this->app->docsService->sync();
        self::assertSame($before, $this->http->asked('raw.githubusercontent.com'), 'unchanged files are not downloaded again');

        $this->http->on(self::TREE, 200, ['tree' => [
            ['path' => 'docs/README.md', 'type' => 'blob', 'sha' => sha1('docs/README.md' . "# NimVor documentation\n\n| Section | Contents |\n|---|---|\n| [Installation](installation/README.md) | Install it |\n| [Security](security/README.md) | Stay safe |\n")],
            ['path' => 'docs/installation/README.md', 'type' => 'blob', 'sha' => 'changed'],
        ]]);
        $this->http->on(self::RAW . 'docs/installation/README.md', 200, "# Installation v2\n");
        $r = $this->app->docsService->sync();
        self::assertSame(['changed' => 1, 'removed' => 3, 'total' => 2], $r);
        self::assertStringContainsString('Installation v2', $this->get('/docs/installation')->body);
        self::assertSame(404, $this->get('/docs/security')->status);
    }

    public function testOnlyAnAdministratorCanSyncNow(): void
    {
        $this->github();
        self::assertSame(303, $this->post('/admin/content/docs-sync')->status);
        self::assertSame(0, $this->rows('docs_pages'), 'a visitor cannot sync');
        $this->loginAs('client', 'c@example.test');
        self::assertSame(403, $this->post('/admin/content/docs-sync')->status);
        $this->post('/logout');
        $this->loginAs('admin', 'a@example.test');
        $res = $this->post('/admin/content/docs-sync');
        self::assertSame('/admin/content', $res->headers['Location']);
        self::assertSame(5, $this->rows('docs_pages'));
        self::assertStringContainsString('Documentation synced from GitHub', $this->get('/admin/content')->body);
    }

    public function testSitemapListsTheDocumentation(): void
    {
        $this->github();
        $this->get('/docs');
        $xml = $this->get('/sitemap.xml')->body;
        self::assertStringContainsString('/docs/installation', $xml);
        self::assertStringContainsString('/docs/security/installer-review', $xml);
    }
}
