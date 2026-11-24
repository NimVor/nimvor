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

/** REST API v1: a website's FTP accounts and SFTP access. */
final class FtpApiTest extends HttpTestCase
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
        $this->ftpStatus([['name' => 'designer@shop.test', 'user' => 'cust', 'uid' => 1001, 'dir' => 'public_html', 'quota_mb' => 100]]);
        $this->agent->succeed('ftp.account_save', ['created' => true]);
        $this->agent->succeed('ftp.account_delete', ['deleted' => 'x']);
        $this->agent->succeed('sftp.configure', ['enabled' => true]);
    }

    /** @param list<array<string, mixed>> $accounts */
    private function ftpStatus(array $accounts): void
    {
        $this->agent->succeed('ftp.status', ['installed' => true, 'active' => true, 'settings' => ['tls' => 'required'], 'accounts' => $accounts, 'sftp' => [['user' => 'cust', 'domain' => 'shop.test', 'password_login' => false, 'keys' => [['key' => 'ssh-ed25519 AAAA one@x', 'fingerprint' => 'SHA256:x']]]], 'sftp_root' => '/home/nimvor/sites']);
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

    public function testListCreateChangeAndDelete(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/ftp';
        $res = $this->api('GET', $base, $cust);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame([true, 'required', 'designer@shop.test', 100], [$d['server_ready'], $d['tls'], $d['accounts'][0]['login'], $d['accounts'][0]['quota_mb']]);
        self::assertSame('cust', $this->lastCall('ftp.status')['user'], 'only this website\'s accounts are asked for');

        $res = $this->api('POST', $base, $cust, ['name' => 'Dev', 'dir' => '/public_html/', 'password' => 'a-long-password', 'quota_mb' => 200]);
        self::assertSame(201, $res->status(), $res->body());
        $c = self::json($res)['data'];
        self::assertSame(['dev@shop.test', 'public_html', 200], [$c['login'], $c['dir'], $c['quota_mb']]);
        self::assertArrayNotHasKey('password', $c, 'a given password is never echoed');
        $sent = $this->lastCall('ftp.account_save');
        self::assertSame(['dev@shop.test', 'cust', 'public_html', 200, 'a-long-password'], [$sent['name'], $sent['user'], $sent['dir'], $sent['quota_mb'], $sent['password']]);
        $audit = (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'ftp.account_create'");
        self::assertStringContainsString('dev@shop.test', $audit);
        self::assertStringNotContainsString('a-long-password', $audit);

        // A generated password is returned once.
        $res = $this->api('POST', $base, $cust, ['name' => 'gen', 'generate' => true]);
        $pw = self::json($res)['data']['password'];
        self::assertSame($pw, $this->lastCall('ftp.account_save')['password']);
        self::assertGreaterThanOrEqual(20, strlen($pw));

        // PATCH changes only what is sent (the folder stays).
        $res = $this->api('PATCH', $base . '/designer', $cust, ['quota_mb' => 1000]);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('ftp.account_save');
        self::assertSame(['designer@shop.test', 'public_html', 1000], [$sent['name'], $sent['dir'], $sent['quota_mb']]);
        self::assertArrayNotHasKey('password', $sent, 'the password stays as it is');
        self::assertSame(200, $this->api('PATCH', $base . '/designer', $cust, ['password' => 'another-long-password'])->status());
        self::assertSame('another-long-password', $this->lastCall('ftp.account_save')['password']);

        $res = $this->api('DELETE', $base . '/designer', $cust);
        self::assertSame([200, 'designer@shop.test'], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame(['designer@shop.test', 'cust'], [$this->lastCall('ftp.account_delete')['name'], $this->lastCall('ftp.account_delete')['user']]);
    }

    public function testValidationNeverReachesTheServer(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/ftp';
        $calls = $this->calls('ftp.account_save');
        foreach ([
            [['name' => 'Bad Name', 'password' => 'a-long-password'], 'name'],
            [['name' => '', 'password' => 'a-long-password'], 'name'],
            [['name' => str_repeat('a', 40), 'password' => 'a-long-password'], 'name'],
            [['name' => 'dev', 'password' => 'short'], 'password'],
            [['name' => 'dev'], 'password'],
            [['name' => 'dev', 'password' => 'a-long-password', 'dir' => '../other'], 'dir'],
        ] as [$body, $field]) {
            $res = $this->api('POST', $base, $cust, $body);
            self::assertSame(422, $res->status(), json_encode($body));
            self::assertArrayHasKey($field, self::json($res)['error']['fields'], json_encode($body));
        }
        self::assertSame($calls, $this->calls('ftp.account_save'));
    }

    public function testAccountsOfOtherWebsitesCannotBeReached(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/ftp';
        $saves = $this->calls('ftp.account_save');
        $deletes = $this->calls('ftp.account_delete');
        // Not in this website's list: not found, nothing is sent.
        self::assertSame(404, $this->api('DELETE', $base . '/admin', $cust)->status());
        self::assertSame(404, $this->api('PATCH', $base . '/admin', $cust, ['quota_mb' => 5])->status());
        self::assertSame($saves, $this->calls('ftp.account_save'));
        self::assertSame($deletes, $this->calls('ftp.account_delete'));
        // The other website is not visible at all.
        $other = '/api/v1/websites/' . $this->adminSite;
        self::assertSame(404, $this->api('GET', $other . '/ftp', $cust)->status());
        self::assertSame(404, $this->api('POST', $other . '/ftp', $cust, ['name' => 'x', 'password' => 'a-long-password'])->status());
        self::assertSame(404, $this->api('GET', $other . '/sftp', $cust)->status());
    }

    public function testTheMaximumNumberOfAccountsIsEnforced(): void
    {
        $many = [];
        for ($i = 0; $i < 25; $i++) {
            $many[] = ['name' => "a$i@shop.test", 'user' => 'cust', 'dir' => '', 'quota_mb' => 0];
        }
        $this->ftpStatus($many);
        $res = $this->api('POST', '/api/v1/websites/' . $this->site . '/ftp', $this->token($this->cust), ['name' => 'one-more', 'password' => 'a-long-password']);
        self::assertSame(409, $res->status(), $res->body());
        self::assertSame('rejected', self::json($res)['error']['code']);
    }

    public function testSftpAccess(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/sftp';
        $d = self::json($this->api('GET', $base, $cust))['data'];
        self::assertSame([false, false, ['ssh-ed25519 AAAA one@x'], '/home/nimvor/sites'], [$d['enabled'], $d['password_login'], $d['keys'], $d['root']]);

        $res = $this->api('PUT', $base, $cust, ['enabled' => true, 'keys' => ["ssh-ed25519 AAAA one@x", "# comment", "ssh-ed25519 BBBB two@x", ""]]);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('sftp.configure');
        self::assertSame(['cust', true, false, ['ssh-ed25519 AAAA one@x', 'ssh-ed25519 BBBB two@x']], [$sent['user'], $sent['enabled'], $sent['password_login'], $sent['keys']]);
        self::assertArrayNotHasKey('password', $sent);

        // Only what is sent changes: the keys stay.
        $this->api('PUT', $base, $cust, ['password_login' => true, 'password' => 'a-long-password']);
        $sent = $this->lastCall('sftp.configure');
        self::assertSame([true, ['ssh-ed25519 AAAA one@x'], 'a-long-password'], [$sent['password_login'], $sent['keys'], $sent['password']]);
        $audit = (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'sftp.configure' ORDER BY id DESC LIMIT 1");
        self::assertStringNotContainsString('a-long-password', $audit);

        $calls = $this->calls('sftp.configure');
        $res = $this->api('PUT', $base, $cust, ['password' => 'short']);
        self::assertSame(422, $res->status());
        self::assertSame($calls, $this->calls('sftp.configure'));
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $base = '/api/v1/websites/' . $this->site;
        self::assertSame(200, $this->api('GET', $base . '/ftp', $ro)->status());
        $saves = $this->calls('ftp.account_save');
        foreach ([['POST', '/ftp', ['name' => 'x', 'password' => 'a-long-password']], ['PATCH', '/ftp/designer', ['quota_mb' => 1]], ['DELETE', '/ftp/designer', []], ['PUT', '/sftp', ['enabled' => true]]] as [$m, $p, $b]) {
            self::assertSame(403, $this->api($m, $base . $p, $ro, $b)->status(), "$m $p");
        }
        self::assertSame($saves, $this->calls('ftp.account_save'));
        self::assertSame(401, $this->api('GET', $base . '/ftp', null)->status());
        self::assertSame(200, $this->api('GET', $base . '/ftp', $cust)->status());
    }
}
