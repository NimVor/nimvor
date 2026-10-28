<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class PagesTest extends SiteTestCase
{
    /** @return array<string, array{string, string}> */
    public static function pages(): array
    {
        return [
            'home' => ['/', 'checks before it'],
            'features' => ['/features', 'Websites and applications'],
            'resources' => ['/resources', 'Build services'],
            'documentation' => ['/resources/documentation', 'Administrator commands'],
            'blog' => ['/resources/blog', 'Blog'],
            'community' => ['/resources/community', 'Report a problem'],
            'build services' => ['/resources/build-services', 'Send request'],
            'sponsors' => ['/resources/sponsors', 'Become a sponsor'],
            'about' => ['/resources/about', 'The rules we follow'],
            'support' => ['/support', 'Security problem'],
            'contact' => ['/support/contact', 'Send message'],
            'privacy' => ['/support/privacy', 'What we store'],
            'login' => ['/login', 'Log in'],
            'register' => ['/register', 'Create an account'],
        ];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('pages')]
    public function testEveryPublicPageRenders(string $path, string $needle): void
    {
        $res = $this->get($path);
        self::assertSame(200, $res->status, $path);
        self::assertStringContainsString($needle, $res->body, $path);
        self::assertStringContainsString('<title>', $res->body);
        self::assertStringContainsString('rel="canonical"', $res->body);
    }

    public function testTheNavigationHasEveryRequestedItem(): void
    {
        $html = $this->get('/')->body;
        foreach (['Home', 'Features', 'Resources', 'Documentation', 'Blog', 'Community &amp; source', 'Build services', 'Sponsors', 'About us', 'Support', 'Contact us', 'Privacy policy', 'Log in'] as $label) {
            self::assertStringContainsString($label, $html, $label);
        }
    }

    public function testUnknownPagesAndMethodsAreRefused(): void
    {
        self::assertSame(404, $this->get('/nope')->status);
        self::assertSame(404, $this->get('/resources/blog/does-not-exist')->status);
        self::assertSame(405, $this->request('PUT', '/features')->status);
    }

    public function testRobotsSitemapAndHealth(): void
    {
        self::assertStringContainsString('Disallow: /admin', $this->get('/robots.txt')->body);
        $map = $this->get('/sitemap.xml')->body;
        self::assertStringContainsString('<loc>https://nimvor.test/features</loc>', $map);
        self::assertStringNotContainsString('/admin', $map);
        $h = $this->get('/health');
        self::assertSame([200, "ok\n"], [$h->status, $h->body]);
        self::assertSame(301, $this->get('/sponsor')->status);
    }

    public function testPublicPagesAreCacheableAndSetNoCookie(): void
    {
        $res = $this->get('/features');
        self::assertSame('public, max-age=60', $res->headers['Cache-Control']);
        self::assertSame([], $res->cookies());
    }

    public function testPagesWithFormsAreNeverCached(): void
    {
        $res = $this->get('/support/contact');
        self::assertSame('private, no-store', $res->headers['Cache-Control']);
        self::assertCount(1, $res->cookies());
    }

    public function testVisitsAreCountedPerRouteWithoutIdentifiers(): void
    {
        $this->get('/features');
        $this->get('/features');
        $this->get('/resources/blog');
        $this->get('/nope');
        $rows = $this->app->db->all('SELECT route, hits FROM visits ORDER BY route');
        self::assertSame([['route' => '/features', 'hits' => 2], ['route' => '/resources/blog', 'hits' => 1]], array_map(static fn (array $r): array => ['route' => $r['route'], 'hits' => (int) $r['hits']], $rows));
        $cols = array_column($this->app->db->all('SHOW COLUMNS FROM visits'), 'Field');
        self::assertSame(['day', 'route', 'hits'], $cols, 'no address or identifier is stored');
    }

    public function testABlogPostShowsAndDraftsDoNot(): void
    {
        $this->app->posts->save(null, 'hello', 'Hello world', 'A summary', "First paragraph.\n\n## A heading\n\n- one\n- two", 'published', null);
        $this->app->posts->save(null, 'secret', 'Secret draft', 'x', 'Not public text here, long enough.', 'draft', null);
        self::assertStringContainsString('A heading', $this->get('/resources/blog/hello')->body);
        self::assertSame(404, $this->get('/resources/blog/secret')->status);
        self::assertStringNotContainsString('Secret draft', $this->get('/resources/blog')->body);
        self::assertStringNotContainsString('/resources/blog/secret', $this->get('/sitemap.xml')->body);
    }
}
