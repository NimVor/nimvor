<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

/** Menus, footer links set by the administrator, blog covers and reviews. */
final class SiteLinksReviewsTest extends SiteTestCase
{
    /** The part of the page between two markers (the header menu, the footer). */
    private function between(string $html, string $from, string $to): string
    {
        $a = strpos($html, $from);
        $b = strpos($html, $to, (int) $a);
        self::assertNotFalse($a, $from);
        self::assertNotFalse($b, $to);
        return substr($html, $a, $b - $a);
    }

    private function addPost(string $slug = 'a-guide', string $image = '', string $body = 'Intro text.'): int
    {
        $this->app->db->run("INSERT INTO posts (slug, title, excerpt, image, body, status, published_at, created_at, updated_at) VALUES (?, ?, 'Summary', ?, ?, 'published', '2026-01-01 09:00:00', UTC_TIMESTAMP(), UTC_TIMESTAMP())", [$slug, 'A guide', $image, $body]);
        $this->app->cache->delete('blog:list');
        return $this->app->db->lastId();
    }

    public function testAboutUsIsAMenuOfItsOwnAtTheEndAndSponsorsAndCommunityAreUnderSupport(): void
    {
        $html = $this->get('/')->body;
        $nav = $this->between($html, '<nav class="nav"', '</nav>');
        $resources = $this->between($nav, 'Resources', 'Support <');
        $support = substr($nav, (int) strpos($nav, 'Support <'));
        foreach (['/resources/about', '/resources/sponsors', '/resources/community'] as $gone) {
            self::assertStringNotContainsString('href="' . $gone . '"', $resources, "$gone left the Resources menu");
        }
        self::assertStringContainsString('href="/reviews"', $resources);
        self::assertStringContainsString('href="/resources/sponsors"', $support);
        self::assertStringContainsString('href="/resources/community"', $support);
        self::assertGreaterThan(strpos($nav, 'Support <'), strpos($nav, 'href="/resources/about"'), 'About us comes after the Support menu');
        self::assertSame(1, substr_count($nav, 'href="/resources/about"'));
        self::assertStringEndsWith('About us</a>', trim($nav), 'About us is the last item');
    }

    public function testTheFooterShowsOnlyTheSocialIconsThatAreSetAndTheTrustpilotLink(): void
    {
        $html = $this->get('/')->body;
        self::assertStringNotContainsString('aria-label="NimVor on social media"', $html, 'no icons until the administrator sets one');
        self::assertStringContainsString('href="https://www.trustpilot.com/review/nimvor.com"', $html);
        self::assertStringContainsString('Review us on Trustpilot', $html);

        $this->loginAs('admin', 'boss@example.test');
        $res = $this->post('/admin/settings', ['social_youtube' => 'https://www.youtube.com/@nimvor', 'social_x' => 'https://x.com/nimvor', 'social_facebook' => '', 'social_reddit' => 'https://www.reddit.com/r/nimvor', 'trustpilot' => 'https://www.trustpilot.com/review/example.org']);
        self::assertSame([303, '/admin/settings'], [$res->status, $res->headers['Location']]);
        $html = $this->get('/')->body;
        $icons = $this->between($html, 'aria-label="NimVor on social media"', '</ul>');
        foreach (['https://www.youtube.com/@nimvor', 'https://x.com/nimvor', 'https://www.reddit.com/r/nimvor'] as $url) {
            self::assertStringContainsString('href="' . $url . '"', $icons);
        }
        self::assertStringNotContainsString('Facebook', $icons, 'an empty address shows no icon');
        self::assertStringContainsString('rel="noopener noreferrer me"', $icons);
        self::assertStringContainsString('href="https://www.trustpilot.com/review/example.org"', $html);
    }

