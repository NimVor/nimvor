<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\PostgresService;
use NimVor\Tests\Support\HttpTestCase;

/** The PostgreSQL SQL editor: ownership, write mode, what is recorded (never values) and what is sent to the agent. */
final class PostgresSqlTest extends HttpTestCase
{
    private Connection $db;
    private int $aliceDb;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('viewer', 'viewer');
        $this->seedUser('alice', 'customer');
        $this->seedUser('bob', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('pg.status', ['installed' => true, 'running' => true, 'managed' => true, 'version' => '16.4', 'port' => 5432]);
        $this->agent->succeed('pg.overview', ['databases' => [], 'roles' => [], 'version' => '16.4', 'max_connections' => 100, 'active_connections' => 1]);
        foreach (['pg.role_create', 'pg.database_create'] as $a) {
            $this->agent->succeed($a, ['ok' => true]);
        }
        $this->agent->succeed('pg.schema_map', ['schema' => ['items' => ['id', 'name']]]);
        $this->agent->succeed('pg.query', ['results' => [['command' => 'SELECT 1', 'columns' => [['name' => 'n', 'type' => 'integer']], 'rows' => [['1'], ['2']], 'rows_affected' => 2]], 'duration_ms' => 3, 'mode' => 'read']);
        $this->agent->succeed('pg.query_cancel', ['cancelled' => true]);
        $this->login('alice');
        $this->post('/postgres/databases', ['name' => 'shop']);
        $this->aliceDb = (int) $this->db->scalar("SELECT id FROM pg_databases WHERE name = 'alice_shop'");
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    /** @param array<string, mixed> $extra */
    private function runSql(string $sql, array $extra = []): \NimVor\Http\Response
    {
        return $this->post('/postgres/sql/run', ['db' => (string) $this->aliceDb, 'query' => $sql] + $extra);
    }

    public function testPageForOwnerCarriesSchemaAndNoInlineScript(): void
    {
        $page = $this->request('GET', '/postgres/sql', [], [], '', ['db' => (string) $this->aliceDb])->body();
        self::assertStringContainsString('id="pgsql"', $page);
        self::assertStringContainsString('items', $page);
        self::assertStringNotContainsString('<script>', $page);
        self::assertSame('alice_shop', $this->lastCall('pg.schema_map')['database']);
    }

    public function testOtherCustomerCannotUseTheEditor(): void
    {
        $this->switchTo('bob');
        $before = $this->calls('pg.query');
        self::assertSame(404, $this->runSql('SELECT 1')->status());
        self::assertSame($before, $this->calls('pg.query'));
        self::assertSame(404, $this->post('/postgres/sql/cancel', ['db' => (string) $this->aliceDb, 'query_id' => 'abcdef0123456789'])->status());
    }

    public function testReadRunSendsModeAndClampsLimits(): void
    {
        $res = $this->runSql('SELECT 1', ['limit' => '99999', 'timeout' => '9999', 'mode' => 'bogus', 'query_id' => 'abcdef0123456789']);
        self::assertSame(200, $res->status());
        $json = json_decode($res->body(), true);
        self::assertTrue($json['success']);
        self::assertSame(3, $json['data']['result']['duration_ms']);
        $call = $this->lastCall('pg.query');
        self::assertSame('read', $call['mode']);
        self::assertSame(1000, $call['limit']);
        self::assertSame(60, $call['timeout_seconds']);
        self::assertSame('alice_shop', $call['database']);
        self::assertSame('abcdef0123456789', $call['query_id']);
    }

    public function testBadQueryIdAndExplainAreDropped(): void
    {
        $this->runSql('SELECT 1', ['query_id' => '../x', 'explain' => 'rm -rf']);
        $call = $this->lastCall('pg.query');
        self::assertSame('', $call['query_id']);
        self::assertSame('', $call['explain']);
    }

    public function testEmptyAndHugeStatementsAreRefused(): void
    {
        self::assertSame(422, $this->runSql('   ')->status());
        self::assertSame(422, $this->runSql(str_repeat('a', 101 * 1024))->status());
    }

    public function testWriteModeNeedsDatabasesManage(): void
    {
        $this->switchTo('viewer');
        // The viewer role cannot own databases, so it is refused before anything is sent.
        $before = $this->calls('pg.query');
        self::assertContains($this->runSql('DELETE FROM items', ['mode' => 'write'])->status(), [403, 404]);
        self::assertSame($before, $this->calls('pg.query'));
    }

    public function testOwnerWriteIsAuditedWithShapeOnly(): void
    {
        $res = $this->runSql("UPDATE users SET password = 'hunter2-secret' WHERE id = 42", ['mode' => 'write']);
        self::assertSame(200, $res->status());
        self::assertSame('write', $this->lastCall('pg.query')['mode']);
        $audit = (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs'));
        self::assertStringContainsString('postgres.query', $audit);
        self::assertStringNotContainsString('hunter2-secret', $audit);
        $history = (string) json_encode($this->db->fetchAll('SELECT * FROM pg_query_history'));
        self::assertStringNotContainsString('hunter2-secret', $history);
        self::assertStringContainsString("SET password = ?", $history);
    }

    public function testReadRunsAreNotAuditedButAreInHistory(): void
    {
        $before = (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'postgres.query'");
        $json = json_decode($this->runSql('SELECT * FROM items WHERE id = 7')->body(), true);
        self::assertSame($before, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'postgres.query'"));
        self::assertSame('SELECT * FROM items WHERE id = ?', $json['data']['history'][0]['fingerprint']);
        self::assertSame(2, $json['data']['history'][0]['rows_returned']);
    }

    public function testSqlErrorIsRecordedAsErrorNotAsFailure(): void
    {
        $this->agent->succeed('pg.query', ['results' => [], 'error' => ['message' => 'relation "x" does not exist', 'code' => '42P01', 'position' => 15], 'duration_ms' => 1, 'mode' => 'read']);
        $json = json_decode($this->runSql('SELECT * FROM x')->body(), true);
        self::assertTrue($json['success']);
        self::assertSame(15, $json['data']['result']['error']['position']);
        self::assertSame('error', $json['data']['history'][0]['status']);
    }

    public function testCancelIsForwarded(): void
    {
        $json = json_decode($this->post('/postgres/sql/cancel', ['db' => (string) $this->aliceDb, 'query_id' => 'abcdef0123456789'])->body(), true);
        self::assertTrue($json['data']['cancelled']);
        self::assertSame('abcdef0123456789', $this->lastCall('pg.query_cancel')['query_id']);
        self::assertSame(404, $this->post('/postgres/sql/cancel', ['db' => (string) $this->aliceDb, 'query_id' => 'nope'])->status());
    }

    public function testHistoryKeepsTheNewest200PerUser(): void
    {
        for ($i = 0; $i < 205; $i++) {
            $this->runSql('SELECT ' . $i);
        }
        self::assertSame(200, (int) $this->db->scalar('SELECT COUNT(*) FROM pg_query_history'));
    }

    public function testFingerprintRemovesLiteralsAndNumbers(): void
    {
        self::assertSame('INSERT INTO t VALUES (?, ?, ?)', PostgresService::fingerprint("INSERT INTO t VALUES ('a''b', 12, 3.5)"));
        self::assertSame('SELECT ?', PostgresService::fingerprint('SELECT $q$secret$q$'));
        self::assertSame('SELECT a FROM b', PostgresService::fingerprint("SELECT\n  a\n FROM   b"));
        self::assertLessThanOrEqual(500, mb_strlen(PostgresService::fingerprint(str_repeat('abc ', 400))));
    }
}
