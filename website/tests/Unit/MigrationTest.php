<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Unit;

use NimVorSite\Tests\Support\SiteTestCase;

final class MigrationTest extends SiteTestCase
{
    public function testMigrationsAreAppliedOnce(): void
    {
        self::assertSame([], $this->app->db->migrate(dirname(__DIR__, 2) . '/database/migrations'));
        self::assertSame('0001_init', $this->app->db->scalar('SELECT name FROM migrations'));
    }

    public function testTablesAreInnoDbUtf8mb4(): void
    {
        $rows = $this->app->db->all("SELECT TABLE_NAME AS t, ENGINE AS e, TABLE_COLLATION AS c FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME <> 'migrations'");
        self::assertGreaterThanOrEqual(6, count($rows));
        foreach ($rows as $r) {
            self::assertSame('InnoDB', $r['e'], $r['t']);
            self::assertStringStartsWith('utf8mb4', $r['c'], $r['t']);
        }
    }

    public function testEmailIsUniqueAndLowercased(): void
    {
        $this->app->users->create('Ada@Example.test', 'Ada', 'x');
        self::assertNotNull($this->app->users->byEmail('ADA@example.TEST'));
        $this->expectException(\PDOException::class);
        $this->app->users->create('ada@example.test', 'Ada 2', 'x');
    }
}