    public function testSettingsRefuseAddressesThatAreNotThePlatformsAndAreForAdministrators(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        foreach ([['social_youtube', 'https://evil.example/@nimvor'], ['social_facebook', 'http://facebook.com/nimvor'], ['social_x', 'javascript:alert(1)'], ['social_reddit', 'https://reddit.com.evil.example/r/x'], ['trustpilot', 'https://example.org/review']] as [$field, $value]) {
            $res = $this->post('/admin/settings', [$field => $value]);
            self::assertSame(422, $res->status, $field . ' ' . $value);
        }
        self::assertNull($this->row("SELECT v FROM site_state WHERE k = 'site:social_youtube'"));
        $this->cookies = [];
        self::assertContains($this->get('/admin/settings')->status, [302, 303, 403]);
        $this->loginAs('client', 'client@example.test');
        self::assertSame(403, $this->get('/admin/settings')->status);
        self::assertSame(403, $this->post('/admin/settings', ['social_x' => 'https://x.com/x'])->status);
    }

    public function testEveryPostHasACoverOnTheBlogAndOnItsOwnPage(): void
    {
        $this->addPost('own', '/assets/blog/own/cover.jpg', 'Text with ![shot](/assets/blog/own/01.jpg) inside.');
        $this->addPost('first-picture', '', "Intro.\n\n![The overview](/assets/blog/first-picture/01.jpg)\n\n![Other](/assets/blog/first-picture/02.jpg)");
        $this->addPost('drawn', '', 'Only words.');

        $list = $this->get('/resources/blog')->body;
        self::assertStringContainsString('src="/assets/blog/own/cover.jpg"', $list, 'the chosen image wins');
        self::assertStringContainsString('src="/assets/blog/first-picture/01.jpg"', $list, 'else the first picture in the text');
        self::assertStringNotContainsString('src="/assets/blog/first-picture/02.jpg"', $list);
        self::assertStringContainsString('src="/resources/blog/drawn/cover.svg"', $list, 'else a drawn cover');

        $page = $this->get('/resources/blog/drawn')->body;
        self::assertStringContainsString('class="post-hero"', $page);
        self::assertStringContainsString('src="/resources/blog/drawn/cover.svg"', $page);
        self::assertStringContainsString('src="/assets/blog/own/cover.jpg"', $this->get('/resources/blog/own')->body);

        $svg = $this->get('/resources/blog/drawn/cover.svg');
        self::assertSame(200, $svg->status);
        self::assertStringContainsString('image/svg+xml', (string) ($svg->headers['Content-Type'] ?? ''));
        self::assertStringStartsWith('<svg', $svg->body);
        self::assertSame($svg->body, $this->get('/resources/blog/drawn/cover.svg')->body, 'the same cover every time');
        self::assertSame(404, $this->get('/resources/blog/nothing-here/cover.svg')->status);
    }

    public function testAdminCanSetACoverAndABadOneIsRefused(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        $ok = $this->post('/admin/posts/new', ['title' => 'With cover', 'slug' => 'with-cover', 'excerpt' => 'x', 'body' => 'A body that is long enough.', 'status' => 'published', 'image' => 'https://cdn.example.org/a.jpg']);
        self::assertSame(303, $ok->status);
        self::assertSame('https://cdn.example.org/a.jpg', $this->row("SELECT image FROM posts WHERE slug = 'with-cover'")['image']);
        foreach (['javascript:alert(1)', '//evil.example/x.jpg', 'http://insecure.example/x.jpg', '/a b.jpg'] as $bad) {
            self::assertSame(422, $this->post('/admin/posts/new', ['title' => 'Bad', 'slug' => 'bad', 'body' => 'A body that is long enough.', 'status' => 'draft', 'image' => $bad])->status, $bad);
        }
    }

