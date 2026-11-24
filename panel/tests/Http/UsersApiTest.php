<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\UserRepository;
use NimVor\Services\ApiTokenService;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: users, hosting accounts and plans. */
final class UsersApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    private int $reseller;
    private int $sub;
    private int $small;
    private int $big;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->reseller = $this->seedUser('resell', 'reseller');
        $this->sub = $this->seedUser('sub', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$this->reseller, $this->sub]);
        $this->db->run("INSERT INTO plans (name, description, disk_quota_mb, max_websites, created_at, updated_at) VALUES ('Small', 'one site', 10240, 1, 'x', 'x')");
        $this->small = (int) $this->db->lastInsertId();
        $this->db->run("INSERT INTO plans (name, description, disk_quota_mb, max_websites, created_at, updated_at) VALUES ('Big', 'ten sites', 51200, 10, 'x', 'x')");
        $this->big = (int) $this->db->lastInsertId();
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [$this->small, $this->cust]);
        $this->app->container->get(SettingsService::class)->update(['dns_default_ipv4' => '203.0.113.10'], null);
        foreach (['sites.apply' => ['changed' => []], 'sites.delete' => ['removed' => true], 'cron.apply' => ['ok' => true], 'workers.apply' => ['ok' => true], 'account.suspend' => ['ok' => true], 'mail.suspend' => ['ok' => true], 'db.user_lock' => ['ok' => true], 'stats.enable' => ['ok' => true], 'mail.mailbox_delete' => ['ok' => true], 'mail.alias_delete' => ['ok' => true], 'mail.domain_remove' => ['ok' => true], 'db.drop' => ['ok' => true], 'db.user_drop' => ['ok' => true], 'dns.zone_delete' => ['ok' => true]] as $a => $r) {
            $this->agent->succeed($a, $r);
        }
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
        return $this->app->container->get(ApiTokenService::class)->create($userId, ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => $ability, 'expires_days' => 30]);
    }

    private function roleId(string $name): int
    {
        return (int) $this->db->scalar('SELECT id FROM roles WHERE name = ?', [$name]);
    }

    public function testListAndShowUsersWithTheirScope(): void
    {
        $admin = $this->token($this->admin);
        $res = $this->api('GET', '/api/v1/users', $admin);
        self::assertSame(200, $res->status());
        $names = array_column(self::json($res)['data'], 'username');
        self::assertEqualsCanonicalizing(['admin', 'cust', 'resell', 'sub'], $names);
        $row = array_values(array_filter(self::json($res)['data'], static fn (array $u): bool => $u['username'] === 'cust'))[0];
        self::assertSame(['customer', 'own'], [$row['role']['name'], $row['role']['scope']]);
        self::assertSame([$this->small, 'Small'], [$row['plan_id'], $row['plan_name']]);
        self::assertArrayNotHasKey('password_hash', $row);
        self::assertSame('cust', self::json($this->api('GET', '/api/v1/users/' . $this->cust, $admin))['data']['username']);

        // A reseller sees only their own customers.
        $reseller = $this->token($this->reseller);
        $mine = self::json($this->api('GET', '/api/v1/users', $reseller))['data'];
        self::assertSame(['sub'], array_column($mine, 'username'));
        self::assertSame(404, $this->api('GET', '/api/v1/users/' . $this->cust, $reseller)->status(), 'another account is not visible');
        self::assertSame(404, $this->api('GET', '/api/v1/users/' . $this->admin, $reseller)->status());
    }

    public function testPermissionsAndReadOnlyTokens(): void
    {
        $cust = $this->token($this->cust);
        self::assertSame(403, $this->api('GET', '/api/v1/users', $cust)->status(), 'customers cannot list users');
        self::assertSame(403, $this->api('POST', '/api/v1/users', $cust, ['username' => 'x'])->status());
        $read = $this->token($this->admin, 'read');
        self::assertSame(200, $this->api('GET', '/api/v1/users', $read)->status());
        foreach ([['POST', '/api/v1/users'], ['PATCH', '/api/v1/users/' . $this->cust], ['DELETE', '/api/v1/users/' . $this->cust], ['PUT', '/api/v1/users/' . $this->cust . '/plan'], ['POST', '/api/v1/users/' . $this->cust . '/suspend'], ['POST', '/api/v1/users/' . $this->cust . '/terminate'], ['POST', '/api/v1/accounts']] as [$m, $p]) {
            $res = $this->api($m, $p, $read, ['x' => 1]);
            self::assertSame([403, 'read_only_token'], [$res->status(), self::json($res)['error']['code']], $m . ' ' . $p);
        }
        self::assertSame(401, $this->api('GET', '/api/v1/users', null)->status());
    }

    public function testCreateUserValidatesAndNeverLeaksTheHash(): void
    {
        $admin = $this->token($this->admin);
        $res = $this->api('POST', '/api/v1/users', $admin, ['username' => 'Bad Name', 'email' => 'nope', 'role_id' => 9999, 'password' => 'short']);
        self::assertSame(422, $res->status());
        $fields = self::json($res)['error']['fields'];
        foreach (['username', 'email', 'role_id', 'password'] as $f) {
            self::assertArrayHasKey($f, $fields, $f);
        }

        $res = $this->api('POST', '/api/v1/users', $admin, ['username' => 'jsmith', 'email' => 'j@example.com', 'role_id' => $this->roleId('operator'), 'generate_password' => true]);
        self::assertSame(201, $res->status(), $res->body());
        $data = self::json($res)['data'];
        self::assertSame('jsmith', $data['user']['username']);
        self::assertGreaterThanOrEqual(16, strlen((string) $data['password']), 'the generated password is returned once');
        self::assertStringNotContainsString('password_hash', $res->body());
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'user.create'"));

        // A password the caller chose must pass the policy and is not echoed.
        $res = $this->api('POST', '/api/v1/users', $admin, ['username' => 'second', 'email' => 's@example.com', 'role_id' => $this->roleId('viewer'), 'password' => 'Correct-Horse-Battery-9']);
        self::assertSame(201, $res->status(), $res->body());
        self::assertNull(self::json($res)['data']['password']);
        // Duplicates are refused.
        self::assertSame(422, $this->api('POST', '/api/v1/users', $admin, ['username' => 'second', 'email' => 'other@example.com', 'role_id' => $this->roleId('viewer'), 'password' => 'Correct-Horse-Battery-9'])->status());
    }

    public function testNobodyCanHandOutMoreThanTheyHold(): void
    {
        $reseller = $this->token($this->reseller);
        // A reseller creates customers only, never an administrator.
        $res = $this->api('POST', '/api/v1/users', $reseller, ['username' => 'sneaky', 'email' => 's@example.com', 'role_id' => $this->roleId('admin'), 'generate_password' => true]);
        self::assertSame(422, $res->status());
        self::assertArrayHasKey('role_id', self::json($res)['error']['fields']);
        self::assertNull($this->db->scalar("SELECT id FROM users WHERE username = 'sneaky'"));
        // A customer of their own is fine and belongs to them.
        $res = $this->api('POST', '/api/v1/users', $reseller, ['username' => 'mine', 'email' => 'm@example.com', 'role_id' => $this->roleId('customer'), 'generate_password' => true]);
        self::assertSame(201, $res->status(), $res->body());
        self::assertSame($this->reseller, self::json($res)['data']['user']['parent_id']);
    }

    public function testPartialUpdateAndTheSafetyRules(): void
    {
        $admin = $this->token($this->admin);
        $res = $this->api('PATCH', '/api/v1/users/' . $this->cust, $admin, ['display_name' => 'Cust Ltd']);
        self::assertSame(200, $res->status(), $res->body());
        $u = self::json($res)['data'];
        self::assertSame(['Cust Ltd', 'cust@example.test', 'customer', true], [$u['display_name'], $u['email'], $u['role']['name'], $u['is_active']], 'what was not sent is kept');

        $res = $this->api('PATCH', '/api/v1/users/' . $this->cust, $admin, ['is_active' => false, 'password' => 'Correct-Horse-Battery-9']);
        self::assertSame(200, $res->status());
        self::assertFalse(self::json($res)['data']['is_active']);
        self::assertGreaterThanOrEqual(2, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'user.update'"), 'every change is audited');

        // Not your own account, not the last administrator.
        $res = $this->api('PATCH', '/api/v1/users/' . $this->admin, $admin, ['is_active' => false]);
        self::assertSame(422, $res->status());
        $res = $this->api('PATCH', '/api/v1/users/' . $this->admin, $admin, ['role_id' => $this->roleId('viewer')]);
        self::assertSame(422, $res->status(), 'the last administrator cannot be demoted');
        self::assertSame(404, $this->api('PATCH', '/api/v1/users/99999', $admin, ['display_name' => 'x'])->status());
        $res = $this->api('PATCH', '/api/v1/users/' . $this->cust, $admin, ['email' => 'resell@example.test']);
        self::assertSame(422, $res->status(), 'a taken email is a validation error');
        self::assertArrayHasKey('email', self::json($res)['error']['fields']);
    }

    public function testDeleteUser(): void
    {
        $admin = $this->token($this->admin);
        $res = $this->api('DELETE', '/api/v1/users/' . $this->admin, $admin);
        self::assertSame([409, 'rejected'], [$res->status(), self::json($res)['error']['code']], 'not yourself');
        $res = $this->api('DELETE', '/api/v1/users/' . $this->cust, $admin);
        self::assertSame(200, $res->status());
        self::assertSame($this->cust, self::json($res)['data']['deleted']);
        self::assertNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$this->cust]));
        self::assertSame(404, $this->api('DELETE', '/api/v1/users/' . $this->cust, $admin)->status());
        self::assertSame(404, $this->api('DELETE', '/api/v1/users/' . $this->admin, $this->token($this->reseller))->status());
    }

    public function testPlansAreListedForThoseWhoMayAssignThem(): void
    {
        $res = $this->api('GET', '/api/v1/plans', $this->token($this->admin));
        self::assertSame(200, $res->status());
        $plans = self::json($res)['data'];
        self::assertEqualsCanonicalizing(['Small', 'Big'], array_column($plans, 'name'));
        $small = array_values(array_filter($plans, static fn (array $p): bool => $p['name'] === 'Small'))[0];
        self::assertSame([10240, 1, 1], [$small['limits']['disk_quota_mb'], $small['limits']['max_websites'], $small['users']]);
        self::assertSame([], self::json($this->api('GET', '/api/v1/plans', $this->token($this->reseller)))['data'], 'a reseller has no plans of their own yet');
    }

    public function testCreateAccountWithItsWebsite(): void
    {
        $admin = $this->token($this->admin);
        $res = $this->api('POST', '/api/v1/accounts', $admin, ['username' => 'bluebird', 'email' => 'owner@bluebird.test', 'domain' => 'bluebird.test', 'generate_password' => true, 'plan_id' => $this->small, 'php_version' => '8.3']);
        self::assertSame(201, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame(['bluebird', 'customer', $this->small], [$d['user']['username'], $d['user']['role']['name'], $d['user']['plan_id']]);
        self::assertNotNull($d['website_id']);
        self::assertGreaterThanOrEqual(16, strlen((string) $d['password']));
        $site = $this->db->fetch('SELECT * FROM websites WHERE id = ?', [$d['website_id']]);
        self::assertSame(['bluebird.test', 'bluebird', 'php'], [$site['domain'], $site['system_user'], $site['kind']]);
        self::assertSame((int) $d['user']['id'], (int) $site['owner_id']);

        // Same domain again: refused before anything is created.
        $res = $this->api('POST', '/api/v1/accounts', $admin, ['username' => 'other', 'email' => 'o@bluebird.test', 'domain' => 'bluebird.test', 'generate_password' => true]);
        self::assertSame(422, $res->status());
        self::assertArrayHasKey('domain', self::json($res)['error']['fields']);
        self::assertNull($this->db->scalar("SELECT id FROM users WHERE username = 'other'"));

        // Only administrators create resellers.
        $res = $this->api('POST', '/api/v1/accounts', $this->token($this->reseller), ['username' => 'rr', 'email' => 'rr@x.test', 'generate_password' => true, 'reseller' => true]);
        self::assertSame(422, $res->status());
        self::assertArrayHasKey('reseller', self::json($res)['error']['fields']);
        // A login without a website.
        $res = $this->api('POST', '/api/v1/accounts', $admin, ['username' => 'nosite', 'email' => 'n@x.test', 'generate_password' => true]);
        self::assertSame(201, $res->status(), $res->body());
        self::assertNull(self::json($res)['data']['website_id']);
    }

    public function testSuspendAndResumeAnAccount(): void
    {
        $admin = $this->token($this->admin);
        $users = $this->app->container->get(UserRepository::class);
        $this->app->container->get(WebsiteService::class)->create(['domain' => 'cust.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $users->find($this->cust));
        $res = $this->api('POST', '/api/v1/users/' . $this->cust . '/suspend', $admin, ['reason' => 'Unpaid invoice']);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame([true, 'Unpaid invoice', []], [$d['user']['suspended'], $d['user']['suspend_reason'], $d['warnings']]);
        self::assertGreaterThan(0, $this->calls('sites.apply'));

        $res = $this->api('POST', '/api/v1/users/' . $this->cust . '/unsuspend', $admin);
        self::assertSame(200, $res->status());
        self::assertFalse(self::json($res)['data']['user']['suspended']);
        // Staff are not suspended; a reseller token cannot suspend (administrators only).
        self::assertSame(409, $this->api('POST', '/api/v1/users/' . $this->admin . '/suspend', $admin)->status());
        // A reseller sees their customer but only administrators suspend: refused, nothing changes.
        $res = $this->api('POST', '/api/v1/users/' . $this->sub . '/suspend', $this->token($this->reseller));
        self::assertSame(409, $res->status());
        self::assertFalse((bool) $this->db->scalar('SELECT suspended_at IS NOT NULL FROM users WHERE id = ?', [$this->sub]));
        self::assertSame(404, $this->api('POST', '/api/v1/users/' . $this->cust . '/suspend', $this->token($this->reseller))->status());
    }

    public function testChangeAnAccountsPlan(): void
    {
        $admin = $this->token($this->admin);
        $res = $this->api('PUT', '/api/v1/users/' . $this->cust . '/plan', $admin, ['plan_id' => $this->big]);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame([$this->big, 'Big', 10], [$d['user']['plan_id'], $d['plan']['name'] === 'Big' ? 'Big' : '', $d['plan']['limits']['max_websites']]);
        self::assertSame($this->big, (int) $this->db->scalar('SELECT plan_id FROM users WHERE id = ?', [$this->cust]));
        $res = $this->api('PUT', '/api/v1/users/' . $this->cust . '/plan', $admin, ['plan_id' => 99999]);
        self::assertSame([409, 'rejected'], [$res->status(), self::json($res)['error']['code']]);
        self::assertSame(404, $this->api('PUT', '/api/v1/users/' . $this->cust . '/plan', $this->token($this->reseller), ['plan_id' => $this->big])->status());
    }

    public function testTerminateNeedsTheConfirmationWord(): void
    {
        $admin = $this->token($this->admin);
        $custToken = $this->token($this->cust);
        $resellerToken = $this->token($this->reseller);
        $users = $this->app->container->get(UserRepository::class);
        $this->app->container->get(WebsiteService::class)->create(['domain' => 'one.test', 'kind' => 'static'], (array) $users->find($this->cust));
        self::assertSame(403, $this->api('POST', '/api/v1/users/' . $this->sub . '/terminate', $custToken, ['confirm' => 'TERMINATE'])->status(), 'a customer has no user management');
        foreach ([[], ['confirm' => 'terminate'], ['confirm' => 'yes']] as $body) {
            $res = $this->api('POST', '/api/v1/users/' . $this->cust . '/terminate', $admin, $body + ['x' => 1]);
            self::assertSame(409, $res->status(), json_encode($body));
        }
        self::assertSame(0, $this->calls('sites.delete'), 'nothing is deleted without the word');
        self::assertNotNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$this->cust]));

        $res = $this->api('POST', '/api/v1/users/' . $this->cust . '/terminate', $admin, ['confirm' => 'TERMINATE']);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame($this->cust, self::json($res)['data']['terminated']);
        self::assertStringContainsString('Terminated: 1 website(s)', self::json($res)['data']['message']);
        self::assertNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$this->cust]));
        self::assertSame(1, $this->calls('sites.delete'));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'account.terminate'"));

        // Staff accounts are never terminated this way; a reseller with customers is refused.
        $res = $this->api('POST', '/api/v1/users/' . $this->admin . '/terminate', $admin, ['confirm' => 'TERMINATE']);
        self::assertSame(409, $res->status());
        $res = $this->api('POST', '/api/v1/users/' . $this->reseller . '/terminate', $admin, ['confirm' => 'TERMINATE']);
        self::assertSame(409, $res->status());
        self::assertNotNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$this->reseller]));
        // Not even a reseller terminates their own customer: administrators only.
        self::assertContains($this->api('POST', '/api/v1/users/' . $this->sub . '/terminate', $resellerToken, ['confirm' => 'TERMINATE'])->status(), [403, 409]);
        self::assertNotNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$this->sub]));
    }
}
