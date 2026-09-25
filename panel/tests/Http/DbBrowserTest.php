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

        // Administrators can open databases WhaCP does not manage by name.
        self::assertSame(200, $this->request('GET', '/databases/browse', query: ['name' => 'legacy_db'])->status());
        $this->api('/databases/browse/api?name=legacy_db', ['op' => 'tables']);
        self::assertSame('legacy_db', $this->lastCall('db.browse_tables')['database']);
        self::assertSame(404, $this->request('GET', '/databases/browse', query: ['name' => 'bad name;'])->status());
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
