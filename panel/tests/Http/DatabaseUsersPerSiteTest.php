<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** A website's database page lists the database users of that website, not those of the customer's other websites. */
final class DatabaseUsersPerSiteTest extends HttpTestCase
{
    private int $siteA;
    private int $siteB;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $this->seedUser('mallory', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->siteA = $sites->create(['domain' => 'a-shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $users->find($cust));
        $this->siteB = $sites->create(['domain' => 'b-blog.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $users->find($cust));
        foreach (['db.create', 'db.user_create', 'db.grant', 'db.list', 'db.status'] as $a) {
            $this->agent->succeed($a, ['ok' => true, 'databases' => [], 'users' => []]);
        }
    }

    public function testEachWebsiteListsOnlyItsOwnUsers(): void
    {
        $this->login('cust');
        $this->post('/databases', ['name' => 'shopdb', 'website_id' => (string) $this->siteA, 'create_user' => '1', 'user_name' => 'shopuser']);
        $this->post('/database-users', ['username' => 'astandalone', 'host_choice' => 'localhost', 'website_id' => (string) $this->siteA, 'return_to' => '/sites/' . $this->siteA . '/databases']);
        $this->post('/database-users', ['username' => 'bstandalone', 'host_choice' => 'localhost', 'website_id' => (string) $this->siteB, 'return_to' => '/sites/' . $this->siteB . '/databases']);

        $this->request('GET', '/databases'); // shows (and so clears) the one-time password of the user just created
        $a = $this->request('GET', '/sites/' . $this->siteA . '/databases')->body();
        $b = $this->request('GET', '/sites/' . $this->siteB . '/databases')->body();
        self::assertStringContainsString('astandalone', $a);
        self::assertStringNotContainsString('bstandalone', $a, "website B's user shows on website A");
        self::assertStringContainsString('bstandalone', $b);
        self::assertStringNotContainsString('astandalone', $b, "website A's user shows on website B");

        $all = $this->request('GET', '/databases')->body();
        self::assertStringContainsString('astandalone', $all, "the customer's own overview lists every user");
        self::assertStringContainsString('bstandalone', $all);
    }

    public function testAnotherCustomerSeesNone(): void
    {
        $this->login('cust');
        $this->post('/database-users', ['username' => 'secretuser', 'host_choice' => 'localhost', 'website_id' => (string) $this->siteA, 'return_to' => '/databases']);
        $this->switchTo('mallory');
        self::assertStringNotContainsString('secretuser', $this->request('GET', '/databases')->body());
        self::assertSame(404, $this->request('GET', '/sites/' . $this->siteA . '/databases')->status());
    }

    private function switchTo(string $user): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }
}
