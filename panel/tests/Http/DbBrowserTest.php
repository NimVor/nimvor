<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Tests\Support\HttpTestCase;

final class DbBrowserTest extends HttpTestCase
{
    private Connection $db;
    private int $shopDb;
    private int $otherDb;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $server = (int) $this->db->scalar('SELECT id FROM servers ORDER BY id LIMIT 1');
        $this->db->run("INSERT INTO databases (server_id, name, owner_id, created_at) VALUES (?, 'cust_shop', ?, '2026-01-01')", [$server, $cust]);
        $this->shopDb = $this->db->lastInsertId();
        $this->db->run("INSERT INTO databases (server_id, name, owner_id, created_at) VALUES (?, 'admin_crm', NULL, '2026-01-01')", [$server]);
        $this->otherDb = $this->db->lastInsertId();
        $this->agent->succeed('db.browse_tables', ['database' => 'x', 'tables' => [['name' => 'orders']], 'server' => '10.11']);
        $this->agent->succeed('db.query', ['results' => [['affected_rows' => 1, 'insert_id' => 0, 'info' => '']], 'ms' => 3]);
        $this->agent->succeed('db.browse_save', ['affected_rows' => 1, 'insert_id' => 0]);
    }

    /** @param array<string, mixed> $body */
    private function api(string $url, array $body): \WhaCP\Http\Response
    {
        $query = [];
        if (str_contains($url, '?')) {
            [$url, $qs] = explode('?', $url, 2);
            parse_str($qs, $query);
        }
        $token = $this->app->container->get(\WhaCP\Security\Csrf::class)->token();
        return $this->request('POST', $url, $body, ['content-type' => 'application/json', 'accept' => 'application/json', 'x-csrf-token' => $token], (string) json_encode($body), $query);
    }

    public function testCustomerBrowsesOwnDatabaseOnly(): void
    {
        $this->login('cust');
        $page = $this->request('GET', '/databases/' . $this->shopDb . '/browse');
        self::assertSame(200, $page->status());
        self::assertStringContainsString('cust_shop', $page->body());
        self::assertStringContainsString('dbbrowser.js', $page->body());

        $res = $this->api('/databases/' . $this->shopDb . '/browse/api', ['op' => 'tables', 'database' => 'admin_crm']);
        self::assertSame(200, $res->status());
        self::assertSame('cust_shop', $this->lastCall('db.browse_tables')['database'], 'the database comes from the route, never the request');

        self::assertSame(404, $this->request('GET', '/databases/' . $this->otherDb . '/browse')->status());
        self::assertSame(404, $this->api('/databases/' . $this->otherDb . '/browse/api', ['op' => 'tables'])->status());
        // Browsing any database by name is for administrators.
        self::assertSame(404, $this->request('GET', '/databases/browse', query: ['name' => 'admin_crm'])->status());
        self::assertSame(404, $this->api('/databases/browse/api?name=admin_crm', ['op' => 'tables'])->status());
        self::assertSame(1, $this->calls('db.browse_tables'));
    }

    public function testOperationsMapToAgentActionsAndAreAudited(): void
    {
        $this->login('admin');
        $this->api('/databases/' . $this->otherDb . '/browse/api', ['op' => 'rows', 'table' => 'orders', 'offset' => '50', 'limit' => 25, 'order' => 'id', 'desc' => true, 'filter' => ['column' => 'id', 'op' => '>', 'value' => '3']]);
        $call = $this->lastCall('db.browse_rows');
        self::assertSame(['admin_crm', 'orders', 50, 25, 'id', true, ['column' => 'id', 'op' => '>', 'value' => '3']], [$call['database'], $call['table'], $call['offset'], $call['limit'], $call['order'], $call['desc'], $call['filter']]);

        $res = $this->api('/databases/' . $this->otherDb . '/browse/api', ['op' => 'query', 'sql' => "UPDATE orders SET note = 'secret-value' WHERE id = 1"]);
        self::assertSame(200, $res->status());
        self::assertSame(1, json_decode($res->body(), true)['data']['results'][0]['affected_rows']);
        $audit = $this->db->fetch("SELECT * FROM audit_logs WHERE action = 'database.browser_query'");
        self::assertNotNull($audit);
        self::assertStringContainsString('UPDATE orders', (string) $audit['details']);

        $this->api('/databases/' . $this->otherDb . '/browse/api', ['op' => 'save', 'table' => 'orders', 'key' => ['id' => '1'], 'values' => ['note' => 'row-secret', 'data' => ['hex' => '00ff']]]);
        self::assertSame(['hex' => '00ff'], $this->lastCall('db.browse_save')['values']['data']);
        $audit = (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'database.browser_save'");
        self::assertStringContainsString('note', $audit);
        self::assertStringNotContainsString('row-secret', $audit, 'row values are not copied into the audit log');

        // Reads are not audited; unknown operations are refused.
        $this->api('/databases/' . $this->otherDb . '/browse/api', ['op' => 'tables']);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'database.browser_tables'"));
        self::assertSame(400, $this->api('/databases/' . $this->otherDb . '/browse/api', ['op' => 'drop_everything'])->status());

        // Administrators can open databases NimVor does not manage by name.
        self::assertSame(200, $this->request('GET', '/databases/browse', query: ['name' => 'legacy_db'])->status());
        $this->api('/databases/browse/api?name=legacy_db', ['op' => 'tables']);
        self::assertSame('legacy_db', $this->lastCall('db.browse_tables')['database']);
        self::assertSame(404, $this->request('GET', '/databases/browse', query: ['name' => 'bad name;'])->status());
    }

    public function testStructureChangesAndCsv(): void
    {
        $this->login('cust');
        $url = '/databases/' . $this->shopDb . '/browse/api';
        $this->agent->succeed('db.browse_alter', ['sql' => 'ALTER TABLE `orders` ADD COLUMN `phone` VARCHAR(20) NULL']);
        // The operation travels as alter_op; the database comes from the route.
        $col = ['name' => 'phone', 'type' => 'VARCHAR', 'length' => '20', 'nullable' => true];
        $res = $this->api($url, ['op' => 'alter', 'alter_op' => 'add_column', 'table' => 'orders', 'column' => $col, 'after' => 'id', 'database' => 'admin_crm', 'dry_run' => true]);
        self::assertSame(200, $res->status());
        $call = $this->lastCall('db.browse_alter');
        self::assertSame(['cust_shop', 'orders', 'add_column', $col, 'id', true], [$call['database'], $call['table'], $call['op'], $call['column'], $call['after'], $call['dry_run']]);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'database.browser_alter'"), 'a preview is not a change');
        $this->api($url, ['op' => 'alter', 'alter_op' => 'add_column', 'table' => 'orders', 'column' => $col]);
        $audit = (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'database.browser_alter'");
        self::assertStringContainsString('add_column', $audit);
        self::assertStringContainsString('ADD COLUMN', $audit);

        // CSV: the export is a read; the import is audited without its rows.
        $this->agent->succeed('db.browse_export_csv', ['csv' => "id\n1\n", 'rows' => 1, 'truncated' => false]);
        $res = $this->api($url, ['op' => 'export_csv', 'table' => 'orders', 'search' => 'ann', 'filter' => ['column' => 'id', 'op' => '>', 'value' => '0']]);
        self::assertSame("id\n1\n", json_decode($res->body(), true)['data']['csv']);
        self::assertSame('ann', $this->lastCall('db.browse_export_csv')['search']);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'database.browser_export_csv'"));
        $this->agent->succeed('db.browse_import_csv', ['rows' => 2, 'affected_rows' => 2]);
        $this->api($url, ['op' => 'import_csv', 'table' => 'orders', 'csv' => "email\nsecret-row@x.test\nb@x.test\n", 'header' => '1', 'delimiter' => ';', 'columns' => ['email'], 'mode' => 'ignore']);
        $call = $this->lastCall('db.browse_import_csv');
        self::assertSame([true, ';', ['email'], 'ignore'], [$call['header'], $call['delimiter'], $call['columns'], $call['mode']]);
        $audit = (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'database.browser_import_csv'");
        self::assertStringContainsString('"rows":2', $audit);
        self::assertStringNotContainsString('secret-row', $audit);

        // Not on another account's database.
        $calls = $this->calls('db.browse_alter');
        self::assertSame(404, $this->api('/databases/' . $this->otherDb . '/browse/api', ['op' => 'alter', 'alter_op' => 'drop_table', 'table' => 'orders'])->status());
        self::assertSame($calls, $this->calls('db.browse_alter'));
    }

    public function testAgentErrorsAreReportedAndFailedChangesAudited(): void
    {
        $this->login('admin');
        $this->agent->fail('db.browse_delete', 422, 'invalid_params', 'this table has no primary or unique key');
        $res = $this->api('/databases/' . $this->otherDb . '/browse/api', ['op' => 'delete', 'table' => 't', 'keys' => [['a' => '1']]]);
        self::assertSame(422, $res->status());
        self::assertStringContainsString('no primary', $res->body());
        self::assertSame('failure', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'database.browser_delete'"));
    }

    public function testViewerCannotBrowse(): void
    {
        $this->seedUser('view', 'viewer');
        $this->login('view');
        self::assertNotSame(200, $this->request('GET', '/databases/' . $this->otherDb . '/browse')->status());
        $this->api('/databases/' . $this->otherDb . '/browse/api', ['op' => 'tables']);
        self::assertSame(0, $this->calls('db.browse_tables'));
    }
}
