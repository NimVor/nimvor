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

/** REST API v1: a website's cron jobs. */
final class CronApiTest extends HttpTestCase
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
        $this->agent->succeed('cron.apply', ['ok' => true]);
        $this->agent->succeed('cron.run', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->agent->succeed('cron.log', ['content' => "### nimvor-run start 2026-09-27T10:00:00Z\nDone\n### nimvor-run end 2026-09-27T10:01:30Z exit=0\n", 'size' => 90, 'truncated' => false]);
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

    public function testCreateChangeAndDeleteAJob(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/cron';
        $res = $this->api('POST', $base, $cust, ['schedule' => '*/15  * * * *', 'command' => 'php artisan schedule:run', 'description' => 'Laravel scheduler']);
        self::assertSame(201, $res->status(), $res->body());
        $j = self::json($res)['data'];
        self::assertSame(['*/15 * * * *', 'php artisan schedule:run', true, false], [$j['schedule'], $j['command'], $j['enabled'], $j['email_output']], 'blanks in the schedule are normalised');
        $sent = $this->lastCall('cron.apply');
        self::assertSame(['shop.test', 'cust'], [$sent['domain'], $sent['user']], 'jobs run as the website\'s system account');
        self::assertSame('*/15 * * * *', $sent['jobs'][0]['schedule']);

        self::assertSame([$j['id']], array_column(self::json($this->api('GET', $base, $cust))['data'], 'id'));
        self::assertSame($j['id'], self::json($this->api('GET', "$base/{$j['id']}", $cust))['data']['id']);

        // PATCH changes only what is sent.
        $res = $this->api('PATCH', "$base/{$j['id']}", $cust, ['enabled' => false]);
        self::assertSame(200, $res->status(), $res->body());
        $p = self::json($res)['data'];
        self::assertSame([false, '*/15 * * * *', 'php artisan schedule:run', 'Laravel scheduler'], [$p['enabled'], $p['schedule'], $p['command'], $p['description']]);
        self::assertFalse($this->lastCall('cron.apply')['jobs'][0]['enabled']);
        self::assertSame('@daily', self::json($this->api('PATCH', "$base/{$j['id']}", $cust, ['schedule' => '@daily']))['data']['schedule']);

        $res = $this->api('DELETE', "$base/{$j['id']}", $cust);
        self::assertSame([200, $j['id']], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame([], $this->lastCall('cron.apply')['jobs']);
        self::assertSame(404, $this->api('GET', "$base/{$j['id']}", $cust)->status());
        self::assertSame(404, $this->api('DELETE', "$base/{$j['id']}", $cust)->status());
    }

    public function testValidationNeverReachesTheServer(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/cron';
        $calls = $this->calls('cron.apply');
        foreach ([
            [['schedule' => 'every day', 'command' => 'ls'], 'schedule'],
            [['schedule' => '* * * * mon', 'command' => 'ls'], 'schedule'],
            [['schedule' => '* * * *', 'command' => 'ls'], 'schedule'],
            [['schedule' => '* * * * *', 'command' => ''], 'command'],
            [['schedule' => '* * * * *', 'command' => "ls\nrm -rf /"], 'command'],
            [['schedule' => '* * * * *', 'command' => str_repeat('a', 2001)], 'command'],
            [['schedule' => '* * * * *', 'command' => 'ls', 'description' => str_repeat('d', 201)], 'description'],
        ] as [$body, $field]) {
            $res = $this->api('POST', $base, $cust, $body);
            self::assertSame(422, $res->status(), json_encode($body));
            self::assertArrayHasKey($field, self::json($res)['error']['fields'], json_encode($body));
        }
        self::assertSame($calls, $this->calls('cron.apply'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM cron_jobs'));
    }

    public function testTheServerRefusingTheCronFileUndoesTheChange(): void
    {
        $cust = $this->token($this->cust);
        $this->agent->fail('cron.apply', 500, 'failed', 'cron file check failed');
        $res = $this->api('POST', '/api/v1/websites/' . $this->site . '/cron', $cust, ['schedule' => '* * * * *', 'command' => 'ls']);
        self::assertSame(502, $res->status());
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM cron_jobs'), 'the job was not kept');
    }

    public function testRunAndReadTheRuns(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/cron';
        $j = self::json($this->api('POST', $base, $cust, ['schedule' => '@hourly', 'command' => 'php cron.php']))['data'];
        $res = $this->api('POST', "$base/{$j['id']}/run", $cust);
        self::assertSame([202, 'aaaaaaaaaaaaaaaa'], [$res->status(), self::json($res)['data']['job_id']]);
        self::assertSame('php cron.php', $this->lastCall('cron.run')['command']);
        // The job can be followed with the jobs endpoint (it was recorded for this user).
        self::assertNotNull($this->db->scalar("SELECT id FROM agent_jobs WHERE job_id = 'aaaaaaaaaaaaaaaa'"));

        $res = $this->api('GET', "$base/{$j['id']}/runs", $cust);
        self::assertSame(200, $res->status(), $res->body());
        $runs = self::json($res)['data']['runs'];
        self::assertSame([0, 'Done'], [$runs[0]['exit'], trim($runs[0]['output'])]);
    }

    public function testScopeAndPermissions(): void
    {
        $admin = $this->token($this->admin);
        $cust = $this->token($this->cust);
        $mine = $this->api('POST', '/api/v1/websites/' . $this->site . '/cron', $cust, ['schedule' => '@daily', 'command' => 'ls']);
        $jid = self::json($mine)['data']['id'];
        $theirs = '/api/v1/websites/' . $this->adminSite . '/cron';
        // Another account's website and jobs are not visible, through any id.
        foreach ([['GET', $theirs], ['POST', $theirs], ['GET', "$theirs/$jid"], ['DELETE', "$theirs/$jid"], ['POST', "$theirs/$jid/run"]] as [$m, $p]) {
            self::assertSame(404, $this->api($m, $p, $cust, ['schedule' => '@daily', 'command' => 'ls'])->status(), $m . ' ' . $p);
        }
        // A job of another website is not reachable through your own website's id.
        $other = self::json($this->api('POST', $theirs, $admin, ['schedule' => '@daily', 'command' => 'ls']))['data']['id'];
        self::assertSame(404, $this->api('GET', '/api/v1/websites/' . $this->site . "/cron/$other", $cust)->status());
        self::assertSame(404, $this->api('DELETE', '/api/v1/websites/' . $this->site . "/cron/$other", $cust)->status());
        self::assertNotNull($this->db->scalar('SELECT id FROM cron_jobs WHERE id = ?', [$other]));
        // Read-only tokens read but never change.
        $read = $this->token($this->admin, 'read');
        self::assertSame(200, $this->api('GET', $theirs, $read)->status());
        foreach ([['POST', $theirs], ['PATCH', "$theirs/$other"], ['DELETE', "$theirs/$other"], ['POST', "$theirs/$other/run"]] as [$m, $p]) {
            self::assertSame(403, $this->api($m, $p, $read, ['x' => 1])->status(), $m . ' ' . $p);
        }
        self::assertSame(401, $this->api('GET', $theirs, null)->status());
    }

    public function testASuspendedAccountsJobsDoNotRun(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/cron';
        $j = self::json($this->api('POST', $base, $cust, ['schedule' => '@daily', 'command' => 'ls']))['data'];
        $this->db->run('UPDATE users SET suspended_at = ? WHERE id = ?', [gmdate('c'), $this->cust]);
        $this->agent->succeed('cron.apply', ['ok' => true]);
        $res = $this->api('POST', "$base/{$j['id']}/run", $this->token($this->admin));
        self::assertSame(409, $res->status(), 'a suspended account\'s cron jobs do not run, not even on request');
        self::assertSame(0, $this->calls('cron.run'));
    }
}
