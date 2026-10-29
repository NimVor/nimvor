<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Unit;

use PHPUnit\Framework\TestCase;

/**
 * Sites without a terminal upgrade by importing database/sql/<migration>.sql in phpMyAdmin.
 * Those files must say exactly what the migration does.
 */
final class MigrationSqlTest extends TestCase
{
    private static function normal(string $sql): string
    {
        return rtrim(trim((string) preg_replace('/\s+/', ' ', $sql)), ';');
    }

    /** @return list<array{string}> */
    public static function migrations(): array
    {
        $out = [];
        foreach (glob(dirname(__DIR__, 2) . '/database/sql/*.sql') ?: [] as $f) {
            $out[] = [basename($f, '.sql')];
        }
        return $out;
    }

    public function testEveryUpgradeFileHasAnExistingMigrationAndSaysTheSame(): void
    {
        $files = self::migrations();
        self::assertNotSame([], $files, 'there is no SQL upgrade file');
        foreach ($files as [$name]) {
            $php = dirname(__DIR__, 2) . '/database/migrations/' . $name . '.php';
            self::assertFileExists($php, $name);
            $sql = self::normal((string) file_get_contents(dirname(__DIR__, 2) . '/database/sql/' . $name . '.sql'));
            $sqlNoComments = self::normal((string) preg_replace('/^--.*$/m', '', (string) file_get_contents(dirname(__DIR__, 2) . '/database/sql/' . $name . '.sql')));
            foreach ((array) require $php as $statement) {
                self::assertStringContainsString(self::normal((string) $statement), $sqlNoComments, $name . ': a statement of the migration is missing from the SQL file');
            }
            self::assertStringContainsString("INSERT IGNORE INTO migrations (name, applied_at) VALUES ('" . $name . "', UTC_TIMESTAMP())", $sql, $name . ' does not record itself');
        }
    }
}
