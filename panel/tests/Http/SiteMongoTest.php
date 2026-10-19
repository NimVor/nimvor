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

        // Aggregation on the account's own database works.
        $this->agent->succeed('mongo.aggregate', ['documents' => [], 'ms' => 1, 'limit' => 50]);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'aggregate', 'db' => $this->prefix . 'shop', 'collection' => 'orders', 'pipeline' => '[{"$match":{}}]'])->status());

        // Indexes on the account's own database work.
        $this->agent->succeed('mongo.indexes', ['indexes' => [['name' => '_id_', 'key' => ['_id' => 1]]]]);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'indexes', 'db' => $this->prefix . 'shop', 'collection' => 'orders'])->status());
        $this->agent->succeed('mongo.index_create', ['name' => 'a_1']);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'index_create', 'db' => $this->prefix . 'shop', 'collection' => 'orders', 'keys' => '{"a":1}'])->status());
        $this->agent->succeed('mongo.index_drop', ['dropped' => 'a_1']);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'index_drop', 'db' => $this->prefix . 'shop', 'collection' => 'orders', 'name' => 'a_1'])->status());

        // Schema analysis on the account's own collection works.
        $this->agent->succeed('mongo.schema', ['sampled' => 2, 'fields' => [['field' => '_id', 'total' => 2, 'types' => [['type' => 'objectId', 'count' => 2]]]]]);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'schema', 'db' => $this->prefix . 'shop', 'collection' => 'orders'])->status());

        // Users on the account's own database: list, create (prefixed), change
        // password and drop.
        $this->agent->succeed('mongo.database_users', ['users' => [['user' => $this->prefix . 'u', 'roles' => ['readWrite']]]]);
        $res = $this->post($base . '/browse', ['op' => 'users', 'db' => $this->prefix . 'shop']);
        self::assertSame($this->prefix . 'u', ((array) json_decode($res->body(), true))['data']['users'][0]['user']);
        $this->agent->succeed('mongo.user_create', ['user' => $this->prefix . 'app', 'db' => $this->prefix . 'shop']);
        $res = $this->post($base . '/browse', ['op' => 'user_create', 'db' => $this->prefix . 'shop', 'user' => 'app', 'password' => 'longenough1', 'role' => 'readWrite']);
        self::assertSame(200, $res->status());
        self::assertSame($this->prefix . 'app', $this->lastCall('mongo.user_create')['user'], 'the created user name is prefixed');
        $this->agent->succeed('mongo.user_password', ['user' => $this->prefix . 'u', 'db' => $this->prefix . 'shop']);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'user_password', 'db' => $this->prefix . 'shop', 'user' => $this->prefix . 'u', 'password' => 'longenough2'])->status());
        $this->agent->succeed('mongo.user_drop', ['dropped' => $this->prefix . 'u', 'db' => $this->prefix . 'shop']);
        self::assertSame(200, $this->post($base . '/browse', ['op' => 'user_drop', 'db' => $this->prefix . 'shop', 'user' => $this->prefix . 'u'])->status());

        // Importing into the account's own collection works.
        $this->agent->succeed('mongo.import', ['inserted' => 2, 'errors' => 0, 'total' => 2]);
        $res = $this->post($base . '/browse', ['op' => 'import', 'db' => $this->prefix . 'shop', 'collection' => 'orders', 'format' => 'json', 'content' => '[{"a":1},{"a":2}]']);
        self::assertSame(200, $res->status());
        self::assertSame(2, ((array) json_decode($res->body(), true))['data']['inserted']);

        // Exporting the account's own collection downloads an attachment.
        $this->agent->succeed('mongo.export', ['count' => 1, 'content' => '[{"_id":"a"}]', 'format' => 'json']);
        $res = $this->request('GET', $base . '/export', [], [], '', ['db' => $this->prefix . 'shop', 'collection' => 'orders', 'format' => 'json']);
        self::assertSame(200, $res->status());
        self::assertStringContainsString('attachment;', (string) $res->header('Content-Disposition'));
        self::assertSame($this->prefix . 'shop', $this->lastCall('mongo.export')['db']);

        // Exporting a database outside the account's prefix never reaches the
        // agent: it is refused (a redirect, not a download).
        $res = $this->request('GET', $base . '/export', [], [], '', ['db' => 'otheracct_secret', 'collection' => 'c', 'format' => 'json']);
        self::assertSame(303, $res->status(), 'export of another account must be refused');
        self::assertNull($res->header('Content-Disposition'));
        self::assertSame(1, $this->calls('mongo.export'), 'only the owned-db export reached the agent');

        // A database outside the account's prefix is refused before the agent,
        // for every operation including the new writes and index management.
        $this->agent->succeed('mongo.document_delete', ['deleted' => 1]);
        foreach ([
            ['collections', []], ['documents', ['collection' => 'c']], ['delete', ['collection' => 'c', 'id' => '"a1"']],
            ['insert', ['collection' => 'c', 'document' => '{"a":1}']], ['update', ['collection' => 'c', 'id' => '"a1"', 'document' => '{"a":1}']],
            ['indexes', ['collection' => 'c']], ['index_create', ['collection' => 'c', 'keys' => '{"a":1}']], ['index_drop', ['collection' => 'c', 'name' => 'a_1']],
            ['aggregate', ['collection' => 'c', 'pipeline' => '[{"$match":{}}]']],
            ['schema', ['collection' => 'c']],
            ['import', ['collection' => 'c', 'format' => 'json', 'content' => '[{"a":1}]']],
            ['users', []], ['user_create', ['user' => 'u', 'password' => 'longenough1']],
            ['user_password', ['user' => 'otheracct_u', 'password' => 'longenough1']], ['user_drop', ['user' => 'otheracct_u']],
        ] as [$op, $extra]) {
            $res = $this->post($base . '/browse', ['op' => $op, 'db' => 'otheracct_secret'] + $extra);
            self::assertSame(403, $res->status(), $op . ' on another account must be refused');
        }
        self::assertSame(1, $this->calls('mongo.collections'), 'only the owned-db collections call reached the agent');
        self::assertSame(0, $this->calls('mongo.document_delete'), 'no delete reached the agent for another account');
        self::assertSame(1, $this->calls('mongo.document_insert'), 'only the owned-db insert reached the agent');
        self::assertSame(1, $this->calls('mongo.import'), 'only the owned-db import reached the agent');
        self::assertSame(1, $this->calls('mongo.schema'), 'only the owned-db schema analysis reached the agent');
        self::assertSame(1, $this->calls('mongo.database_users'), 'only the owned-db user listing reached the agent');
        self::assertSame(1, $this->calls('mongo.user_create'), 'only the owned-db user creation reached the agent');
        self::assertSame(1, $this->calls('mongo.index_create'), 'only the owned-db index create reached the agent');
    }

    public function testBackupRestoreIsConfinedToTheAccount(): void
    {
        $this->agent->succeed('mongo.status', ['installed' => true, 'running' => true, 'version' => '8.0.0']);
        $this->agent->succeed('mongo.overview', [
            'databases' => [['name' => $this->prefix . 'shop', 'size' => 100]],
            'users' => [],
        ]);
        // The listing returns one owned and one other account's archive; only
        // the owned one is shown.
        $this->agent->succeed('mongo.backups', ['backups' => [
            ['file' => '20260901-120000_' . $this->prefix . 'shop.archive.gz', 'db' => $this->prefix . 'shop', 'size' => 10, 'created' => '2026-09-01T12:00:00Z'],
            ['file' => '20260901-130000_otheracct_secret.archive.gz', 'db' => 'otheracct_secret', 'size' => 20, 'created' => '2026-09-01T13:00:00Z'],
        ]]);
        $this->login('cust');
        $base = '/sites/' . $this->site . '/mongo';

        $page = $this->request('GET', $base)->body();
        self::assertStringContainsString('20260901-120000_' . $this->prefix . 'shop.archive.gz', $page, 'own archive is offered for restore');
        self::assertStringNotContainsString('otheracct_secret', $page, "another account's archive is hidden");

        // Backing up the account's own database works and is prefixed.
        $this->agent->succeed('mongo.backup', ['job_id' => 'aaaaaaaaaaaaaaaa', 'file' => 'x']);
        $this->post($base . '/backup', ['db' => $this->prefix . 'shop']);
        self::assertSame($this->prefix . 'shop', $this->lastCall('mongo.backup')['db']);

        // Backing up another account's database is refused before the agent.
        $this->post($base . '/backup', ['db' => 'otheracct_secret']);
        self::assertSame(1, $this->calls('mongo.backup'), 'no cross-account backup reached the agent');
        self::assertNotNull($this->flash('error'));

        // Restoring/deleting an archive whose database is not the account's is
        // refused before the agent (the database is read from the file name).
        $this->agent->succeed('mongo.restore', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $this->agent->succeed('mongo.backup_delete', ['deleted' => 'x']);
        $this->post($base . '/restore', ['file' => '20260901-130000_otheracct_secret.archive.gz']);
        $this->post($base . '/backups/delete', ['file' => '20260901-130000_otheracct_secret.archive.gz']);
        self::assertSame(0, $this->calls('mongo.restore'));
        self::assertSame(0, $this->calls('mongo.backup_delete'));

        // Restoring the account's own archive works, with the database read
        // from the file name.
        $this->post($base . '/restore', ['file' => '20260901-120000_' . $this->prefix . 'shop.archive.gz', 'drop' => '0']);
        self::assertSame($this->prefix . 'shop', $this->lastCall('mongo.restore')['db']);
        self::assertSame(1, $this->calls('mongo.restore'));
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