    public function testAReviewWaitsForApprovalAndThenShowsOnTheHomePageAndTheReviewsPage(): void
    {
        self::assertStringContainsString('No reviews yet', $this->get('/')->body, 'nothing is invented: an invitation until there is a review');
        $send = $this->post('/reviews', ['name' => 'Ada', 'role' => 'Sysadmin', 'rating' => '5', 'title' => 'Calm and clear', 'body' => 'It changed how we run our servers, and the undo paths are real.']);
        self::assertSame([303, '/reviews'], [$send->status, $send->headers['Location']]);
        self::assertSame('pending', $this->row('SELECT status FROM reviews')['status']);
        self::assertStringNotContainsString('Calm and clear', $this->get('/reviews')->body);
        self::assertStringNotContainsString('Calm and clear', $this->get('/')->body);

        $this->loginAs('admin', 'boss@example.test');
        self::assertStringContainsString('Calm and clear', $this->get('/admin/reviews')->body);
        $id = (int) $this->row('SELECT id FROM reviews')['id'];
        self::assertSame(303, $this->post('/admin/reviews/' . $id . '/status', ['status' => 'published'])->status);

        foreach (['/', '/reviews'] as $path) {
            $html = $this->get($path)->body;
            self::assertStringContainsString('Calm and clear', $html, $path);
            self::assertStringContainsString('aria-label="5 out of 5"', $html, $path);
        }
        self::assertStringContainsString('5.0', $this->get('/reviews')->body);

        $this->post('/admin/reviews/' . $id . '/status', ['status' => 'hidden']);
        self::assertStringNotContainsString('Calm and clear', $this->get('/reviews')->body);
        $this->post('/admin/reviews/' . $id . '/delete');
        self::assertSame(0, $this->rows('reviews'));
        self::assertSame(404, $this->post('/admin/reviews/' . $id . '/status', ['status' => 'published'])->status);
    }

    public function testReviewsAreValidatedAndEscapedAndBotsGetNothing(): void
    {
        foreach ([['rating' => '0'], ['rating' => '6'], ['body' => 'too short'], ['name' => '']] as $i => $bad) {
            $this->ip = '203.0.113.' . (100 + $i); // the per-address limit is not what this checks
            $res = $this->post('/reviews', $bad + ['name' => 'Bob', 'rating' => '4', 'body' => 'A review that is certainly long enough.']);
            self::assertSame(422, $res->status, "case $i");
        }
        self::assertSame(0, $this->rows('reviews'));
        $this->post('/reviews', ['name' => 'Bot', 'rating' => '5', 'body' => 'Spam spam spam spam spam spam.', 'website' => 'http://spam.example']);
        self::assertSame(0, $this->rows('reviews'), 'the hidden field catches bots');

        $this->post('/reviews', ['name' => '<script>alert(1)</script>', 'rating' => '3', 'body' => 'Body with <b>markup</b> that must be shown as text.']);
        $this->loginAs('admin', 'boss@example.test');
        $this->post('/admin/reviews/' . (int) $this->row('SELECT id FROM reviews')['id'] . '/status', ['status' => 'published']);
        $html = $this->get('/reviews')->body;
        self::assertStringNotContainsString('<script>alert(1)</script>', $html);
        self::assertStringContainsString('&lt;script&gt;', $html);
    }

    public function testAdministratorsCanAddAReviewFromTrustpilot(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        $res = $this->post('/admin/reviews', ['name' => 'Grace', 'rating' => '4', 'body' => 'Good panel, still learning it.', 'source' => 'trustpilot', 'url' => 'https://www.trustpilot.com/reviews/abc']);
        self::assertSame(303, $res->status, $res->body);
        $row = $this->row('SELECT * FROM reviews');
        self::assertSame(['published', 'trustpilot'], [$row['status'], $row['source']]);
        $html = $this->get('/reviews')->body;
        self::assertStringContainsString('Trustpilot', $html);
        self::assertStringContainsString('href="https://www.trustpilot.com/reviews/abc"', $html);
        self::assertSame(422, $this->post('/admin/reviews', ['name' => 'X', 'rating' => '4', 'body' => 'Good panel, still learning it.', 'source' => 'trustpilot', 'url' => 'javascript:alert(1)'])->status);
    }

    public function testTheSitemapListsTheReviewsPage(): void
    {
        self::assertStringContainsString('/reviews</loc>', $this->get('/sitemap.xml')->body);
    }
}
