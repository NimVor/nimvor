<?php

declare(strict_types=1);

namespace NimVor\Database;

use RuntimeException;

/**
 * Applies ordered SQL migrations from database/migrations.
 *
 * Each migration file returns a list of SQL statements. Migrations run in a
 * transaction and are recorded in the `migrations` table, so running the
 * migrator twice is harmless.
 */
final class Migrator
{
    public function __construct(
        private readonly Connection $db,
        private readonly string $directory,
    ) {
    }

    /** @return list<string> names of migrations applied by this call */
    public function migrate(): array
    {
        $this->db->run('CREATE TABLE IF NOT EXISTS migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)');
        $applied = array_column($this->db->fetchAll('SELECT name FROM migrations'), 'name');
        $files = glob(rtrim($this->directory, '/') . '/*.php') ?: [];
        sort($files, SORT_STRING);

        $ran = [];
        foreach ($files as $file) {
            $name = basename($file, '.php');
            if (in_array($name, $applied, true)) {
                continue;
            }
            $statements = require $file;
            if (!is_array($statements)) {
                throw new RuntimeException("Migration {$name} must return a list of SQL statements");
            }
            $this->db->transaction(function (Connection $db) use ($statements, $name): void {
                foreach ($statements as $sql) {
                    $db->pdo()->exec((string) $sql);
                }
                $db->run('INSERT INTO migrations (name, applied_at) VALUES (?, ?)', [$name, gmdate('c')]);
            });
            $ran[] = $name;
        }
        return $ran;
    }

    /** @return list<string> */
    public function pending(): array
    {
        $this->db->run('CREATE TABLE IF NOT EXISTS migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)');
        $applied = array_column($this->db->fetchAll('SELECT name FROM migrations'), 'name');
        $pending = [];
        foreach (glob(rtrim($this->directory, '/') . '/*.php') ?: [] as $file) {
            $name = basename($file, '.php');
            if (!in_array($name, $applied, true)) {
                $pending[] = $name;
            }
        }
        sort($pending);
        return $pending;
    }
}
