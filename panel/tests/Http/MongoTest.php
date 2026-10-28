<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Tests\Support\HttpTestCase;

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
        // Databases tab (the default) shows the server version and the database.
        $page = $this->request('GET', '/mongo')->body();
        foreach (['MongoDB', '7.0.14', 'shop'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        // The Users tab lists the users.
        $users = $this->request('GET', '/mongo', [], [], '', ['section' => 'users'])->body();
        foreach (['shopuser', 'readWrite'] as $want) {
            self::assertStringContainsString($want, $users, $want);
        }

        $this->agent->succeed('mongo.database_create', ['name' => 'blog']);
        $this->post('/mongo/databases', ['name' => 'blog']);
        self::assertSame(['name' => 'blog'], $this->lastCall('mongo.database_create'));

        $this->agent->succeed('mongo.user_create', ['user' => 'u', 'db' => 'blog']);
        $this->post('/mongo/users', ['user' => 'bloguser', 'password' => 'longenough1', 'db' => 'blog', 'role' => 'read']);
        self::assertSame(['user' => 'bloguser', 'password' => 'longenough1', 'db' => 'blog', 'role' => 'read'], $this->lastCall('mongo.user_create'));

        // One-step create: a database with its user in the same request.
        $this->post('/mongo/databases', ['name' => 'app', 'user' => 'appuser', 'password' => 'longenough2', 'role' => 'readWrite']);
        self::assertSame(['name' => 'app'], $this->lastCall('mongo.database_create'));
        self::assertSame(['user' => 'appuser', 'password' => 'longenough2', 'db' => 'app', 'role' => 'readWrite'], $this->lastCall('mongo.user_create'));

        $this->agent->succeed('mongo.database_drop', ['dropped' => 'shop']);
        $this->post('/mongo/databases/drop', ['name' => 'shop', 'confirm' => 'shop']);
        self::assertSame(['name' => 'shop', 'confirm' => 'shop'], $this->lastCall('mongo.database_drop'));

        self::assertSame(5, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'mongo.%'"));

        // Bulk delete drops each selected database (one single drop happened
        // earlier, so two more here makes three in total).
        $this->agent->succeed('mongo.database_drop', ['dropped' => 'x']);
        $this->post('/mongo/databases/bulk-drop', ['names' => ['blog', 'app']]);
        self::assertSame(3, $this->calls('mongo.database_drop'));
        // An empty selection is refused before the agent.
        $this->post('/mongo/databases/bulk-drop', ['names' => []]);
        self::assertSame(3, $this->calls('mongo.database_drop'));
    }

    public function testDatabaseNoteSavesAndShows(): void
    {
        $this->login('admin');
        // The Databases grid shows the database's user and a Position column.
        $page = $this->request('GET', '/mongo')->body();
        self::assertStringContainsString('shopuser', $page, 'the database row shows its user');
        self::assertStringContainsString('Localhost', $page, 'the Position column');

        $this->post('/mongo/note', ['name' => 'shop', 'note' => 'primary store']);
        $page = $this->request('GET', '/mongo')->body();
        self::assertStringContainsString('primary store', $page, 'the saved note shows on the row');

        // Clearing removes it.
        $this->post('/mongo/note', ['name' => 'shop', 'note' => '']);
        self::assertStringNotContainsString('primary store', $this->request('GET', '/mongo')->body());

        // A customer may not set notes (the route requires mongo.manage).
        $this->login('cust');
        self::assertSame(403, $this->post('/mongo/note', ['name' => 'shop', 'note' => 'x'])->status());
    }

    public function testRemoteServersAddListAndPull(): void
    {
        $this->login('admin');
        // Adding tests the connection first, then stores it.
        $this->agent->succeed('mongo.remote_ping', ['ok' => true, 'version' => '7.0.5', 'databases' => 2]);
        $this->post('/mongo/remotes', ['name' => 'Old server', 'host' => 'db.example.com', 'port' => '27017', 'username' => '', 'password' => '', 'auth_db' => 'admin']);
        $ping = $this->lastCall('mongo.remote_ping');
        self::assertSame('db.example.com', $ping['host']);

        // It shows on the Remote servers tab.
        $page = $this->request('GET', '/mongo', [], [], '', ['section' => 'remotes'])->body();
        self::assertStringContainsString('Old server', $page);

        // The Get-DB modal lists the remote's databases over JSON.
        $this->agent->succeed('mongo.remote_databases', ['databases' => [['name' => 'shop', 'size' => 2048]]]);
        $res = $this->post('/mongo/remotes/databases', ['id' => '1']);
        $body = (array) json_decode($res->body(), true);
        self::assertTrue($body['success']);
        self::assertSame('shop', $body['data']['databases'][0]['name']);

        // Pulling copies a remote database onto this server (async job).
        $this->agent->succeed('mongo.remote_pull', ['job_id' => 'job-remote-1']);
        $this->post('/mongo/remotes/pull', ['id' => '1', 'remote_db' => 'shop', 'target_db' => 'shop_copy', 'drop' => '1']);
        $pull = $this->lastCall('mongo.remote_pull');
        self::assertSame('shop', $pull['remote_db']);
        self::assertSame('shop_copy', $pull['target_db']);
        self::assertTrue($pull['drop']);
        self::assertSame('db.example.com', $pull['host']);

        // Remove it (the saved row is gone; the modal placeholder is not a row).
        $this->post('/mongo/remotes/delete', ['id' => '1']);
        $after = $this->request('GET', '/mongo', [], [], '', ['section' => 'remotes'])->body();
        self::assertStringNotContainsString('Old server', $after);
        self::assertStringContainsString('No remote servers yet', $after);

        // A customer may not manage remotes.
        $this->login('cust');
        self::assertSame(403, $this->post('/mongo/remotes', ['name' => 'x', 'host' => 'y'])->status());
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
        // Configuration tab: summary + the settings/config-file modals.
        $page = $this->request('GET', '/mongo', [], [], '', ['section' => 'config'])->body();
        foreach (['Configuration', 'name="port"', 'name="bind_ip"', 'authorization', 'Edit config file'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        // Log tab shows the log tail.
        $logPage = $this->request('GET', '/mongo', [], [], '', ['section' => 'log'])->body();
        self::assertStringContainsString('listening on 27017', $logPage);
        self::assertStringContainsString('/var/log/mongodb/mongod.log', $logPage);

        // Saving the configuration reaches the agent with validated values.
        $this->agent->succeed('mongo.config_set', ['path' => '/etc/mongod.conf', 'port' => 27018, 'bind_ip' => '0.0.0.0', 'authorization' => true, 'raw' => '']);
        $this->post('/mongo/config', ['port' => '27018', 'bind_ip' => '0.0.0.0', 'authorization' => '1']);
        self::assertSame(['port' => 27018, 'bind_ip' => '0.0.0.0', 'authorization' => true], $this->lastCall('mongo.config_set'));
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'mongo.config_set'"));

        // A bad port never reaches the agent.
        $this->post('/mongo/config', ['port' => '0', 'bind_ip' => '127.0.0.1']);
        self::assertSame(1, $this->calls('mongo.config_set'));

        // The raw config-file editor save reaches the agent.
        $this->agent->succeed('mongo.config_write', ['path' => '/etc/mongod.conf', 'port' => 28000, 'bind_ip' => '127.0.0.1', 'authorization' => false, 'raw' => '']);
        $this->post('/mongo/config-file', ['content' => "net:\n  port: 28000\n"]);
        self::assertSame("net:\n  port: 28000\n", $this->lastCall('mongo.config_write')['content']);
    }

    public function testAdminPasswordAndAuthToggle(): void
    {
        $this->login('admin');
        $this->agent->succeed('mongo.admin_password', ['user' => 'nimvorAdmin']);
        $this->post('/mongo/admin-password', ['password' => 'longenough-admin']);
        self::assertSame('longenough-admin', $this->lastCall('mongo.admin_password')['password']);

        $this->agent->succeed('mongo.auth_set', ['authorization' => true]);
        $this->post('/mongo/auth', ['enable' => '1']);
        self::assertTrue($this->lastCall('mongo.auth_set')['enable']);
        $this->post('/mongo/auth', ['enable' => '0']);
        self::assertFalse($this->lastCall('mongo.auth_set')['enable']);

        // A customer cannot reach either endpoint.
        $this->login('cust');
        self::assertSame(403, $this->post('/mongo/admin-password', ['password' => 'longenough-admin'])->status());
        self::assertSame(403, $this->post('/mongo/auth', ['enable' => '1'])->status());
    }

    public function testMongoPermissionsAreIndependentOfServerPermissions(): void
    {
        // A role with only the MongoDB permissions — no server.view/manage —
        // can view and manage MongoDB, proving the new permissions stand alone.
        $svc = $this->app->container->get(\NimVor\Services\RoleService::class);
        $roleId = $svc->create(['name' => 'mongoadmin', 'label' => 'MongoDB admin', 'permissions' => [
            \NimVor\Security\Permissions::MONGO_VIEW, \NimVor\Security\Permissions::MONGO_MANAGE,
        ]]);
        $this->app->container->get(Connection::class)->run('UPDATE roles SET scope = ? WHERE id = ?', ['all', $roleId]);
        $this->seedUser('mdba', 'mongoadmin');
        $this->login('mdba');

        // View works through mongo.view.
        self::assertSame(200, $this->request('GET', '/mongo')->status());
        $this->agent->succeed('mongo.collections', ['collections' => []]);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'collections', 'db' => 'shop'])->status());

        // Management works through mongo.manage.
        $this->agent->succeed('mongo.database_create', ['name' => 'blog']);
        $res = $this->post('/mongo/databases', ['name' => 'blog']);
        self::assertSame(303, $res->status());
        self::assertSame(1, $this->calls('mongo.database_create'));
    }

    public function testBackupRestoreAndDelete(): void
    {
        $this->agent->succeed('mongo.backups', ['backups' => [
            ['file' => '20260901-120000_shop.archive.gz', 'db' => 'shop', 'size' => 2048, 'created' => '2026-09-01T12:00:00Z'],
        ]]);
        $this->login('admin');

        // The Backups tab lists existing archives and offers a backup form.
        $page = $this->request('GET', '/mongo', [], [], '', ['section' => 'backups'])->body();
        foreach (['Backups', '20260901-120000_shop.archive.gz'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }

        // Starting a backup reaches the agent and tracks the job.
        $this->agent->succeed('mongo.backup', ['job_id' => 'aaaaaaaaaaaaaaaa', 'file' => '20260901-120500_shop.archive.gz']);
        $this->post('/mongo/backup', ['db' => 'shop']);
        self::assertSame('shop', $this->lastCall('mongo.backup')['db']);

        // Restoring passes the file, database and drop flag through.
        $this->agent->succeed('mongo.restore', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $this->post('/mongo/restore', ['file' => '20260901-120000_shop.archive.gz', 'db' => 'shop', 'drop' => '0']);
        self::assertSame('20260901-120000_shop.archive.gz', $this->lastCall('mongo.restore')['file']);
        self::assertSame('shop', $this->lastCall('mongo.restore')['db']);
        self::assertFalse($this->lastCall('mongo.restore')['drop']);

        // Deleting an archive reaches the agent.
        $this->agent->succeed('mongo.backup_delete', ['deleted' => '20260901-120000_shop.archive.gz']);
        $this->post('/mongo/backups/delete', ['file' => '20260901-120000_shop.archive.gz']);
        self::assertSame('20260901-120000_shop.archive.gz', $this->lastCall('mongo.backup_delete')['file']);

        // A customer cannot reach any of the backup endpoints.
        $this->login('cust');
        self::assertSame(403, $this->post('/mongo/backup', ['db' => 'shop'])->status());
        self::assertSame(403, $this->post('/mongo/restore', ['file' => '20260901-120000_shop.archive.gz', 'db' => 'shop'])->status());
    }

    public function testDatabaseBrowser(): void
    {
        $this->login('admin');
        // The databases table links to the full-page browser per database.
        $page = $this->request('GET', '/mongo')->body();
        self::assertStringContainsString('/mongo/browser?db=shop', $page);
        // The browser is a full-page tool with a collections sidebar.
        $tool = $this->request('GET', '/mongo/browser')->body();
        foreach (['MongoDB browser', 'id="nim-mongo"', 'assets/js/mongobrowser.js', 'Choose a database', 'shop'] as $want) {
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

        // A sort and a projection reach the agent alongside the filter.
        $this->post('/mongo/browse', ['op' => 'documents', 'db' => 'shop', 'collection' => 'orders', 'sort' => '{"name":-1}', 'projection' => '{"name":1}']);
        self::assertSame('{"name":-1}', $this->lastCall('mongo.documents')['sort']);
        self::assertSame('{"name":1}', $this->lastCall('mongo.documents')['projection']);

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

        // Explain returns the query plan without running the pipeline.
        $this->agent->succeed('mongo.explain', ['explain' => ['queryPlanner' => ['winningPlan' => ['stage' => 'COLLSCAN']]]]);
        $res = $this->post('/mongo/browse', ['op' => 'explain', 'db' => 'shop', 'collection' => 'orders', 'pipeline' => '[{"$match":{}}]']);
        self::assertStringContainsString('COLLSCAN', $res->body());
        self::assertSame('[{"$match":{}}]', $this->lastCall('mongo.explain')['pipeline']);

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

        // Collection stats feed the right-rail and the Stats/Validation tabs.
        $this->agent->succeed('mongo.collection_stats', ['count' => 1248, 'size' => 13002342, 'nindexes' => 4, 'validationLevel' => 'strict']);
        $res = $this->post('/mongo/browse', ['op' => 'collection_stats', 'db' => 'shop', 'collection' => 'orders']);
        $body = (array) json_decode($res->body(), true);
        self::assertSame(1248, $body['data']['count']);
        self::assertSame('orders', $this->lastCall('mongo.collection_stats')['collection']);

        // Collection management: create, empty, rename, drop.
        $this->agent->succeed('mongo.collection_create', ['created' => 'carts']);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'coll_create', 'db' => 'shop', 'collection' => 'carts'])->status());
        self::assertSame('carts', $this->lastCall('mongo.collection_create')['collection']);
        $this->agent->succeed('mongo.collection_empty', ['deleted' => 7]);
        self::assertSame(7, ((array) json_decode($this->post('/mongo/browse', ['op' => 'coll_empty', 'db' => 'shop', 'collection' => 'orders'])->body(), true))['data']['deleted']);
        $this->agent->succeed('mongo.collection_rename', ['renamed' => 'orders_old']);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'coll_rename', 'db' => 'shop', 'collection' => 'orders', 'name' => 'orders_old'])->status());
        self::assertSame('orders_old', $this->lastCall('mongo.collection_rename')['name']);
        $this->agent->succeed('mongo.collection_drop', ['dropped' => 'carts']);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'coll_drop', 'db' => 'shop', 'collection' => 'carts'])->status());

        // Schema: the sampled count and per-field types come back.
        $this->agent->succeed('mongo.schema', ['sampled' => 3, 'fields' => [
            ['field' => '_id', 'total' => 3, 'types' => [['type' => 'objectId', 'count' => 3]]],
            ['field' => 'price', 'total' => 2, 'types' => [['type' => 'double', 'count' => 2]]],
        ]]);
        $res = $this->post('/mongo/browse', ['op' => 'schema', 'db' => 'shop', 'collection' => 'orders', 'sample' => 500]);
        $body = (array) json_decode($res->body(), true);
        self::assertSame(3, $body['data']['sampled']);
        self::assertSame('price', $body['data']['fields'][1]['field']);
        self::assertSame(500, $this->lastCall('mongo.schema')['sample']);

        // Users: list, create, change password, drop — all scoped to the db.
        $this->agent->succeed('mongo.database_users', ['users' => [['user' => 'shopu', 'roles' => ['readWrite']]]]);
        $res = $this->post('/mongo/browse', ['op' => 'users', 'db' => 'shop']);
        $body = (array) json_decode($res->body(), true);
        self::assertSame('shopu', $body['data']['users'][0]['user']);
        self::assertSame(['readWrite'], $body['data']['users'][0]['roles']);
        $this->agent->succeed('mongo.user_create', ['user' => 'newu', 'db' => 'shop']);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'user_create', 'db' => 'shop', 'user' => 'newu', 'password' => 'longenough1', 'role' => 'read'])->status());
        self::assertSame('shop', $this->lastCall('mongo.user_create')['db']);
        self::assertSame('read', $this->lastCall('mongo.user_create')['role']);
        $this->agent->succeed('mongo.user_password', ['user' => 'newu', 'db' => 'shop']);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'user_password', 'db' => 'shop', 'user' => 'newu', 'password' => 'longenough2'])->status());
        $this->agent->succeed('mongo.user_drop', ['dropped' => 'newu', 'db' => 'shop']);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'user_drop', 'db' => 'shop', 'user' => 'newu'])->status());

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
        // A viewer may list users (read) but not create, change or drop them.
        $this->agent->succeed('mongo.database_users', ['users' => []]);
        self::assertSame(200, $this->post('/mongo/browse', ['op' => 'users', 'db' => 'shop'])->status());
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'user_create', 'db' => 'shop', 'user' => 'u', 'password' => 'longenough1'])->status());
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'user_password', 'db' => 'shop', 'user' => 'u', 'password' => 'longenough1'])->status());
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'user_drop', 'db' => 'shop', 'user' => 'u'])->status());
        self::assertSame(0, $this->calls('mongo.user_create'));
        // A viewer may not create, drop, empty or rename a collection.
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'coll_create', 'db' => 'shop', 'collection' => 'c'])->status());
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'coll_drop', 'db' => 'shop', 'collection' => 'c'])->status());
        self::assertSame(403, $this->post('/mongo/browse', ['op' => 'coll_empty', 'db' => 'shop', 'collection' => 'c'])->status());
        self::assertSame(0, $this->calls('mongo.collection_create'));
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
