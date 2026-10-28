<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class AdminTest extends SiteTestCase
{
    private int $admin;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->loginAs('admin', 'boss@example.test');
    }

    /** @return array<string, string> */
    private function post_(array $over = []): array
    {
        return $over + ['title' => 'My first post', 'slug' => '', 'excerpt' => 'Short summary', 'body' => "Hello there.\n\nThis is the body of the post.", 'status' => 'published'];
    }

    public function testCreatingAPublishedPostShowsItOnTheBlogAtOnce(): void
    {
        self::assertStringNotContainsString('My first post', $this->get('/resources/blog')->body); // fills the cache
        $res = $this->post('/admin/posts/new', $this->post_());
        self::assertSame([303, '/admin/posts'], [$res->status, $res->headers['Location']]);
        self::assertStringContainsString('My first post', $this->get('/resources/blog')->body, 'the cached list was refreshed');
        self::assertSame(200, $this->get('/resources/blog/my-first-post')->status);
        self::assertSame($this->admin, (int) $this->row('SELECT author_id FROM posts')['author_id']);
    }

    public function testSlugsAreUniqueAndPostsAreValidated(): void
    {
        $this->post('/admin/posts/new', $this->post_());
        $dup = $this->post('/admin/posts/new', $this->post_());
        self::assertSame(422, $dup->status);
        self::assertStringContainsString('Another post already uses this address', $dup->body);
        self::assertSame(422, $this->post('/admin/posts/new', $this->post_(['title' => 'x', 'status' => 'bogus']))->status);
        self::assertSame(1, $this->rows('posts'));
    }

    public function testEditUnpublishAndDelete(): void
    {
        $this->post('/admin/posts/new', $this->post_());
        $id = (int) $this->row('SELECT id FROM posts')['id'];
        self::assertStringContainsString('My first post', $this->get("/admin/posts/{$id}/edit")->body);
        $this->post("/admin/posts/{$id}/edit", $this->post_(['title' => 'Renamed', 'slug' => 'my-first-post', 'status' => 'draft']));
        self::assertSame('Renamed', $this->row('SELECT title FROM posts')['title']);
        self::assertSame(404, $this->get('/resources/blog/my-first-post')->status, 'a draft is not public');
        $this->post("/admin/posts/{$id}/delete");
        self::assertSame(0, $this->rows('posts'));
        self::assertSame(404, $this->get('/admin/posts/999/edit')->status);
    }

    public function testSponsorsNeedAnHttpsLinkAndShowOnThePublicPage(): void
    {
        self::assertSame(422, $this->post('/admin/sponsors', ['name' => 'Acme', 'url' => 'javascript:alert(1)', 'tier' => 'gold'])->status);
        self::assertSame(422, $this->post('/admin/sponsors', ['name' => 'Acme', 'url' => 'http://acme.test', 'tier' => 'gold'])->status);
        self::assertSame(303, $this->post('/admin/sponsors', ['name' => 'Acme Hosting', 'url' => 'https://acme.test/', 'tier' => 'gold'])->status);
        self::assertStringContainsString('Acme Hosting', $this->get('/resources/sponsors')->body);
        $id = (int) $this->row('SELECT id FROM sponsors')['id'];
        $this->post("/admin/sponsors/{$id}/delete");
        self::assertStringNotContainsString('Acme Hosting', $this->get('/resources/sponsors')->body, 'the cached list was refreshed');
    }

    public function testStatusChangesForMessagesAndRequests(): void
    {
        $m = $this->app->messages->create(null, 'Visitor', 'v@example.test', 'Subject line', 'A message long enough');
        $r = $this->app->requests->create(null, 'Visitor', 'v@example.test', 'custom', 'Build me a plugin that does things');
        $this->post("/admin/messages/{$m}/status", ['status' => 'closed']);
        $this->post("/admin/requests/{$r}/status", ['status' => 'quoted']);
        $this->post("/admin/requests/{$r}/status", ['status' => 'not-a-status']);
        self::assertSame('closed', $this->row('SELECT status FROM contact_messages')['status']);
        self::assertSame('quoted', $this->row('SELECT status FROM build_requests')['status'], 'an unknown status is ignored');
    }

    public function testYouCannotDeactivateYourselfOrTheLastAdministrator(): void
    {
        $this->post("/admin/users/{$this->admin}/active", ['active' => '0']);
        self::assertSame(1, (int) $this->row('SELECT is_active FROM users WHERE id = ?', [$this->admin])['is_active']);
        $other = $this->createUser('client@example.test');
        $this->post("/admin/users/{$other}/active", ['active' => '0']);
        self::assertSame(0, (int) $this->row('SELECT is_active FROM users WHERE id = ?', [$other])['is_active']);
        $this->post("/admin/users/{$other}/active", ['active' => '1']);
        self::assertSame(1, (int) $this->row('SELECT is_active FROM users WHERE id = ?', [$other])['is_active']);
        self::assertSame(404, $this->post('/admin/users/9999/active', ['active' => '0'])->status);
    }

    public function testTheReportsCountWhatIsThere(): void
    {
        $this->createUser('c1@example.test');
        $this->app->messages->create(null, 'V', 'v@example.test', 'Subject line', 'A message long enough');
        $this->app->requests->create(null, 'V', 'v@example.test', 'review', 'Please review our server setup');
        $this->get('/features');
        $this->app->cache->delete('report:overview');
        $r = $this->app->reports->overview();
        self::assertSame(2, $r['totals']['users']);
        self::assertSame(1, $r['totals']['clients']);
        self::assertSame(1, $r['totals']['admins']);
        self::assertSame(1, $r['messages']['new']);
        self::assertSame(1, $r['requests']['new']);
        self::assertSame(30, count($r['signups']));
        self::assertSame(2, end($r['signups']));
        self::assertGreaterThanOrEqual(1, $r['visits'][gmdate('Y-m-d')]);
        $page = $this->get('/admin/reports')->body;
        self::assertStringContainsString('<svg class="chart"', $page);
        self::assertStringContainsString('/features', $page);
    }

    public function testCsvExportsAreProtectedAgainstSpreadsheetFormulas(): void
    {
        $this->createUser('=HYPERLINK("http://evil.test")@example.test', '=cmd|calc');
        $this->app->messages->create(null, '+SUM(A1)', 'v@example.test', '@evil subject', 'A message long enough');
        $users = $this->get('/admin/reports/users.csv');
        self::assertSame('text/csv; charset=UTF-8', $users->headers['Content-Type']);
        self::assertStringContainsString('attachment; filename="nimvor-users-', $users->headers['Content-Disposition']);
        self::assertStringContainsString("'=cmd|calc", $users->body);
        self::assertStringNotContainsString(',=cmd|calc', $users->body);
        self::assertStringContainsString("'+SUM(A1)", $this->get('/admin/reports/messages.csv')->body);
        self::assertSame(404, $this->get('/admin/reports/passwords.csv')->status);
    }
}
