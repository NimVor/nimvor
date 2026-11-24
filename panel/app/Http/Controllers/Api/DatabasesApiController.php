<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\DatabaseRepository;
use NimVor\Services\DatabaseService;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Support\ValidationException;

/**
 * REST API v1: databases, their users and the users' privileges (the endpoints that create a database and
 * list databases are in ApiController). Every endpoint calls DatabaseService, as the web interface does:
 * names are validated and prefixed for hosting customers, plan limits apply, and a database is only
 * deleted when its name is sent as confirmation. A generated password is returned once; the stored
 * "reveal" of the web interface is deliberately not offered.
 */
final class DatabasesApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly DatabaseService $databases,
        private readonly DatabaseRepository $repo,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
    ) {
    }

    /**
     * @param array<string, mixed> $d with its grants
     * @return array<string, mixed>
     */
    private static function databaseOut(array $d): array
    {
        return [
            'id' => (int) $d['id'], 'name' => (string) $d['name'], 'website_id' => $d['website_id'] === null ? null : (int) $d['website_id'],
            'website' => $d['website'] ?? null, 'created_at' => $d['created_at'],
            'grants' => array_values(array_map(static fn (array $g): array => [
                'user_id' => (int) $g['user_id'], 'username' => (string) $g['username'], 'host' => (string) $g['host'], 'privileges' => (string) $g['privileges'],
            ], (array) ($d['grants'] ?? []))),
        ];
    }

    /**
     * @param array<string, mixed> $u
     * @return array<string, mixed>
     */
    private function userOut(array $u): array
    {
        return [
            'id' => (int) $u['id'], 'username' => (string) $u['username'], 'host' => (string) $u['host'],
            'website_id' => ($u['website_id'] ?? null) === null ? null : (int) $u['website_id'], 'created_at' => $u['created_at'],
            'grants' => array_values(array_map(static fn (array $g): array => [
                'database_id' => (int) $g['database_id'], 'database' => (string) $g['name'], 'privileges' => (string) $g['privileges'],
            ], $this->repo->grantsOf((int) $u['id']))),
        ];
    }

    /** @return array<string, mixed>|null */
    private function database(Request $r, string $id): ?array
    {
        foreach ($this->repo->databases($this->server->id(), $this->user($r)) as $d) {
            if ((int) $d['id'] === (int) $id) {
                return $d;
            }
        }
        return null;
    }

    /** @return array<string, mixed>|null */
    private function dbUser(Request $r, string $id): ?array
    {
        return $this->repo->user($this->server->id(), (int) $id, $this->user($r));
    }

    public function showDatabase(Request $r, string $id): Response
    {
        $d = $this->database($r, $id);
        return $d === null ? self::notFound() : self::ok(self::databaseOut($d));
    }

    public function deleteDatabase(Request $r, string $id): Response
    {
        $d = $this->database($r, $id);
        if ($d === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $d): Response {
            $confirm = (string) ($r->query('confirm') ?? self::body($r)['confirm'] ?? '');
            $this->databases->drop($d, $confirm);
            return self::ok(['deleted' => (int) $d['id'], 'name' => (string) $d['name']]);
        });
    }

    public function dumpDatabase(Request $r, string $id): Response
    {
        $d = $this->database($r, $id);
        if ($d === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $d): Response {
            $job = $this->databases->dump($d);
            $this->jobs->track($job, 'Dump ' . $d['name'], 'database', (string) $d['id'], (int) $this->user($r)['id']);
            return self::ok(['job_id' => $job], 202);
        });
    }

    public function setGrant(Request $r, string $id, string $user): Response
    {
        $d = $this->database($r, $id);
        $u = $this->dbUser($r, $user);
        if ($d === null || $u === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $d, $u): Response {
            $priv = (string) (self::body($r)['privileges'] ?? '');
            if ($priv !== 'none' && !isset(DatabaseService::PRIVILEGES[$priv])) {
                throw new ValidationException(['privileges' => 'Use all, readwrite, readonly or none.']);
            }
            $this->databases->grant($d, $u, $priv === 'none' ? null : $priv);
            return self::ok(self::databaseOut((array) $this->database($r, (string) $d['id'])));
        });
    }

    public function listUsers(Request $r): Response
    {
        return self::ok(array_map(fn (array $u): array => $this->userOut($u), $this->repo->users($this->server->id(), $this->user($r))));
    }

    public function showUser(Request $r, string $id): Response
    {
        $u = $this->dbUser($r, $id);
        return $u === null ? self::notFound() : self::ok($this->userOut($u));
    }

    public function createUser(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $res = $this->databases->createUser(self::body($r), $this->user($r));
            $host = DatabaseService::hostFrom(self::body($r), $this->user($r));
            $created = array_values(array_filter(
                $this->repo->users($this->server->id(), $this->user($r)),
                static fn (array $u): bool => $u['username'] === $res['user'] && $u['host'] === $host
            ))[0] ?? ['id' => 0, 'username' => $res['user'], 'host' => $host, 'website_id' => null, 'created_at' => gmdate('c')];
            return self::ok($this->userOut($created) + ['password' => $res['password']], 201);
        });
    }

    public function deleteUser(Request $r, string $id): Response
    {
        $u = $this->dbUser($r, $id);
        if ($u === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($u): Response {
            $this->databases->dropUser($u);
            return self::ok(['deleted' => (int) $u['id']]);
        });
    }

    public function resetPassword(Request $r, string $id): Response
    {
        $u = $this->dbUser($r, $id);
        if ($u === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $u): Response {
            $pw = $this->databases->resetPassword($u, filter_var(self::body($r)['store_password'] ?? false, FILTER_VALIDATE_BOOL));
            return self::ok(['id' => (int) $u['id'], 'username' => (string) $u['username'], 'host' => (string) $u['host'], 'password' => $pw]);
        });
    }

    public function addHost(Request $r, string $id): Response
    {
        $u = $this->dbUser($r, $id);
        if ($u === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $u): Response {
            $in = self::body($r);
            $res = $this->databases->addHost($u, $in, $this->user($r));
            return self::ok(['username' => $res['user'], 'host' => DatabaseService::hostFrom($in, $this->user($r)), 'password' => $res['password']], 201);
        });
    }
}
