<?php

declare(strict_types=1);

namespace NimVorSite\Core;

use PDO;

final class Db
{
    private PDO $pdo;

    /** @param array{host: string, port: int, name: string, user: string, pass: string} $c */
    public function __construct(array $c)
    {
        $this->pdo = new PDO(
            sprintf('mysql:host=%s;port=%d;dbname=%s;charset=utf8mb4', $c['host'], $c['port'], $c['name']),
            $c['user'],
            $c['pass'],
            [
                PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
                PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
                PDO::ATTR_EMULATE_PREPARES => false,
                PDO::MYSQL_ATTR_INIT_COMMAND => "SET time_zone = '+00:00', sql_mode = 'STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'",
            ]
        );
    }

    /** @param list<mixed> $params */
    public function run(string $sql, array $params = []): int
    {
        $st = $this->pdo->prepare($sql);
        $st->execute($params);
        return $st->rowCount();
    }

    /**
     * @param list<mixed> $params
     * @return list<array<string, mixed>>
     */
    public function all(string $sql, array $params = []): array
    {
        $st = $this->pdo->prepare($sql);
        $st->execute($params);
        return $st->fetchAll();
    }

    /**
     * @param list<mixed> $params
     * @return array<string, mixed>|null
     */
    public function one(string $sql, array $params = []): ?array
    {
        $st = $this->pdo->prepare($sql);
        $st->execute($params);
        $row = $st->fetch();
        return $row === false ? null : $row;
    }

    /** @param list<mixed> $params */
    public function scalar(string $sql, array $params = []): mixed
    {
        $st = $this->pdo->prepare($sql);
        $st->execute($params);
        $v = $st->fetchColumn();
        return $v === false ? null : $v;
    }

    public function lastId(): int
    {
        return (int) $this->pdo->lastInsertId();
    }

    /** @param callable(): mixed $fn */
    public function transaction(callable $fn): mixed
    {
        $this->pdo->beginTransaction();
        try {
            $r = $fn();
            $this->pdo->commit();
            return $r;
        } catch (\Throwable $e) {
            $this->pdo->rollBack();
            throw $e;
        }
    }

    /** Applies database/migrations/*.php (each returns a list of SQL statements) once, in order. @return list<string> the ones applied */
    public function migrate(string $dir): array
    {
        $this->pdo->exec('CREATE TABLE IF NOT EXISTS migrations (name VARCHAR(190) NOT NULL PRIMARY KEY, applied_at DATETIME NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4');
        $done = array_column($this->all('SELECT name FROM migrations'), 'name');
        $files = glob(rtrim($dir, '/') . '/*.php') ?: [];
        sort($files);
        $applied = [];
        foreach ($files as $f) {
            $name = basename($f, '.php');
            if (in_array($name, $done, true)) {
                continue;
            }
            foreach ((array) require $f as $sql) {
                $this->pdo->exec((string) $sql);
            }
            $this->run('INSERT INTO migrations (name, applied_at) VALUES (?, UTC_TIMESTAMP())', [$name]);
            $applied[] = $name;
        }
        return $applied;
    }
}
