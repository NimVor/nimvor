<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** The PostgreSQL table browser: only the owner's database, what is sent to the agent, and the CSV export. */
final class PostgresBrowserTest extends HttpTestCase
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
        foreach (['pg.role_create', 'pg.database_create', 'pg.row_insert', 'pg.row_update', 'pg.row_delete'] as $a) {
            $this->agent->succeed($a, ['ok' => true]);
        }
        $this->agent->succeed('pg.tables', ['schemas' => ['public'], 'tables' => [
            ['schema' => 'public', 'name' => 'items', 'kind' => 'table', 'rows_estimate' => 2, 'size_bytes' => 8192, 'editable' => true],
            ['schema' => 'public', 'name' => 'nokey', 'kind' => 'table', 'rows_estimate' => 0, 'size_bytes' => 0, 'editable' => false],
        ]]);
        $cols = [
            ['name' => 'id', 'type' => 'integer', 'not_null' => true, 'default' => "nextval('items_id_seq'::regclass)", 'key' => true, 'generated' => false, 'identity' => false],
            ['name' => 'name', 'type' => 'text', 'not_null' => false, 'key' => false, 'generated' => false, 'identity' => false],
        ];
        $this->agent->succeed('pg.browse', [
            'columns' => $cols, 'rows' => [['1', 'Ana, "the" first'], ['2', null]], 'truncated' => [], 'total' => 2, 'total_capped' => false,
            'offset' => 0, 'limit' => 50, 'key' => ['id'], 'editable' => true, 'kind' => 'r',
        ]);
        $this->agent->succeed('pg.table_structure', ['columns' => $cols, 'key' => ['id'], 'indexes' => [['name' => 'items_pkey', 'definition' => 'CREATE UNIQUE INDEX items_pkey ON public.items USING btree (id)', 'unique' => true, 'primary' => true]], 'editable' => true, 'reason' => '', 'kind' => 'r']);
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

    public function testOwnerBrowsesRowsAndTables(): void
    {
        $page = $this->request('GET', '/postgres/browse', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items'])->body();
        self::assertStringContainsString('items', $page);
        self::assertStringContainsString('nokey', $page);
        self::assertStringContainsString('Ana, &quot;the&quot; first', $page, 'values are escaped');
        self::assertStringContainsString('NULL', $page);
        self::assertStringContainsString('Insert row', $page);
        $call = $this->lastCall('pg.browse');
        self::assertSame('alice_shop', $call['database']);
        self::assertSame(['offset' => 0, 'limit' => 50], ['offset' => $call['offset'], 'limit' => $call['limit']]);
    }

    public function testThePageHasTheSameLayoutAsTheMysqlBrowser(): void
    {
        $page = $this->request('GET', '/postgres/browse', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items'])->body();
        // The full-height tool layout and the shared database-browser structure and styles.
        foreach (['class="nim-tool"', 'class="nim-db"', 'nim-db-side', 'nim-db-main', 'nim-db-tabs', 'nim-db-pane', 'nim-db-bar', 'nim-db-grid-wrap', 'nim-db-grid'] as $needle) {
            self::assertStringContainsString($needle, $page, $needle);
        }
        // The four tabs, Browse being the current one, and the table list with the open table marked.
        foreach (['Browse', 'Structure', 'Insert', 'SQL'] as $label) {
            self::assertStringContainsString($label, $page);
        }
        self::assertMatchesRegularExpression('~<a class="btn btn-sm btn-primary"[^>]*aria-current="page"[^>]*><i class="bi bi-grid-3x3"~', $page);
        self::assertMatchesRegularExpression('~nim-db-table active"[^>]*data-pgf-name="public\.items"~', $page);
        self::assertStringContainsString('Filter tables', $page);
        // The SQL tab keeps the table that was open.
        self::assertStringContainsString('/postgres/sql?db=' . $this->aliceDb . '&amp;schema=public&amp;table=items', $page);
        // No inline script or style: the panel's content security policy blocks them.
        self::assertStringNotContainsString('<script>', $page);
    }

    public function testTheTabsShowTheStructureAndAnInsertForm(): void
    {
        $q = ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items'];
        $structure = $this->request('GET', '/postgres/browse', [], [], '', $q + ['tab' => 'structure'])->body();
        self::assertStringContainsString('items_pkey', $structure);
        self::assertStringContainsString('NOT NULL', $structure);
        self::assertMatchesRegularExpression('~btn-primary"[^>]*aria-current="page"[^>]*><i class="bi bi-diagram-3"~', $structure);

        $insert = $this->request('GET', '/postgres/browse', [], [], '', $q + ['tab' => 'insert'])->body();
        self::assertStringContainsString('action="/postgres/rows"', $insert);
        self::assertStringContainsString('name="mode[name]"', $insert);
        self::assertStringContainsString('name="value[name]"', $insert);
        self::assertStringContainsString('csrf', strtolower($insert));
        // A tab the page does not offer, or a tab with no table open, falls back to Browse.
        $odd = $this->request('GET', '/postgres/browse', [], [], '', $q + ['tab' => 'drop'])->body();
        self::assertMatchesRegularExpression('~btn-primary"[^>]*aria-current="page"[^>]*><i class="bi bi-grid-3x3"~', $odd);
        $none = $this->request('GET', '/postgres/browse', [], [], '', ['db' => (string) $this->aliceDb, 'tab' => 'insert'])->body();
        self::assertStringContainsString('Choose a table on the left', $none);
    }

    public function testATableWithoutAKeyOffersNoInsertFormAndSaysWhy(): void
    {
        $cols = [['name' => 'v', 'type' => 'integer', 'not_null' => false, 'key' => false, 'generated' => false, 'identity' => false]];
        $this->agent->succeed('pg.browse', ['columns' => $cols, 'rows' => [['1']], 'truncated' => [], 'total' => 1, 'total_capped' => false, 'offset' => 0, 'limit' => 50, 'key' => [],
            'editable' => false, 'reason' => 'This table has no primary or unique key, so single rows cannot be addressed.', 'kind' => 'r']);
        $q = ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'nokey'];
        $insert = $this->request('GET', '/postgres/browse', [], [], '', $q + ['tab' => 'insert'])->body();
        self::assertStringNotContainsString('action="/postgres/rows"', $insert);
        self::assertStringContainsString('no primary or unique key', $insert);
        $browse = $this->request('GET', '/postgres/browse', [], [], '', $q)->body();
        self::assertStringNotContainsString('Insert row', $browse, 'no button that leads to a form that is not there');
        self::assertStringContainsString('no primary or unique key', $browse);
    }

    public function testSortSearchAndFilterAreForwardedAsData(): void
    {
        $this->request('GET', '/postgres/browse', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items', 'q' => "x'; DROP TABLE items; --", 'fc' => 'name', 'fo' => 'contains', 'fv' => 'an', 'sort' => 'id', 'dir' => 'desc', 'page' => '3', 'per' => '25']);
        $call = $this->lastCall('pg.browse');
        self::assertSame("x'; DROP TABLE items; --", $call['search']);
        self::assertSame([['column' => 'name', 'op' => 'contains', 'value' => 'an']], $call['filters']);
        self::assertSame(['id', true, 50, 25], [$call['sort'], $call['desc'], $call['offset'], $call['limit']]);
        // An operator the page does not offer is dropped, never forwarded.
        $this->request('GET', '/postgres/browse', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items', 'fc' => 'name', 'fo' => '; DROP', 'fv' => 'x']);
        self::assertArrayNotHasKey('filters', $this->lastCall('pg.browse'));
    }

    public function testAnotherCustomerCannotBrowseOrEdit(): void
    {
        $this->switchTo('bob');
        $before = $this->calls('pg.tables') + $this->calls('pg.browse');
        $res = $this->request('GET', '/postgres/browse', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items']);
        self::assertSame('/postgres', $res->header('Location'));
        $this->request('GET', '/postgres/export', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items']);
        self::assertSame($before, $this->calls('pg.tables') + $this->calls('pg.browse'), "bob's request reached the agent");
        foreach (['/postgres/rows', '/postgres/rows/update', '/postgres/rows/delete'] as $path) {
            $this->post($path, ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items', 'key' => ['id' => '1'], 'mode' => ['name' => 'value'], 'value' => ['name' => 'hacked']]);
        }
        self::assertSame(0, $this->calls('pg.row_insert') + $this->calls('pg.row_update') + $this->calls('pg.row_delete'));
    }

    public function testRowFormsSendValuesNullsAndKeys(): void
    {
        $f = ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items'];
        // Insert: "default" leaves the column out, "value" sends the text, "null" sends NULL.
        $this->post('/postgres/rows', $f + ['mode' => ['id' => 'default', 'name' => 'value'], 'value' => ['id' => '', 'name' => '']]);
        self::assertSame(['name' => ''], $this->lastCall('pg.row_insert')['values'], 'an empty text is an empty string, not NULL');
        $this->post('/postgres/rows', $f + ['mode' => ['name' => 'null']]);
        self::assertSame(['name' => null], $this->lastCall('pg.row_insert')['values']);
        // Update: "keep" columns are not sent; the key comes along.
        $this->post('/postgres/rows/update', $f + ['key' => ['id' => '2'], 'mode' => ['id' => 'keep', 'name' => 'value'], 'value' => ['name' => "O'Brien"]]);
        $call = $this->lastCall('pg.row_update');
        self::assertSame(['id' => '2'], $call['key']);
        self::assertSame(['name' => "O'Brien"], $call['values']);
        $this->post('/postgres/rows/delete', $f + ['key' => ['id' => '2']]);
        self::assertSame(['id' => '2'], $this->lastCall('pg.row_delete')['key']);
        self::assertSame('alice_shop', $this->lastCall('pg.row_delete')['database']);
    }

    public function testAgentRefusalIsShownAndNothingElseHappens(): void
    {
        $this->agent->fail('pg.row_update', 409, 'conflict', 'The row no longer exists or changed; nothing was modified. Reload the table.');
        $res = $this->post('/postgres/rows/update', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items', 'key' => ['id' => '9'], 'mode' => ['name' => 'value'], 'value' => ['name' => 'x']]);
        self::assertStringContainsString('/postgres/browse?', (string) $res->header('Location'));
        self::assertStringContainsString('no longer exists', (string) json_encode($this->flash('error')));
    }

    public function testExportIsCsvWithNullsAndQuotesAndASafeFilename(): void
    {
        $res = $this->request('GET', '/postgres/export', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items']);
        self::assertSame(200, $res->status());
        self::assertStringContainsString('text/csv', (string) $res->header('Content-Type'));
        self::assertSame("id,name\n1,\"Ana, \"\"the\"\" first\"\n2,\n", $res->body());
        self::assertStringContainsString('filename="alice_shop.public.items.csv"', (string) $res->header('Content-Disposition'));
        $evil = $this->request('GET', '/postgres/export', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => "a\"\r\nSet-Cookie: x=1"]);
        self::assertMatchesRegularExpression('/^attachment; filename="[A-Za-z0-9._-]+\.csv"$/', (string) $evil->header('Content-Disposition'), 'nothing in the name can end the header');
    }

    public function testReadOnlyTablesShowNoEditControls(): void
    {
        $this->agent->succeed('pg.browse', [
            'columns' => [['name' => 'x', 'type' => 'integer', 'not_null' => false, 'key' => false, 'generated' => false, 'identity' => false]], 'rows' => [['1']], 'truncated' => [],
            'total' => 1, 'total_capped' => false, 'offset' => 0, 'limit' => 50, 'key' => [], 'editable' => false, 'reason' => 'This table has no primary key, so its rows cannot be changed safely.', 'kind' => 'r',
        ]);
        $page = $this->request('GET', '/postgres/browse', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'nokey'])->body();
        self::assertStringContainsString('has no primary key', $page);
        self::assertStringNotContainsString('Add row', $page);
        self::assertStringNotContainsString('pgb-row', $page);
    }

    public function testViewerMayBrowseButNotChange(): void
    {
        $this->switchTo('viewer');
        $res = $this->request('GET', '/postgres/browse', [], [], '', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items']);
        self::assertSame(200, $res->status());
        self::assertStringNotContainsString('Add row', $res->body());
        self::assertSame(403, $this->post('/postgres/rows', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items', 'mode' => ['name' => 'value'], 'value' => ['name' => 'x']])->status());
        self::assertSame(0, $this->calls('pg.row_insert'));
    }

    public function testNoRowValuesInTheAuditTrail(): void
    {
        $this->post('/postgres/rows', ['db' => (string) $this->aliceDb, 'schema' => 'public', 'table' => 'items', 'mode' => ['name' => 'value'], 'value' => ['name' => 'secret-card-4111']]);
        self::assertStringNotContainsString('secret-card-4111', (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs')));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'postgres.row_insert'"));
    }
}
