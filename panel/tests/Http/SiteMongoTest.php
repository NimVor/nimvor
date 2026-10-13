<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\FeatureService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Site → MongoDB: a customer's own prefixed databases and users. */
final class SiteMongoTest extends HttpTestCase
{
    private int $site;
    private int $cust;
    private string $prefix;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->site = $this->app->container->get(WebsiteService::class)->create(
            ['domain' => 'shop.test', 'kind' => 'static'],
            (array) $this->app->container->get(UserRepository::class)->find($this->cust),
        );
        $user = strtolower((string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$this->site]));
        $this->prefix = $user . '_';
    }

    public function testOnlyTheAccountsOwnDatabasesAreShownAndCreatable(): void
    {
        $this->agent->succeed('mongo.status', ['installed' => true, 'running' => true, 'version' => '8.0.0']);
        $this->agent->succeed('mongo.overview', [
            'databases' => [['name' => $this->prefix . 'shop', 'size' => 100], ['name' => 'otheracct_secret', 'size' => 50]],
            'users' => [['user' => $this->prefix . 'u', 'db' => $this->prefix . 'shop', 'roles' => ['readWrite']], ['user' => 'otheracct_u', 'db' => 'otheracct_secret', 'roles' => ['read']]],
        ]);
        $this->agent->succeed('mongo.database_create', ['name' => 'x']);
        $this->agent->succeed('mongo.database_drop', ['dropped' => 'x']);
        $this->login('cust');
        $base = '/sites/' . $this->site . '/mongo';

        $page = $this->request('GET', $base)->body();
        self::assertStringContainsString($this->prefix . 'shop', $page, 'own database is shown');
        self::assertStringNotContainsString('otheracct_secret', $page, 'another account\'s database is hidden');

        // Create prepends the account prefix.
        $this->post($base . '/databases', ['name' => 'blog']);
        self::assertSame($this->prefix . 'blog', $this->lastCall('mongo.database_create')['name']);

        // Dropping a database outside the prefix is refused before the agent.
        $this->post($base . '/databases/drop', ['name' => 'otheracct_secret']);
        self::assertSame(0, $this->calls('mongo.database_drop'));
        self::assertNotNull($this->flash('error'));

        // Dropping an own database works.
        $this->post($base . '/databases/drop', ['name' => $this->prefix . 'shop']);
        self::assertSame(1, $this->calls('mongo.database_drop'));
    }

    public function testMongoIsGatedByFeature(): void
    {
        $this->db->run("INSERT INTO plans (name, created_at, updated_at) VALUES ('P', 'x', 'x')");
        $plan = (int) $this->db->lastInsertId();
        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'NoMongo', 'features' => array_values(array_diff(array_keys(FeatureService::FEATURES), ['mongodb']))], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $plan]);
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [$plan, $this->cust]);
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/sites/' . $this->site . '/mongo')->status());
    }
}
