<?php

declare(strict_types=1);

namespace NimVor\Tests\Security;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\CloudflareSiteService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/**
 * Cross-tenant isolation for the per-account customer services (MongoDB and
 * Cloudflare). The generic /sites/{id}/* sweep in TenantIsolationTest proves a
 * customer cannot reach another tenant's site by its id; these tests prove the
 * feature-specific confinement: MongoDB names are pinned to the account's
 * prefix (a crafted name cannot touch another account's database or user), and
 * a Cloudflare token stored for one website never leaks to or is usable by
 * another.
 */
final class CustomerServiceIsolationTest extends HttpTestCase
{
    private Connection $db;
    private int $alice;
    private int $bob;
    private int $aliceSite;
    private int $bobSite;
    private string $alicePrefix;
    private string $bobPrefix;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->alice = $this->seedUser('alice', 'customer');
        $this->bob = $this->seedUser('bob', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->aliceSite = $sites->create(['domain' => 'alice.test', 'kind' => 'static'], (array) $users->find($this->alice));
        $this->bobSite = $sites->create(['domain' => 'victim.test', 'kind' => 'static'], (array) $users->find($this->bob));
        $this->alicePrefix = strtolower((string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$this->aliceSite])) . '_';
        $this->bobPrefix = strtolower((string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$this->bobSite])) . '_';
    }

    public function testMongoOverviewShowsOnlyTheAccountsOwnPrefix(): void
    {
        $this->agent->succeed('mongo.status', ['installed' => true, 'running' => true, 'version' => '8.0.0']);
        // The agent returns every account's databases/users; the panel must
        // filter Alice's page to her prefix and never reveal Bob's.
        $this->agent->succeed('mongo.overview', [
            'databases' => [
                ['name' => $this->alicePrefix . 'shop', 'size' => 100],
                ['name' => $this->bobPrefix . 'secret', 'size' => 50],
            ],
            'users' => [
                ['user' => $this->alicePrefix . 'u', 'db' => $this->alicePrefix . 'shop', 'roles' => ['readWrite']],
                ['user' => $this->bobPrefix . 'u', 'db' => $this->bobPrefix . 'secret', 'roles' => ['readWrite']],
            ],
        ]);
        $this->login('alice');
        $page = $this->request('GET', '/sites/' . $this->aliceSite . '/mongo')->body();
        self::assertStringContainsString($this->alicePrefix . 'shop', $page, 'own database is shown');
        self::assertStringNotContainsString($this->bobPrefix . 'secret', $page, "another account's database must not be shown");
    }

    public function testMongoCannotTouchAnotherAccountsDatabaseOrUser(): void
    {
        $this->agent->succeed('mongo.status', ['installed' => true, 'running' => true, 'version' => '8.0.0']);
        $this->agent->succeed('mongo.overview', ['databases' => [], 'users' => []]);
        $this->agent->succeed('mongo.database_drop', ['dropped' => 'x']);
        $this->agent->succeed('mongo.user_password', ['ok' => true]);
        $this->agent->succeed('mongo.user_drop', ['dropped' => 'x']);
        $this->login('alice');
        $base = '/sites/' . $this->aliceSite . '/mongo';

        // Drop / re-password / drop-user, each naming Bob's prefixed resource,
        // must be refused before the agent is called.
        $this->post($base . '/databases/drop', ['name' => $this->bobPrefix . 'secret']);
        self::assertStringContainsString('does not belong', (string) $this->flash('error'));
        $this->post($base . '/users/password', ['user' => $this->bobPrefix . 'u', 'db' => $this->bobPrefix . 'secret', 'password' => 'a-long-enough-pass-1']);
        self::assertStringContainsString('does not belong', (string) $this->flash('error'));
        $this->post($base . '/users/drop', ['user' => $this->bobPrefix . 'u', 'db' => $this->bobPrefix . 'secret']);
        self::assertStringContainsString('does not belong', (string) $this->flash('error'));

        self::assertSame(0, $this->calls('mongo.database_drop'), 'drop never reached the agent');
        self::assertSame(0, $this->calls('mongo.user_password'), 'password never reached the agent');
        self::assertSame(0, $this->calls('mongo.user_drop'), 'user drop never reached the agent');

        // Even a create that borrows Bob's prefix is pinned under Alice's own
        // prefix, so it cannot collide with or overwrite Bob's namespace.
        $this->agent->succeed('mongo.database_create', ['name' => 'x']);
        $this->post($base . '/databases', ['name' => $this->bobPrefix . 'secret']);
        $created = (string) $this->lastCall('mongo.database_create')['name'];
        self::assertStringStartsWith($this->alicePrefix, $created, 'a created database is pinned to the account prefix');
    }

    public function testCloudflareTokenIsNeverLeakedToAnotherTenant(): void
    {
        $cf = $this->app->container->get(CloudflareSiteService::class);
        $cf->setTransport(static function (string $method, string $url, ?array $body, string $token): array {
            $ok = static fn (mixed $result): array => ['status' => 200, 'body' => ['success' => true, 'result' => $result]];
            return match (true) {
                str_contains($url, '/user/tokens/verify') => $ok(['status' => 'active']),
                str_contains($url, '/zones?per_page=50') => $ok([['id' => str_repeat('a', 32), 'name' => 'victim.test', 'status' => 'active']]),
                default => $ok([]),
            };
        });

        // Bob connects his own token on his own site.
        $secretToken = 'cf-secret-token_ABCDEF0123456789';
        $this->login('bob');
        $this->post('/sites/' . $this->bobSite . '/cloudflare/connect', ['token' => $secretToken]);
        $stored = (string) $this->db->scalar('SELECT token_enc FROM cloudflare_sites WHERE website_id = ?', [$this->bobSite]);
        self::assertNotSame('', $stored, 'the connection was stored');
        self::assertStringNotContainsString($secretToken, $stored, 'the token is encrypted at rest');

        // Alice (a different tenant) must not see Bob's token or zone anywhere.
        $this->login('alice');
        $page = $this->request('GET', '/sites/' . $this->aliceSite . '/cloudflare')->body();
        self::assertStringNotContainsString($secretToken, $page);
        self::assertStringNotContainsString('victim.test', $page, "another tenant's zone must not appear");
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM cloudflare_sites WHERE website_id = ?', [$this->aliceSite]), 'Alice has no connection of her own');

        // Alice's own site is not connected, so an operation on it cannot
        // borrow Bob's token — it is refused, not silently run with his.
        $this->post('/sites/' . $this->aliceSite . '/cloudflare/ssl', ['mode' => 'full']);
        self::assertStringContainsString('not connected', (string) $this->flash('error'));
    }
}
