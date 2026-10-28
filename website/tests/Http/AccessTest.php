<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class AccessTest extends SiteTestCase
{
    /** @return array<string, array{string}> */
    public static function protectedPages(): array
    {
        return [['/account'], ['/account/requests'], ['/account/messages'], ['/account/profile'], ['/admin'], ['/admin/reports'], ['/admin/reports/users.csv'], ['/admin/users'], ['/admin/messages'], ['/admin/requests'], ['/admin/posts'], ['/admin/posts/new'], ['/admin/sponsors']];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('protectedPages')]
    public function testAnonymousVisitorsAreSentToTheLoginPage(string $path): void
    {
        $res = $this->get($path);
        self::assertSame(303, $res->status, $path);
        self::assertStringStartsWith('/login?next=', $res->headers['Location']);
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('protectedPages')]
    public function testClientsCannotReachTheAdminArea(string $path): void
    {
        $this->loginAs('client');
        $res = $this->get($path);
        if (str_starts_with($path, '/admin')) {
            self::assertSame(403, $res->status, $path);
        } else {
            self::assertSame(200, $res->status, $path);
        }
    }

    public function testAdminPostsAreForbiddenToClientsToo(): void
    {
        $this->loginAs('client');
        $this->app->posts->save(null, 'p', 'Post title', 'x', 'A body that is long enough to save.', 'published', null);
        $id = (int) $this->app->db->scalar('SELECT id FROM posts');
        foreach (['/admin/posts/new', "/admin/posts/{$id}/edit", "/admin/posts/{$id}/delete", '/admin/sponsors', "/admin/users/{$id}/active"] as $path) {
            self::assertSame(403, $this->post($path, ['title' => 'x'])->status, $path);
        }
        self::assertSame(1, $this->rows('posts'));
    }

    public function testAnAdministratorReachesEverything(): void
    {
        $this->loginAs('admin');
        foreach (['/admin', '/admin/reports', '/admin/users', '/admin/messages', '/admin/requests', '/admin/posts', '/admin/posts/new', '/admin/sponsors', '/account'] as $path) {
            self::assertSame(200, $this->get($path)->status, $path);
        }
    }

    public function testClientsOnlyEverSeeTheirOwnRequestsAndMessages(): void
    {
        $a = $this->createUser('a@example.test', 'Alice');
        $b = $this->createUser('b@example.test', 'Bob');
        $this->app->requests->create($a, 'Alice', 'a@example.test', 'migration', 'Alice private details for the migration work');
        $this->app->requests->create($b, 'Bob', 'b@example.test', 'review', 'Bob private details for the review work here');
        $this->app->messages->create($b, 'Bob', 'b@example.test', 'Bob secret subject', 'Bob private message text');
        $this->login('a@example.test');
        $page = $this->get('/account/requests')->body . $this->get('/account/messages')->body . $this->get('/account')->body;
        self::assertStringContainsString('Alice private details', $page);
        self::assertStringNotContainsString('Bob private', $page);
        self::assertStringNotContainsString('Bob secret subject', $page);
    }
}
