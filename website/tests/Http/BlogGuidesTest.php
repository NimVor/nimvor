<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Services\BlogImporter;
use NimVorSite\Support\Text;
use NimVorSite\Tests\Support\SiteTestCase;

final class BlogGuidesTest extends SiteTestCase
{
    private string $dir;

    protected function setUp(): void
    {
        parent::setUp();
        $this->dir = sys_get_temp_dir() . '/nimvor-guides-' . bin2hex(random_bytes(4));
        mkdir($this->dir);
    }

    protected function tearDown(): void
    {
        foreach (glob($this->dir . '/*') ?: [] as $f) {
            @unlink($f);
        }
        @rmdir($this->dir);
    }

    private function guide(string $slug, string $title, string $body, string $date = '2026-11-01'): void
    {
        file_put_contents($this->dir . "/{$slug}.md", "---\ntitle: {$title}\nslug: {$slug}\nexcerpt: A guide\ndate: {$date}\n---\n{$body}\n");
    }

    public function testPicturesOnALineOfTheirOwnComeOnlyFromTheSitesBlogFolder(): void
    {
        $ok = Text::html('![The DNS page](/assets/blog/dns/01.jpg)');
        self::assertStringContainsString('<figure class="shot"><img src="/assets/blog/dns/01.jpg" alt="The DNS page"', $ok);
        self::assertStringContainsString('<figcaption>The DNS page</figcaption>', $ok);
        foreach (['![x](https://evil.example/a.jpg)', '![x](/assets/img/logo.png)', '![x](/assets/blog/../../config/config.php)', '![x](/assets/blog/a.svg)'] as $bad) {
            self::assertStringNotContainsString('<img', Text::html($bad), $bad);
        }
        // Quotes in the description cannot break out of the attribute.
        self::assertStringNotContainsString('" onerror="', Text::html('![x" onerror="y](/assets/blog/a/b.jpg)'));
    }

    public function testGuidesAreImportedAndShownWithTheirPictures(): void
    {
        $this->guide('dns-guide', 'DNS in NimVor', "Intro.\n\n## Step 1\n\n![The zone list](/assets/blog/dns/01.jpg)\n\nText.");
        $imp = new BlogImporter($this->app->posts, $this->dir);
        self::assertSame(['added' => 1, 'updated' => 0, 'kept' => 0], $imp->import());
        self::assertSame(['added' => 0, 'updated' => 0, 'kept' => 1], $imp->import(), 'a second import changes nothing');
        $page = $this->get('/resources/blog/dns-guide');
        self::assertSame(200, $page->status);
        self::assertStringContainsString('<img src="/assets/blog/dns/01.jpg"', $page->body);
        self::assertStringContainsString("img-src 'self'", $page->headers['Content-Security-Policy']);
        self::assertStringContainsString('2026-11-01', $page->body);
        self::assertStringContainsString('DNS in NimVor', $this->get('/resources/blog')->body);
    }

    public function testAChangedFileUpdatesTheGuideButAnEditedPostIsKept(): void
    {
        $this->guide('dns-guide', 'DNS in NimVor', 'First version.');
        $imp = new BlogImporter($this->app->posts, $this->dir);
        $imp->import();
        $this->guide('dns-guide', 'DNS in NimVor', 'Second version.');
        self::assertSame(['added' => 0, 'updated' => 1, 'kept' => 0], $imp->import());
        self::assertSame('Second version.', $this->row('SELECT body FROM posts')['body']);

        $this->loginAs('admin', 'boss@example.test');
        $id = (int) $this->row('SELECT id FROM posts')['id'];
        $this->post("/admin/posts/{$id}/edit", ['title' => 'My own title', 'slug' => 'dns-guide', 'excerpt' => 'x', 'body' => 'Edited by hand, in the admin area.', 'status' => 'published']);
        $this->guide('dns-guide', 'DNS in NimVor', 'Third version.');
        self::assertSame(['added' => 0, 'updated' => 0, 'kept' => 1], $imp->import(), 'an administrator\'s edit is not overwritten');
        self::assertSame('Edited by hand, in the admin area.', $this->row('SELECT body FROM posts')['body']);
        self::assertSame(['added' => 0, 'updated' => 1, 'kept' => 0], $imp->import(true), 'unless asked to replace it');
    }

    public function testBadFilesAreSkippedAndTheAdminButtonLoadsTheGuides(): void
    {
        file_put_contents($this->dir . '/no-header.md', 'Just text');
        file_put_contents($this->dir . '/bad-slug.md', "---\ntitle: X\nslug: ../etc\n---\nbody");
        self::assertNull(BlogImporter::parse('Just text'));
        self::assertNull(BlogImporter::parse("---\ntitle: X\nslug: Bad Slug\n---\nbody"));
        self::assertSame(['added' => 0, 'updated' => 0, 'kept' => 0], (new BlogImporter($this->app->posts, $this->dir))->import());
        $this->loginAs('admin', 'boss@example.test');
        $res = $this->post('/admin/posts/import');
        self::assertSame([303, '/admin/posts'], [$res->status, $res->headers['Location']]);
        $this->cookies = [];
        self::assertContains($this->post('/admin/posts/import')->status, [303, 403, 419]);
    }

    public function testEveryShippedGuideIsValidAndItsPicturesExist(): void
    {
        $files = glob(dirname(__DIR__, 2) . '/content/blog/*.md') ?: [];
        self::assertGreaterThanOrEqual(13, count($files));
        $slugs = [];
        foreach ($files as $f) {
            $g = BlogImporter::parse((string) file_get_contents($f));
            self::assertNotNull($g, basename($f));
            self::assertSame(basename($f, '.md'), $g['slug'], 'the file name is the slug');
            self::assertArrayNotHasKey($g['slug'], $slugs);
            $slugs[$g['slug']] = true;
            self::assertMatchesRegularExpression('/\n## Step 1/', $g['body'], $g['slug']);
            preg_match_all('#!\[[^\]]+\]\((/assets/blog/[^)]+)\)#', $g['body'], $m);
            self::assertNotSame([], $m[1], $g['slug'] . ' has no pictures');
            foreach ($m[1] as $img) {
                self::assertFileExists(dirname(__DIR__, 2) . '/public' . $img, $g['slug']);
            }
            self::assertStringContainsString('<img', Text::html($g['body']));
        }
    }
}
