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

/** REST API v1: a website's backups, restore and schedules. */
final class BackupsApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    private int $site;
    private int $adminSite;
    private int $manual;
    private int $auto;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->site = $sites->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($this->cust));
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($this->admin));
        $this->db = $this->app->container->get(Connection::class);
        $manifest = json_encode(['files' => [['name' => 'files.tar.gz.enc', 'kind' => 'files', 'size' => 1024], ['name' => 'db-shop_db.sql.gz.enc', 'kind' => 'database', 'database' => 'shop_db', 'size' => 512]]]);
        $this->db->run("INSERT INTO backups (server_id, website_id, kind, domain, backup_id, label, manifest, size, encrypted, local, status, created_at) VALUES (1, ?, 'site', 'shop.test', '20260901T030000Z-a1b2c3', 'before upgrade', ?, 1536, 1, 1, 'completed', '2026-09-01T03:00:00Z')", [$this->site, $manifest]);
        $this->manual = (int) $this->db->scalar('SELECT MAX(id) FROM backups');
        $this->db->run("INSERT INTO backup_schedules (server_id, website_id, kind, frequency, hour, retention, enabled, created_at) VALUES (1, ?, 'site', 'daily', 3, 7, 1, '2026-08-01T00:00:00Z')", [$this->site]);
        $sched = (int) $this->db->scalar('SELECT MAX(id) FROM backup_schedules');
        $this->db->run("INSERT INTO backups (server_id, website_id, schedule_id, kind, domain, backup_id, manifest, size, encrypted, local, status, created_at) VALUES (1, ?, ?, 'site', 'shop.test', '20260902T030000Z-d4e5f6', ?, 1536, 1, 1, 'completed', '2026-09-02T03:00:00Z')", [$this->site, $sched, $manifest]);
        $this->auto = (int) $this->db->scalar('SELECT MAX(id) FROM backups');
        $this->agent->succeed('backup.restore', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $this->agent->succeed('backup.delete', ['deleted' => true]);
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

    public function testReadAndDeleteABackup(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/backups';
        $res = $this->api('GET', "$base/{$this->manual}", $cust);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame(['completed', true, false, true, 'before upgrade'], [$d['status'], $d['encrypted'], $d['scheduled'], $d['on_server'], $d['label']]);
        self::assertSame(['files', 'database'], array_column($d['parts'], 'kind'));
        self::assertSame('shop_db', $d['parts'][1]['database']);

        $res = $this->api('DELETE', "$base/{$this->manual}", $cust);
        self::assertSame([200, $this->manual], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame(['shop.test', '20260901T030000Z-a1b2c3'], [$this->lastCall('backup.delete')['domain'], $this->lastCall('backup.delete')['id']]);
        self::assertSame(404, $this->api('GET', "$base/{$this->manual}", $cust)->status());
        self::assertSame(404, $this->api('DELETE', "$base/{$this->manual}", $cust)->status());
    }

    public function testRestoreNeedsTheDomainAndOnlyNamedParts(): void
    {
        $cust = $this->token($this->cust);
        $url = '/api/v1/websites/' . $this->site . '/backups/' . $this->manual . '/restore';
        $calls = $this->calls('backup.restore');
        foreach ([['confirm' => 'other.test', 'files' => true], ['files' => true], ['confirm' => 'shop.test'], ['confirm' => 'shop.test', 'databases' => ['nope']]] as $body) {
            $res = $this->api('POST', $url, $cust, $body);
            self::assertSame(409, $res->status(), json_encode($body) . $res->body());
            self::assertSame('rejected', self::json($res)['error']['code']);
        }
        self::assertSame($calls, $this->calls('backup.restore'), 'nothing reaches the server without a confirmed, valid request');

        $res = $this->api('POST', $url, $cust, ['confirm' => 'shop.test', 'files' => true, 'databases' => ['shop_db']]);
        self::assertSame([202, 'bbbbbbbbbbbbbbbb'], [$res->status(), self::json($res)['data']['job_id'] ?? null], $res->body());
        $sent = $this->lastCall('backup.restore');
        self::assertSame(['shop.test', true, true], [$sent['domain'], $sent['files'], $sent['safety_backup']]);
        self::assertNotNull($this->db->scalar("SELECT id FROM agent_jobs WHERE job_id = 'bbbbbbbbbbbbbbbb'"), 'the restore can be followed with the jobs endpoint');
    }

    public function testScheduledCopiesAreHiddenFromCustomersWithoutTheFeature(): void
    {
        $cust = $this->token($this->cust);
        $admin = $this->token($this->admin);
        $base = '/api/v1/websites/' . $this->site . '/backups';
        // The list (existing endpoint) and the new ones agree.
        self::assertSame([$this->manual], array_column(self::json($this->api('GET', $base, $cust))['data'], 'id'));
        foreach ([['GET', ''], ['DELETE', ''], ['POST', '/restore']] as [$m, $s]) {
            self::assertSame(404, $this->api($m, "$base/{$this->auto}$s", $cust, ['confirm' => 'shop.test', 'files' => true])->status(), "$m $s");
        }
        self::assertSame([], self::json($this->api('GET', '/api/v1/websites/' . $this->site . '/backup-schedules', $cust))['data']);
        // An administrator sees everything.
        self::assertSame(200, $this->api('GET', "$base/{$this->auto}", $admin)->status());
        self::assertTrue(self::json($this->api('GET', "$base/{$this->auto}", $admin))['data']['scheduled']);
        self::assertCount(1, self::json($this->api('GET', '/api/v1/websites/' . $this->site . '/backup-schedules', $admin))['data']);
    }

    public function testOnlyAdministratorsManageSchedules(): void
    {
        $cust = $this->token($this->cust);
        $admin = $this->token($this->admin);
        $base = '/api/v1/websites/' . $this->site . '/backup-schedules';
        $res = $this->api('POST', $base, $cust, ['frequency' => 'daily']);
        self::assertSame(403, $res->status());
        self::assertSame(403, $this->api('DELETE', "$base/1", $cust)->status());
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM backup_schedules'));

        $res = $this->api('POST', $base, $admin, ['frequency' => 'weekly', 'hour' => 4, 'weekday' => 2, 'retention' => 10, 'include_databases' => false]);
        self::assertSame(201, $res->status(), $res->body());
        $s = self::json($res)['data'];
        self::assertSame(['weekly', 4, 2, 10, true, false], [$s['frequency'], $s['hour'], $s['weekday'], $s['retention'], $s['include_files'], $s['include_databases']]);

        foreach ([['frequency' => 'hourly'], ['hour' => 24], ['retention' => 0], ['include_files' => false, 'include_databases' => false]] as $bad) {
            self::assertSame(422, $this->api('POST', $base, $admin, $bad)->status(), json_encode($bad));
        }
        $res = $this->api('DELETE', $base . '/' . $s['id'], $admin);
        self::assertSame([200, $s['id']], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame(404, $this->api('DELETE', $base . '/' . $s['id'], $admin)->status());
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $base = '/api/v1/websites/' . $this->site;
        self::assertSame(200, $this->api('GET', "$base/backups/{$this->manual}", $ro)->status());
        foreach ([['DELETE', "/backups/{$this->manual}", []], ['POST', "/backups/{$this->manual}/restore", ['confirm' => 'shop.test', 'files' => true]], ['POST', '/backup-schedules', []]] as [$m, $p, $b]) {
            self::assertSame(403, $this->api($m, $base . $p, $ro, $b)->status(), "$m $p");
        }
        self::assertSame(401, $this->api('GET', "$base/backups/{$this->manual}", null)->status());
        // Another customer's website: its backups and schedules are not visible.
        $other = '/api/v1/websites/' . $this->adminSite;
        self::assertSame(404, $this->api('GET', "$other/backups/{$this->manual}", $cust)->status());
        self::assertSame(404, $this->api('GET', "$other/backup-schedules", $cust)->status());
        // A backup of another website is not reachable through this one.
        $this->db->run("INSERT INTO backups (server_id, website_id, kind, domain, backup_id, manifest, status, created_at) VALUES (1, ?, 'site', 'other.test', 'x', '{}', 'completed', '2026-09-03')", [$this->adminSite]);
        $foreign = (int) $this->db->scalar('SELECT MAX(id) FROM backups');
        self::assertSame(404, $this->api('GET', "$base/backups/$foreign", $cust)->status());
        self::assertSame(404, $this->api('DELETE', "$base/backups/$foreign", $cust)->status());
    }
}
