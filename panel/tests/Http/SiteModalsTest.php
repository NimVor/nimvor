<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** The quick-settings windows opened from the Websites list's three-dot menu. */
final class SiteModalsTest extends HttpTestCase
{
    private int $siteId;
    private int $staticId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $owner = ['role_scope' => 'all', 'id' => 1] + (array) $users->find($cust);
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3', 'max_children' => '7'], $owner);
        $this->staticId = $sites->create(['domain' => 'plain.test', 'kind' => 'static'], $owner);
        $this->db = $this->app->container->get(Connection::class);
    }

    private function switchTo(string $user): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testTheMenuOffersPhpFpmToAdministratorsOnly(): void
    {
        $this->login('admin');
        $this->agent->succeed('discovery.inventory', ['websites' => ['nginx_version' => '1.24.0', 'catch_all_vhosts' => 0, 'warnings' => [], 'websites' => []]]);
        $page = $this->request('GET', '/websites', [], [], '', ['view' => 'list'])->body();
        self::assertStringContainsString('/sites/' . $this->siteId . '/modal/php', $page);
        self::assertStringContainsString('id="nim-site-modal"', $page);
        self::assertStringNotContainsString('/sites/' . $this->staticId . '/modal/php', $page, 'a static website has no PHP-FPM');
        $this->switchTo('cust');
        $this->agent->succeed('discovery.inventory', ['websites' => ['nginx_version' => '1.24.0', 'catch_all_vhosts' => 0, 'warnings' => [], 'websites' => []]]);
        self::assertStringNotContainsString('/modal/php', $this->request('GET', '/websites', [], [], '', ['view' => 'list'])->body());
    }

    public function testThePhpFpmWindowCarriesTheCurrentSettingsAndIsRefusedToOthers(): void
    {
        $this->login('admin');
        $res = $this->request('GET', '/sites/' . $this->siteId . '/modal/php');
        self::assertSame(200, $res->status());
        $body = $res->body();
        foreach (['name="php_version"', 'name="pm"', 'name="max_children"', 'value="7"', 'data-nim-site-modal-form', 'name="_token"', 'shop.test'] as $want) {
            self::assertStringContainsString($want, $body, $want);
        }
        self::assertStringNotContainsString('<html', $body, 'only the window, not a page');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->staticId . '/modal/php')->status());
        self::assertSame(404, $this->request('GET', '/sites/' . $this->siteId . '/modal/nothing')->status());
        $this->switchTo('cust');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->siteId . '/modal/php')->status());
    }

    public function testTheWindowSavesInTheBackgroundAndReportsRefusedFields(): void
    {
        $this->login('admin');
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $token = ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
        $json = ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest'];
        $res = $this->request('POST', '/sites/' . $this->siteId . '/php', ['php_version' => '8.3', 'pm' => 'dynamic', 'max_children' => '20', 'max_requests' => '300'] + $token, $json);
        self::assertSame(200, $res->status());
        self::assertTrue((bool) json_decode($res->body(), true)['success']);
        $s = (array) json_decode((string) $this->db->scalar('SELECT php_settings FROM websites WHERE id = ?', [$this->siteId]), true);
        self::assertSame([20, 'dynamic', 300], [$s['max_children'], $s['pm'], $s['max_requests']]);
        self::assertSame(20, $this->lastCall('sites.apply')['php']['max_children']);

        $calls = $this->calls('sites.apply');
        $bad = $this->request('POST', '/sites/' . $this->siteId . '/php', ['php_version' => '8.3', 'max_children' => '9999'] + $token, $json);
        self::assertSame(422, $bad->status());
        $out = json_decode($bad->body(), true);
        self::assertFalse($out['success']);
        self::assertArrayHasKey('max_children', $out['error']['errors']);
        self::assertSame($calls, $this->calls('sites.apply'), 'nothing is applied after a refused value');
    }

    public function testTheNginxWindowIsOfferedToAdministratorsForEveryKindOfWebsite(): void
    {
        $this->login('admin');
        $this->agent->succeed('discovery.inventory', ['websites' => ['nginx_version' => '1.24.0', 'catch_all_vhosts' => 0, 'warnings' => [], 'websites' => []]]);
        $page = $this->request('GET', '/websites', [], [], '', ['view' => 'list'])->body();
        self::assertStringContainsString('/sites/' . $this->siteId . '/modal/nginx', $page);
        self::assertStringContainsString('/sites/' . $this->staticId . '/modal/nginx', $page);
        $res = $this->request('GET', '/sites/' . $this->staticId . '/modal/nginx');
        self::assertSame(200, $res->status());
        foreach (['name="read_timeout_seconds"', 'name="gzip"', 'name="extra"', 'data-nim-site-modal-form', 'plain.test'] as $want) {
            self::assertStringContainsString($want, $res->body(), $want);
        }
        self::assertStringNotContainsString('name="fastcgi_buffer_kb"', $res->body(), 'a static website has no PHP response buffer');
        self::assertStringContainsString('name="fastcgi_buffer_kb"', $this->request('GET', '/sites/' . $this->siteId . '/modal/nginx')->body());
        $this->switchTo('cust');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->siteId . '/modal/nginx')->status());
    }

    public function testNginxTuningIsSavedSentToTheAgentAndRefusedWhenUnsafe(): void
    {
        $this->login('admin');
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $token = ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
        $json = ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest'];
        $ok = $this->request('POST', '/sites/' . $this->siteId . '/nginx', ['read_timeout_seconds' => '120', 'gzip' => 'on', 'gzip_level' => '5', 'extra' => "client_body_buffer_size 256k;\n\ntcp_nodelay on"] + $token, $json);
        self::assertSame(200, $ok->status());
        $sent = $this->lastCall('sites.apply')['nginx'];
        self::assertSame([120, true, 5, ['client_body_buffer_size 256k', 'tcp_nodelay on']], [$sent['read_timeout_seconds'], $sent['gzip'], $sent['gzip_level'], $sent['extra']]);
        self::assertStringContainsString('client_body_buffer_size 256k', $this->request('GET', '/sites/' . $this->siteId . '/modal/nginx')->body());

        $calls = $this->calls('sites.apply');
        foreach (['root /etc', 'gzip_types $host', 'include /etc/passwd'] as $bad) {
            $res = $this->request('POST', '/sites/' . $this->siteId . '/nginx', ['extra' => $bad] + $token, $json);
            self::assertSame(422, $res->status(), $bad);
            self::assertArrayHasKey('extra', json_decode($res->body(), true)['error']['errors']);
        }
        $res = $this->request('POST', '/sites/' . $this->staticId . '/nginx', ['fastcgi_buffer_kb' => '16'] + $token, $json);
        self::assertSame(422, $res->status());
        self::assertSame($calls, $this->calls('sites.apply'), 'a refused value reaches no server');

        $this->switchTo('cust');
        $token = ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
        self::assertSame(404, $this->request('POST', '/sites/' . $this->siteId . '/nginx', ['gzip' => 'off'] + $token, $json)->status());
    }
}
