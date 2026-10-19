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

    public function testBrowserIsConfinedToTheAccountsDatabases(): void
    {
        $this->agent->succeed('mongo.status', ['installed' => true, 'running' => true, 'version' => '8.0.0']);
        $this->agent->succeed('mongo.overview', [
            'databases' => [['name' => $this->prefix . 'shop', 'size' => 100], ['name' => 'otheracct_secret', 'size' => 50]],
            'users' => [],
        ]);
        $this->login('cust');
        $base = '/sites/' . $this->site . '/mongo';

        // The databases table links to the browser; the browser is the
        // full-page tool and lists only the account's own database.
        self::assertStringContainsString('/mongo/browser?db=' . rawurlencode($this->prefix . 'shop'), $this->request('GET', $base)->body());
        $tool = $this->request('GET', $base . '/browser')->body();
        self::assertStringContainsString('id="wcp-mongo"', $tool);
        self::assertStringContainsString($this->prefix . 'shop', $tool);
        self::assertStringNotContainsString('otheracct_secret', $tool, "another account's database is not offered");

        // Browsing the account's own database works.
        $this->agent->succeed('mongo.collections', ['collections' => [['name' => 'orders', 'count' => 2]]]);
        $res = $this->post($base . '/browse', ['op' => 'collections', 'db' => $this->prefix . 'shop']);
        self::assertSame(200, $res->status());
        self::assertSame('orders', ((array) json_decode($res->body(), true))['data']['collections'][0]['name']);

        $this->agent->succeed('mongo.documents', ['documents' => [['_id' => 'a1']], 'total' => 1, 'skip' => 0, 'limit' => 50]);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'documents', 'db' => $this->prefix . 'shop', 'collection' => 'orders'])->status());

        // Insert and update into the account's own database work.
        $this->agent->succeed('mongo.document_insert', ['inserted_id' => 'n1']);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'insert', 'db' => $this->prefix . 'shop', 'collection' => 'orders', 'document' => '{"a":1}'])->status());
        $this->agent->succeed('mongo.document_update', ['matched' => 1, 'modified' => 1]);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'update', 'db' => $this->prefix . 'shop', 'collection' => 'orders', 'id' => '"a1"', 'document' => '{"a":2}'])->status());

        // Indexes on the account's own database work.
        $this->agent->succeed('mongo.indexes', ['indexes' => [['name' => '_id_', 'key' => ['_id' => 1]]]]);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'indexes', 'db' => $this->prefix . 'shop', 'collection' => 'orders'])->status());
        $this->agent->succeed('mongo.index_create', ['name' => 'a_1']);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'index_create', 'db' => $this->prefix . 'shop', 'collection' => 'orders', 'keys' => '{"a":1}'])->status());
        $this->agent->succeed('mongo.index_drop', ['dropped' => 'a_1']);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'index_drop', 'db' => $this->prefix . 'shop', 'collection' => 'orders', 'name' => 'a_1'])->status());

        // A database outside the account's prefix is refused before the agent,
        // for every operation including the new writes and index management.
        $this->agent->succeed('mongo.document_delete', ['deleted' => 1]);
        foreach ([
            ['collections', []], ['documents', ['collection' => 'c']], ['delete', ['collection' => 'c', 'id' => '"a1"']],
            ['insert', ['collection' => 'c', 'document' => '{"a":1}']], ['update', ['collection' => 'c', 'id' => '"a1"', 'document' => '{"a":1}']],
            ['indexes', ['collection' => 'c']], ['index_create', ['collection' => 'c', 'keys' => '{"a":1}']], ['index_drop', ['collection' => 'c', 'name' => 'a_1']],
        ] as [$op, $extra]) {
            $res = $this->post($base . '/browse', ['op' => $op, 'db' => 'otheracct_secret'] + $extra);
            self::assertSame(403, $res->status(), $op . ' on another account must be refused');
        }
        self::assertSame(1, $this->calls('mongo.collections'), 'only the owned-db collections call reached the agent');
        self::assertSame(0, $this->calls('mongo.document_delete'), 'no delete reached the agent for another account');
        self::assertSame(1, $this->calls('mongo.document_insert'), 'only the owned-db insert reached the agent');
        self::assertSame(1, $this->calls('mongo.index_create'), 'only the owned-db index create reached the agent');
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
