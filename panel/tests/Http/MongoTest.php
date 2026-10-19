<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Tests\Support\HttpTestCase;

/** Server → MongoDB: databases and users, admin only. */
final class MongoTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('mongo.status', ['installed' => true, 'running' => true, 'version' => '7.0.14', 'auth' => false]);
        $this->agent->succeed('mongo.overview', [
            'databases' => [['name' => 'shop', 'size' => 8192, 'empty' => false]],
            'users' => [['user' => 'shopuser', 'db' => 'shop', 'roles' => ['readWrite']]],
        ]);
    }

    public function testAdminManagesDatabasesAndUsers(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/mongo')->body();
        foreach (['MongoDB', '7.0.14', 'shop', 'shopuser', 'readWrite'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }

        $this->agent->succeed('mongo.database_create', ['name' => 'blog']);
        $this->post('/mongo/databases', ['name' => 'blog']);
        self::assertSame(['name' => 'blog'], $this->lastCall('mongo.database_create'));

        $this->agent->succeed('mongo.user_create', ['user' => 'u', 'db' => 'blog']);
        $this->post('/mongo/users', ['user' => 'bloguser', 'password' => 'longenough1', 'db' => 'blog', 'role' => 'read']);
        self::assertSame(['user' => 'bloguser', 'password' => 'longenough1', 'db' => 'blog', 'role' => 'read'], $this->lastCall('mongo.user_create'));

        $this->agent->succeed('mongo.database_drop', ['dropped' => 'shop']);
        $this->post('/mongo/databases/drop', ['name' => 'shop', 'confirm' => 'shop']);
        self::assertSame(['name' => 'shop', 'confirm' => 'shop'], $this->lastCall('mongo.database_drop'));

        self::assertSame(3, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'mongo.%'"));
    }

    public function testConfigurationAndLog(): void
    {
        $this->agent->succeed('mongo.config_get', [
            'path' => '/etc/mongod.conf', 'port' => 27017, 'bind_ip' => '127.0.0.1',
            'db_path' => '/var/lib/mongodb', 'log_path' => '/var/log/mongodb/mongod.log',
            'authorization' => false, 'raw' => "net:\n  port: 27017\nsecurity:\n  authorization: disabled\n",
        ]);
        $this->agent->succeed('mongo.log', ['path' => '/var/log/mongodb/mongod.log', 'lines' => "started\nlistening on 27017"]);
        $this->login('admin');
        $page = $this->request('GET', '/mongo')->body();
        foreach (['Configuration', 'name="port"', 'name="bind_ip"', 'authorization', 'listening on 27017', '/var/log/mongodb/mongod.log'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }

        // Saving the configuration reaches the agent with validated values.
        $this->agent->succeed('mongo.config_set', ['path' => '/etc/mongod.conf', 'port' => 27018, 'bind_ip' => '0.0.0.0', 'authorization' => true, 'raw' => '']);
        $this->post('/mongo/config', ['port' => '27018', 'bind_ip' => '0.0.0.0', 'authorization' => '1']);
        self::assertSame(['port' => 27018, 'bind_ip' => '0.0.0.0', 'authorization' => true], $this->lastCall('mongo.config_set'));
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'mongo.config_set'"));

        // A bad port never reaches the agent.
        $this->post('/mongo/config', ['port' => '0', 'bind_ip' => '127.0.0.1']);
        self::assertSame(1, $this->calls('mongo.config_set'));
    }

    public function testDatabaseBrowser(): void
    {
        $this->login('admin');
        // The databases table links to the full-page browser per database.
        $page = $this->request('GET', '/mongo')->body();
        self::assertStringContainsString('/mongo/browser?db=shop', $page);
        // The browser is a full-page tool with a collections sidebar.
        $tool = $this->request('GET', '/mongo/browser')->body();
        foreach (['MongoDB browser', 'id="wcp-mongo"', 'assets/js/mongobrowser.js', 'Choose a database', 'shop'] as $want) {
            self::assertStringContainsString($want, $tool, $want);
        }

        $this->agent->succeed('mongo.collections', ['collections' => [['name' => 'orders', 'count' => 3]]]);
        $res = $this->post('/mongo/browse', ['op' => 'collections', 'db' => 'shop']);
        self::assertSame(200, $res->status());
        $body = (array) json_decode($res->body(), true);
        self::assertTrue($body['success']);
        self::assertSame('orders', $body['data']['collections'][0]['name']);
        self::assertSame(['db' => 'shop'], $this->lastCall('mongo.collections'));

        $this->agent->succeed('mongo.documents', ['documents' => [['_id' => 'a1', 'name' => 'x']], 'total' => 1, 'skip' => 0, 'limit' => 50]);
        $res = $this->post('/mongo/browse', ['op' => 'documents', 'db' => 'shop', 'collection' => 'orders', 'filter' => '{"name":"x"}', 'skip' => '0', 'limit' => '50']);
        $body = (array) json_decode($res->body(), true);
        self::assertSame(1, $body['data']['total']);
        self::assertSame('{"name":"x"}', $this->lastCall('mongo.documents')['filter']);

        $this->agent->succeed('mongo.document_delete', ['deleted' => 1]);
        $res = $this->post('/mongo/browse', ['op' => 'delete', 'db' => 'shop', 'collection' => 'orders', 'id' => '"a1"']);
        self::assertSame(1, ((array) json_decode($res->body(), true))['data']['deleted']);
        self::assertSame('"a1"', $this->lastCall('mongo.document_delete')['id']);

        // Insert and update (replace by _id) reach the agent with the document.
        $this->agent->succeed('mongo.document_insert', ['inserted_id' => 'x1']);
        $this->post('/mongo/browse', ['op' => 'insert', 'db' => 'shop', 'collection' => 'orders', 'document' => '{"name":"x"}']);
        self::assertSame('{"name":"x"}', $this->lastCall('mongo.document_insert')['document']);
        $this->agent->succeed('mongo.document_update', ['matched' => 1, 'modified' => 1]);
        $this->post('/mongo/browse', ['op' => 'update', 'db' => 'shop', 'collection' => 'orders', 'id' => '"a1"', 'document' => '{"_id":"a1","name":"y"}']);
        self::assertSame('"a1"', $this->lastCall('mongo.document_update')['id']);

        // Aggregation: a pipeline runs and returns documents + timing.
        $this->agent->succeed('mongo.aggregate', ['documents' => [['_id' => 'a', 'n' => 3]], 'ms' => 5, 'limit' => 50]);
        $res = $this->post('/mongo/browse', ['op' => 'aggregate', 'db' => 'shop', 'collection' => 'orders', 'pipeline' => '[{"$group":{"_id":"$name","n":{"$sum":1}}}]']);
        $body = (array) json_decode($res->body(), true);
        self::assertSame(5, $body['data']['ms']);
        self::assertSame('[{"$group":{"_id":"$name","n":{"$sum":1}}}]', $this->lastCall('mongo.aggregate')['pipeline']);

        // Indexes: list, create, drop.
        $this->agent->succeed('mongo.indexes', ['indexes' => [['name' => '_id_', 'key' => ['_id' => 1]], ['name' => 'email_1', 'key' => ['email' => 1], 'unique' => true]]]);
        $res = $this->post('/mongo/browse', ['op' => 'indexes', 'db' => 'shop', 'collection' => 'orders']);
        self::assertStringContainsString('email_1', $res->body());
        $this->agent->succeed('mongo.index_create', ['name' => 'email_1']);
        $this->post('/mongo/browse', ['op' => 'index_create', 'db' => 'shop', 'collection' => 'orders', 'keys' => '{"email":1}', 'options' => '{"unique":true}']);
        self::assertSame('{"email":1}', $this->lastCall('mongo.index_create')['keys']);
        $this->agent->succeed('mongo.index_drop', ['dropped' => 'email_1']);
        $this->post('/mongo/browse', ['op' => 'index_drop', 'db' => 'shop', 'collection' => 'orders', 'name' => 'email_1']);
        self::assertSame('email_1', $this->lastCall('mongo.index_drop')['name']);

        // An unknown op is a clean 400, not a 500.
        self::assertSame(400, $this->post('/mongo/browse', ['op' => 'nope', 'db' => 'shop'])->status());

        // Export: the collection is downloaded as an attachment, and the
        // chosen format decides the extension and content type.
        $this->agent->succeed('mongo.export', ['count' => 2, 'content' => "[\n  {\"_id\":\"a\"}\n]", 'format' => 'json']);
        $res = $this->request('GET', '/mongo/export', [], [], '', ['db' => 'shop', 'collection' => 'orders', 'format' => 'json', 'filter' => '{"n":1}']);
        self::assertSame(200, $res->status());
        self::assertSame('attachment; filename="shop.orders.json"', $res->header('Content-Disposition'));
        self::assertStringContainsString('application/json', (string) $res->header('Content-Type'));
        self::assertStringContainsString('"_id":"a"', $res->body());
        self::assertSame('{"n":1}', $this->lastCall('mongo.export')['filter']);

        $this->agent->succeed('mongo.export', ['count' => 2, 'content' => "{\"_id\":\"a\"}\n{\"_id\":\"b\"}", 'format' => 'jsonl']);
        $res = $this->request('GET', '/mongo/export', [], [], '', ['db' => 'shop', 'collection' => 'orders', 'format' => 'jsonl']);
        self::assertSame('attachment; filename="shop.orders.jsonl"', $res->header('Content-Disposition'));
        self::assertStringContainsString('application/x-ndjson', (string) $res->header('Content-Type'));
        self::assertSame('jsonl', $this->lastCall('mongo.export')['format']);

        // Import: documents are inserted and the result (inserted / errors) is
        // returned; the uploaded content and format reach the agent.
        $this->agent->succeed('mongo.import', ['inserted' => 2, 'errors' => 1, 'total' => 3]);
        $res = $this->post('/mongo/browse', ['op' => 'import', 'db' => 'shop', 'collection' => 'orders', 'format' => 'json', 'content' => '[{"a":1},{"a":2},{"a":3}]']);
        $body = (array) json_decode($res->body(), true);
        self::assertSame(200, $res->status());
        self::assertSame(2, $body['data']['inserted']);
        self::assertSame(1, $body['data']['errors']);
        self::assertSame('[{"a":1},{"a":2},{"a":3}]', $this->lastCall('mongo.import')['content']);
        self::assertSame('json', $this->lastCall('mongo.import')['format']);
    }

    public function testBrowserDeleteNeedsManageAndCustomersCannotReachMongo(): void
    {
        // A read-only viewer may browse but not delete a document.
        $this->seedUser('viewer', 'viewer');
        $this->login('viewer');
        $this->agent->succeed('mongo.collections', ['collections' => []]);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'collections', 'db' => 'shop'])->status());
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'delete', 'db' => 'shop', 'collection' => 'c', 'id' => '"a1"'])->status());
        self::assertSame(0, $this->calls('mongo.document_delete'));
        // A viewer may list indexes (read) but not create or drop them.
        $this->agent->succeed('mongo.indexes', ['indexes' => []]);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'indexes', 'db' => 'shop', 'collection' => 'c'])->status());
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'index_create', 'db' => 'shop', 'collection' => 'c', 'keys' => '{"a":1}'])->status());
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'index_drop', 'db' => 'shop', 'collection' => 'c', 'name' => 'a_1'])->status());
        // A viewer may not import (a write); the agent is never reached.
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'import', 'db' => 'shop', 'collection' => 'c', 'format' => 'json', 'content' => '[{"a":1}]'])->status());
        self::assertSame(0, $this->calls('mongo.import'));
        self::assertSame(0, $this->calls('mongo.index_create'));

        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/mongo')->status());
        self::assertSame(403, $this->post('/mongo/databases', ['name' => 'x'])->status());
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'collections', 'db' => 'shop'])->status());
        self::assertSame(0, $this->calls('mongo.database_create'));
    }

    public function testNotInstalledOffersRepositoryThenInstall(): void
    {
        $this->agent->succeed('mongo.status', ['installed' => false, 'running' => false, 'version' => '', 'auth' => false]);
        $this->agent->succeed('mongo.repository', ['kind' => 'apt-ubuntu', 'name' => 'MongoDB 8.0 (apt)', 'url' => 'https://repo.mongodb.org/apt/ubuntu', 'version' => '8.0', 'supported' => true, 'enabled' => false, 'note' => '']);
        $this->login('admin');
        $page = $this->request('GET', '/mongo')->body();
        self::assertStringContainsString('Install MongoDB', $page);
        self::assertStringContainsString('Add repository', $page);
        self::assertSame(0, $this->calls('mongo.overview'), 'nothing is queried when MongoDB is not installed');

        // Repository must be added before install is offered.
        self::assertStringNotContainsString('action="/mongo/install"', $page, 'install is not offered until the repository is enabled');

        $this->agent->succeed('mongo.repository.enable', ['job_id' => 'cccccccccccccccc']);
        $this->post('/mongo/repository', []);
        self::assertSame(1, $this->calls('mongo.repository.enable'));
    }

    public function testInstallOfferedOnceRepositoryEnabled(): void
    {
        $this->agent->succeed('mongo.status', ['installed' => false, 'running' => false, 'version' => '', 'auth' => false]);
        $this->agent->succeed('mongo.repository', ['kind' => 'apt-ubuntu', 'name' => 'MongoDB 8.0 (apt)', 'url' => '', 'version' => '8.0', 'supported' => true, 'enabled' => true, 'note' => '']);
        $this->login('admin');
        $page = $this->request('GET', '/mongo')->body();
        self::assertStringContainsString('action="/mongo/install"', $page);

        $this->agent->succeed('mongo.install', ['job_id' => 'dddddddddddddddd']);
        $this->post('/mongo/install', []);
        self::assertSame(1, $this->calls('mongo.install'));
    }

    public function testCustomersCannotInstallOrEnableRepository(): void
    {
        $this->login('cust');
        self::assertSame(403, $this->post('/mongo/repository', [])->status());
        self::assertSame(403, $this->post('/mongo/install', [])->status());
        self::assertSame(403, $this->post('/mongo/service', ['op' => 'start'])->status());
        self::assertSame(0, $this->calls('mongo.install'));
    }
}
