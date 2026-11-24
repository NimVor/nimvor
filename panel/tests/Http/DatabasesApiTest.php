<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\UserRepository;
use NimVor\Services\ApiTokenService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: databases, their users and privileges. */
final class DatabasesApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    private int $site;
    private int $adminSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'reseller');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->site = $sites->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($this->cust));
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($this->admin));
        $this->db = $this->app->container->get(Connection::class);
        foreach (['db.create' => ['created' => true], 'db.drop' => ['dropped' => true], 'db.user_create' => ['created' => true], 'db.user_drop' => ['dropped' => true], 'db.user_password' => ['changed' => true], 'db.grant' => ['ok' => true], 'db.dump' => ['job_id' => 'dddddddddddddddd']] as $a => $res) {
            $this->agent->succeed($a, $res);
        }
        $this->db->run("UPDATE websites SET owner_id = ? WHERE id = ?", [$this->cust, $this->site]);
    }

    /** @param array<string, mixed> $json */
    private function api(string $method, string $path, ?string $token, array $json = []): Response
    {
        $h = ['content-type' => 'application/json'] + ($token === null ? [] : ['authorization' => 'Bearer ' . $token]);
        return $this->app->handle(new Request($method, $path, [], [], ['REMOTE_ADDR' => '192.0.2.50'], $h, $json === [] ? '' : (string) json_encode($json)));
    }

    /** @return array<string, mixed> */
    private static function json(Response $r): array
    {
        return (array) json_decode($r->body(), true);
    }

    private function token(int $userId, string $ability = 'write'): string
    {
        return $this->app->container->get(ApiTokenService::class)->create($userId, ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => $ability, 'all_scopes' => '1', 'expires_days' => 30]);
    }

    /** @return array<string, mixed> */
    private function mkUser(string $token, array $in = []): array
    {
        $res = $this->api('POST', '/api/v1/database-users', $token, $in + ['username' => 'app']);
        self::assertSame(201, $res->status(), $res->body());
        return self::json($res)['data'];
    }

    public function testUserLifecycleAndPasswordsAreShownOnce(): void
    {
        $cust = $this->token($this->cust);
        $u = $this->mkUser($cust);
        self::assertSame(['cust_app', 'localhost'], [$u['username'], $u['host']], 'a customer\'s names carry their account prefix');
        self::assertGreaterThanOrEqual(16, strlen($u['password']));
        self::assertSame($u['password'], $this->lastCall('db.user_create')['password']);
        $audit = (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'database.user_create'");
        self::assertStringNotContainsString($u['password'], $audit);

        $read = self::json($this->api('GET', '/api/v1/database-users/' . $u['id'], $cust))['data'];
        self::assertArrayNotHasKey('password', $read);
        self::assertSame([], $read['grants']);
        self::assertSame([$u['id']], array_column(self::json($this->api('GET', '/api/v1/database-users', $cust))['data'], 'id'));

        $res = $this->api('POST', '/api/v1/database-users/' . $u['id'] . '/password', $cust, []);
        self::assertSame(200, $res->status(), $res->body());
        $pw = self::json($res)['data']['password'];
        self::assertNotSame($u['password'], $pw);
        self::assertSame([$pw, 'cust_app'], [$this->lastCall('db.user_password')['password'], $this->lastCall('db.user_password')['user']]);

        $res = $this->api('DELETE', '/api/v1/database-users/' . $u['id'], $cust);
        self::assertSame([200, $u['id']], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame('cust_app', $this->lastCall('db.user_drop')['user']);
        self::assertSame(404, $this->api('GET', '/api/v1/database-users/' . $u['id'], $cust)->status());
        self::assertSame(404, $this->api('DELETE', '/api/v1/database-users/' . $u['id'], $cust)->status());
    }

    public function testUserValidation(): void
    {
        $cust = $this->token($this->cust);
        $calls = $this->calls('db.user_create');
        foreach ([[], ['username' => ''], ['username' => 'Bad Name'], ['username' => str_repeat('a', 40)], ['username' => 'app', 'host' => 'evil.example.com'], ['username' => 'app', 'host' => "1.2.3.4'; DROP"], ['username' => 'app', 'website_id' => $this->adminSite]] as $bad) {
            $res = $this->api('POST', '/api/v1/database-users', $cust, $bad);
            self::assertSame(422, $res->status(), json_encode($bad) . $res->body());
        }
        self::assertSame($calls, $this->calls('db.user_create'));
        $this->mkUser($cust);
        self::assertSame(422, $this->api('POST', '/api/v1/database-users', $cust, ['username' => 'app'])->status(), 'the same account twice');
    }

    public function testGrantsAndTheDatabaseDetail(): void
    {
        $cust = $this->token($this->cust);
        $made = self::json($this->api('POST', '/api/v1/databases', $cust, ['name' => 'shop', 'website_id' => $this->site]))['data'];
        $dbId = $made['database_id'];
        $u = $this->mkUser($cust);
        $base = "/api/v1/databases/$dbId";

        $res = $this->api('PUT', "$base/grants/{$u['id']}", $cust, ['privileges' => 'readonly']);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame(['cust_shop', 'cust_app', 'readonly'], [$d['name'], $d['grants'][0]['username'], $d['grants'][0]['privileges']]);
        $sent = $this->lastCall('db.grant');
        self::assertSame(['cust_app', 'cust_shop', 'readonly', false], [$sent['user'], $sent['database'], $sent['privileges'], $sent['revoke']]);
        self::assertSame('cust_shop', self::json($this->api('GET', "/api/v1/database-users/{$u['id']}", $cust))['data']['grants'][0]['database']);

        $res = $this->api('PUT', "$base/grants/{$u['id']}", $cust, ['privileges' => 'none']);
        self::assertSame([], self::json($res)['data']['grants']);
        self::assertTrue($this->lastCall('db.grant')['revoke']);
        $calls = $this->calls('db.grant');
        foreach ([[], ['privileges' => 'god'], ['privileges' => '']] as $bad) {
            self::assertSame(422, $this->api('PUT', "$base/grants/{$u['id']}", $cust, $bad)->status());
        }
        self::assertSame($calls, $this->calls('db.grant'));
        self::assertSame($dbId, self::json($this->api('GET', $base, $cust))['data']['id']);
    }

    public function testDeletingADatabaseNeedsItsName(): void
    {
        $cust = $this->token($this->cust);
        $dbId = self::json($this->api('POST', '/api/v1/databases', $cust, ['name' => 'shop']))['data']['database_id'];
        $drops = $this->calls('db.drop');
        foreach ([[], ['confirm' => 'shop'], ['confirm' => 'nope']] as $body) {
            $res = $this->api('DELETE', "/api/v1/databases/$dbId", $cust, $body);
            self::assertSame(409, $res->status(), json_encode($body));
        }
        self::assertSame($drops, $this->calls('db.drop'), 'nothing is dropped without the exact name');
        $req = new Request('DELETE', "/api/v1/databases/$dbId", ['confirm' => 'cust_shop'], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $cust]);
        $res = $this->app->handle($req);
        self::assertSame([200, 'cust_shop'], [$res->status(), self::json($res)['data']['name']], $res->body());
        self::assertSame('cust_shop', $this->lastCall('db.drop')['name']);
        self::assertSame(404, $this->api('GET', "/api/v1/databases/$dbId", $cust)->status());
        // The name in the body works too.
        $dbId = self::json($this->api('POST', '/api/v1/databases', $cust, ['name' => 'two']))['data']['database_id'];
        self::assertSame(200, $this->api('DELETE', "/api/v1/databases/$dbId", $cust, ['confirm' => 'cust_two'])->status());
    }

    public function testDumpAndRemoteHosts(): void
    {
        $cust = $this->token($this->cust);
        $admin = $this->token($this->admin);
        $dbId = self::json($this->api('POST', '/api/v1/databases', $cust, ['name' => 'shop']))['data']['database_id'];
        $res = $this->api('POST', "/api/v1/databases/$dbId/dump", $cust);
        self::assertSame([202, 'dddddddddddddddd'], [$res->status(), self::json($res)['data']['job_id'] ?? null], $res->body());
        self::assertSame('cust_shop', $this->lastCall('db.dump')['name']);
        self::assertNotNull($this->db->scalar("SELECT id FROM agent_jobs WHERE job_id = 'dddddddddddddddd'"));

        // Remote access: an administrator may, a customer only when the provider allows it.
        $u = $this->mkUser($admin, ['username' => 'remote']);
        $res = $this->api('POST', "/api/v1/database-users/{$u['id']}/hosts", $admin, ['host' => '203.0.113.%']);
        self::assertSame(201, $res->status(), $res->body());
        self::assertSame(['remote', '203.0.113.%'], [self::json($res)['data']['username'], self::json($res)['data']['host']]);
        self::assertNotSame($u['password'], self::json($res)['data']['password'], 'its own password');
        self::assertSame(['remote', '203.0.113.%'], [$this->lastCall('db.user_create')['user'], $this->lastCall('db.user_create')['host']]);
        self::assertSame(422, $this->api('POST', "/api/v1/database-users/{$u['id']}/hosts", $admin, ['host' => '203.0.113.%'])->status(), 'already there');
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $admin = $this->token($this->admin);
        $theirs = self::json($this->api('POST', '/api/v1/databases', $admin, ['name' => 'adm', 'create_user' => true, 'username' => 'admuser']))['data'];
        $theirUser = array_values(array_filter(self::json($this->api('GET', '/api/v1/database-users', $admin))['data'], static fn (array $u): bool => $u['username'] === 'admuser'))[0];
        $mine = self::json($this->api('POST', '/api/v1/databases', $cust, ['name' => 'mine']))['data']['database_id'];
        $myUser = $this->mkUser($cust);

        // Another account's resources are not visible and nothing reaches the server.
        $n = $this->calls('db.drop') + $this->calls('db.grant') + $this->calls('db.user_drop') + $this->calls('db.user_password') + $this->calls('db.dump');
        $calls = [['GET', "/databases/{$theirs['database_id']}"], ['DELETE', "/databases/{$theirs['database_id']}"], ['POST', "/databases/{$theirs['database_id']}/dump"], ['PUT', "/databases/{$theirs['database_id']}/grants/{$myUser['id']}"], ['PUT', "/databases/$mine/grants/{$theirUser['id']}"], ['GET', "/database-users/{$theirUser['id']}"], ['DELETE', "/database-users/{$theirUser['id']}"], ['POST', "/database-users/{$theirUser['id']}/password"], ['POST', "/database-users/{$theirUser['id']}/hosts"]];
        foreach ($calls as [$m, $p]) {
            self::assertSame(404, $this->api($m, '/api/v1' . $p, $cust, ['confirm' => 'cust_adm', 'privileges' => 'all', 'host' => 'localhost'])->status(), "$m $p");
        }
        self::assertSame($n, $this->calls('db.drop') + $this->calls('db.grant') + $this->calls('db.user_drop') + $this->calls('db.user_password') + $this->calls('db.dump'));
        self::assertNotContains($theirUser['id'], array_column(self::json($this->api('GET', '/api/v1/database-users', $cust))['data'], 'id'));

        // Read-only tokens read and never write.
        self::assertSame(200, $this->api('GET', "/api/v1/databases/$mine", $ro)->status());
        self::assertSame(200, $this->api('GET', '/api/v1/database-users', $ro)->status());
        foreach ([['DELETE', "/databases/$mine"], ['POST', "/databases/$mine/dump"], ['PUT', "/databases/$mine/grants/{$myUser['id']}"], ['POST', '/database-users'], ['DELETE', "/database-users/{$myUser['id']}"], ['POST', "/database-users/{$myUser['id']}/password"], ['POST', "/database-users/{$myUser['id']}/hosts"]] as [$m, $p]) {
            self::assertSame(403, $this->api($m, '/api/v1' . $p, $ro, ['username' => 'x'])->status(), "$m $p");
        }
        self::assertSame(401, $this->api('GET', "/api/v1/databases/$mine", null)->status());
    }
}
