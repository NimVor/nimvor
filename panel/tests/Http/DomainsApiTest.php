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

/** REST API v1: subdomains, addon domains and redirects. */
final class DomainsApiTest extends HttpTestCase
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
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.3', 'fpm_binary' => '/usr/sbin/php-fpm8.3', 'fpm_service' => 'php8.3-fpm.service']]]);
        $this->site = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($this->cust));
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'php', 'php_version' => '8.3'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($this->admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id = ?', [$this->cust, $this->site]);
        $this->agent->succeed('sites.delete', ['warnings' => []]);
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

    public function testSubdomainsAndAddonDomains(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site;
        self::assertSame([], self::json($this->api('GET', "$base/subdomains", $cust))['data']);

        $res = $this->api('POST', "$base/subdomains", $cust, ['subdomain' => 'Blog', 'dns' => true]);
        self::assertSame(201, $res->status(), $res->body());
        $sub = self::json($res)['data'];
        self::assertSame(['blog.shop.test', 'php', true, false], [$sub['domain'], $sub['kind'], $sub['enabled'], $sub['dns_record_added']], 'no DNS zone for the website: nothing added');
        self::assertStringEndsWith('/blog.shop.test/public_html', $sub['document_root']);
        self::assertSame($sub['id'], (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'blog.shop.test'"));
        self::assertSame((int) $this->site, (int) $this->db->scalar("SELECT parent_id FROM websites WHERE domain = 'blog.shop.test'"));
        self::assertSame((string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$this->site]), (string) $this->db->scalar("SELECT system_user FROM websites WHERE domain = 'blog.shop.test'"), 'it runs as its parent\'s account');

        $res = $this->api('POST', "$base/addons", $cust, ['domain' => 'Harbor-Books.test', 'www' => true]);
        self::assertSame(201, $res->status(), $res->body());
        $add = self::json($res)['data'];
        self::assertSame(['harbor-books.test', ['www.harbor-books.test']], [$add['domain'], $add['aliases']]);

        self::assertSame(['blog.shop.test'], array_column(self::json($this->api('GET', "$base/subdomains", $cust))['data'], 'domain'));
        self::assertSame(['harbor-books.test'], array_column(self::json($this->api('GET', "$base/addons", $cust))['data'], 'domain'));
        // They are websites: the existing endpoints reach them.
        self::assertSame(200, $this->api('GET', '/api/v1/websites/' . $sub['id'], $cust)->status());
        self::assertSame(200, $this->api('DELETE', '/api/v1/websites/' . $sub['id'], $cust)->status());
        self::assertSame([], self::json($this->api('GET', "$base/subdomains", $cust))['data']);
    }

    public function testValidationAndLimits(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site;
        $applies = $this->calls('sites.apply');
        foreach ([[[], 'subdomain'], [['subdomain' => 'www'], 'subdomain'], [['subdomain' => 'bad name'], 'subdomain'], [['subdomain' => '-x'], 'subdomain']] as [$body, $field]) {
            $res = $this->api('POST', "$base/subdomains", $cust, $body);
            self::assertSame(422, $res->status(), json_encode($body));
            self::assertArrayHasKey($field, self::json($res)['error']['fields']);
        }
        foreach ([[[], 'domain'], [['domain' => 'blog.shop.test'], 'domain'], [['domain' => 'shop.test'], 'domain'], [['domain' => 'not a domain'], 'domain'], [['domain' => 'other.test'], 'domain']] as [$body, $field]) {
            $res = $this->api('POST', "$base/addons", $cust, $body);
            self::assertSame(422, $res->status(), json_encode($body) . $res->body());
            self::assertArrayHasKey($field, self::json($res)['error']['fields']);
        }
        self::assertSame($applies, $this->calls('sites.apply'), 'nothing invalid reaches the server');
        self::assertSame(201, $this->api('POST', "$base/subdomains", $cust, ['subdomain' => 'blog'])->status());
        self::assertSame(422, $this->api('POST', "$base/subdomains", $cust, ['subdomain' => 'blog'])->status(), 'the same name twice');
    }

    public function testRedirects(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/redirects';
        $res = $this->api('POST', $base, $cust, ['from' => 'old-menu', 'to' => 'https://shop.test/menu', 'code' => 302]);
        self::assertSame(201, $res->status(), $res->body());
        self::assertSame(['/old-menu', 'https://shop.test/menu', 302], array_values(self::json($res)['data']));
        self::assertSame(['/old-menu'], array_column(self::json($this->api('GET', $base, $cust))['data'], 'from'));

        $applies = $this->calls('sites.apply');
        foreach ([['from' => '/old-menu', 'to' => '/x'], ['from' => '', 'to' => '/x'], ['from' => '/a b', 'to' => '/x'], ['from' => '/a/../b', 'to' => '/x'], ['from' => '/p', 'to' => 'javascript:alert(1)'], ['from' => '/p', 'to' => ''], ['from' => '/p', 'to' => '/p'], ['from' => '/p', 'to' => '/x', 'code' => 307], ['from' => '/q', 'to' => 'https://shop.test/q']] as $bad) {
            self::assertSame(422, $this->api('POST', $base, $cust, $bad)->status(), json_encode($bad));
        }
        self::assertSame($applies, $this->calls('sites.apply'));

        $req = new Request('DELETE', $base, ['from' => '/old-menu'], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $cust]);
        $res = $this->app->handle($req);
        self::assertSame([200, '/old-menu'], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame([], self::json($this->api('GET', $base, $cust))['data']);
        self::assertSame(404, $this->app->handle($req)->status(), 'already gone');
        self::assertSame(422, $this->api('DELETE', $base, $cust)->status(), 'the path must be named');
    }

    public function testTheServerRefusingTheConfigurationUndoesTheChange(): void
    {
        $cust = $this->token($this->cust);
        $this->agent->fail('sites.apply', 500, 'failed', 'nginx -t failed');
        $res = $this->api('POST', '/api/v1/websites/' . $this->site . '/redirects', $cust, ['from' => '/a', 'to' => '/b']);
        self::assertSame(502, $res->status());
        $this->agent->succeed('sites.apply', ['changed' => []]);
        self::assertSame([], self::json($this->api('GET', '/api/v1/websites/' . $this->site . '/redirects', $cust))['data'], 'the redirect was not kept');
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $base = '/api/v1/websites/' . $this->site;
        foreach (['/subdomains', '/addons', '/redirects'] as $p) {
            self::assertSame(200, $this->api('GET', $base . $p, $ro)->status(), $p);
        }
        $applies = $this->calls('sites.apply');
        foreach ([['POST', '/subdomains', ['subdomain' => 'x']], ['POST', '/addons', ['domain' => 'x.test']], ['POST', '/redirects', ['from' => '/a', 'to' => '/b']], ['DELETE', '/redirects', []]] as [$m, $p, $b]) {
            self::assertSame(403, $this->api($m, $base . $p, $ro, $b)->status(), "$m $p");
        }
        $other = '/api/v1/websites/' . $this->adminSite;
        foreach ([['GET', '/subdomains', []], ['POST', '/subdomains', ['subdomain' => 'x']], ['GET', '/addons', []], ['POST', '/addons', ['domain' => 'x.test']], ['GET', '/redirects', []], ['POST', '/redirects', ['from' => '/a', 'to' => '/b']], ['DELETE', '/redirects', []]] as [$m, $p, $b]) {
            self::assertSame(404, $this->api($m, $other . $p, $cust, $b)->status(), "$m $p");
        }
        self::assertSame($applies, $this->calls('sites.apply'));
        self::assertSame(401, $this->api('GET', $base . '/redirects', null)->status());
    }
}
