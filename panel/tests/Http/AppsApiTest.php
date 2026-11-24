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

/** REST API v1: the application installer. */
final class AppsApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    private int $site;
    private int $adminSite;
    private int $staticSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.3', 'fpm_binary' => '/usr/sbin/php-fpm8.3', 'fpm_service' => 'php8.3-fpm.service']]]);
        $this->site = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($this->cust));
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'php', 'php_version' => '8.3'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($this->admin));
        $this->staticSite = $sites->create(['domain' => 'static.test', 'kind' => 'static'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($this->cust));
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id IN (?, ?)', [$this->cust, $this->site, $this->staticSite]);
        foreach (['db.create', 'db.user_create', 'db.grant', 'db.drop', 'db.user_drop'] as $a) {
            $this->agent->succeed($a, ['ok' => true]);
        }
        $this->agent->succeed('apps.install', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->agent->succeed('apps.plan', ['ok' => true, 'changes' => ['Download WordPress']]);
        $this->agent->succeed('jobs.get', ['id' => 'aaaaaaaaaaaaaaaa', 'status' => 'running']);
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

    public function testCatalogForTheTokensUser(): void
    {
        $d = self::json($this->api('GET', '/api/v1/apps', $this->token($this->cust)))['data'];
        self::assertTrue($d['installer_enabled']);
        $slugs = array_merge(...array_map(static fn (array $c): array => array_column($c['apps'], 'slug'), $d['categories']));
        self::assertContains('wordpress', $slugs);
        self::assertSame(['slug', 'name', 'description', 'needs_database'], array_keys($d['categories'][0]['apps'][0]));
    }

    public function testInstallWordPressCreatesTheDatabaseAndReturnsThePasswordOnce(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/apps';
        $calls = $this->calls('apps.install');
        foreach ([['title' => 'Shop', 'admin_user' => 'owner', 'admin_email' => 'nope'], ['title' => 'Shop', 'admin_user' => 'x', 'admin_email' => 'o@shop.test'], ['title' => str_repeat('t', 101), 'admin_user' => 'owner', 'admin_email' => 'o@shop.test']] as $bad) {
            self::assertSame(422, $this->api('POST', "$base/wordpress", $cust, $bad)->status(), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('apps.install'));
        self::assertSame(0, $this->calls('db.create'), 'nothing is created for invalid input');

        $res = $this->api('POST', "$base/wordpress", $cust, ['title' => 'Shop', 'admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        self::assertSame(202, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame(['aaaaaaaaaaaaaaaa', $this->site, 'owner'], [$d['job_id'], $d['website_id'], $d['admin_user']]);
        $sent = $this->lastCall('apps.install');
        self::assertSame(['public_html', 'http://shop.test', $d['admin_password']], [$sent['path'], $sent['url'], $sent['admin_password']]);
        self::assertGreaterThanOrEqual(20, strlen($d['admin_password']));
        self::assertSame('cust_shop_wp', $sent['db']['name'], 'the database is named for the customer');
        self::assertStringNotContainsString($d['admin_password'], (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs')));
        self::assertNotNull($this->db->scalar("SELECT id FROM agent_jobs WHERE job_id = 'aaaaaaaaaaaaaaaa'"), 'the job can be followed');

        // The history shows it, without the password.
        $list = self::json($this->api('GET', $base, $cust))['data'];
        self::assertSame(['wordpress', 'running', 'owner'], [$list[0]['app'], $list[0]['status'], $list[0]['admin_user']]);
        self::assertStringNotContainsString($d['admin_password'], (string) json_encode($list));
        self::assertNotNull($list[0]['database']);

        // The same folder cannot be installed into twice at once: no second database or job.
        $dbs = (int) $this->db->scalar('SELECT COUNT(*) FROM databases');
        $jobs = $this->calls('apps.install');
        $res = $this->api('POST', "$base/wordpress", $cust, ['title' => 'Shop', 'admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        self::assertSame(409, $res->status(), $res->body());
        self::assertSame([$dbs, $jobs], [(int) $this->db->scalar('SELECT COUNT(*) FROM databases'), $this->calls('apps.install')]);
    }

    public function testApplicationsMustFitTheWebsiteAndTheCatalog(): void
    {
        $cust = $this->token($this->cust);
        $calls = $this->calls('apps.install');
        // A PHP application on a static website.
        self::assertSame(409, $this->api('POST', '/api/v1/websites/' . $this->staticSite . '/apps/wordpress', $cust, ['title' => 'x', 'admin_user' => 'owner', 'admin_email' => 'o@x.test'])->status());
        self::assertSame(409, $this->api('GET', '/api/v1/websites/' . $this->staticSite . '/apps/wordpress/plan', $cust)->status());
        // Something that is not in the catalog.
        self::assertSame(409, $this->api('POST', '/api/v1/websites/' . $this->site . '/apps/not-an-app', $cust, [])->status());
        self::assertSame($calls, $this->calls('apps.install'));
        // Installer switched off by the provider.
        $this->app->container->get(\NimVor\Services\AppCatalogService::class)->setInstallerEnabled(false, null);
        $res = $this->api('POST', '/api/v1/websites/' . $this->site . '/apps/wordpress', $cust, ['title' => 'x', 'admin_user' => 'owner', 'admin_email' => 'o@shop.test']);
        self::assertSame(409, $res->status());
        self::assertFalse(self::json($this->api('GET', '/api/v1/apps', $cust))['data']['installer_enabled']);
        self::assertSame($calls, $this->calls('apps.install'));
    }

    public function testPlanIsADryRun(): void
    {
        $cust = $this->token($this->cust);
        $res = $this->api('GET', '/api/v1/websites/' . $this->site . '/apps/wordpress/plan', $cust);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame(['Download WordPress'], self::json($res)['data']['changes']);
        self::assertSame(0, $this->calls('apps.install') + $this->calls('db.create'), 'nothing is created');
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM app_installs'));
    }

    public function testInstallIntoASubdomainOnlyOfThisWebsite(): void
    {
        $cust = $this->token($this->cust);
        $parent = (array) $this->app->container->get(\NimVor\Repositories\WebsiteRepository::class)->find(1, $this->site, null);
        $sub = $this->app->container->get(WebsiteService::class)->createChild($parent, 'subdomain', ['subdomain' => 'blog'], ['role_scope' => 'all', 'id' => 1])['id'];
        $base = '/api/v1/websites/' . $this->site . '/apps/wordpress';
        $res = $this->api('POST', $base, $cust, ['target_id' => $sub, 'title' => 'Blog', 'admin_user' => 'owner', 'admin_email' => 'o@shop.test']);
        self::assertSame([202, $sub], [$res->status(), self::json($res)['data']['website_id']], $res->body());
        self::assertSame('http://blog.shop.test', $this->lastCall('apps.install')['url']);
        // Another website (not a subdomain of this one) is refused, nothing runs.
        $n = $this->calls('apps.install');
        self::assertSame(409, $this->api('POST', $base, $cust, ['target_id' => $this->staticSite, 'title' => 'x', 'admin_user' => 'owner', 'admin_email' => 'o@shop.test'])->status());
        self::assertSame($n, $this->calls('apps.install'));
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $base = '/api/v1/websites/' . $this->site . '/apps';
        self::assertSame(200, $this->api('GET', '/api/v1/apps', $ro)->status());
        self::assertSame(200, $this->api('GET', $base, $ro)->status());
        $n = $this->calls('apps.install');
        self::assertSame(403, $this->api('POST', "$base/wordpress", $ro, ['title' => 'x'])->status());
        $other = '/api/v1/websites/' . $this->adminSite . '/apps';
        foreach ([['GET', ''], ['GET', '/wordpress/plan'], ['POST', '/wordpress']] as [$m, $p]) {
            self::assertSame(404, $this->api($m, $other . $p, $cust, ['title' => 'x', 'admin_user' => 'owner', 'admin_email' => 'o@shop.test'])->status(), "$m $p");
        }
        self::assertSame($n, $this->calls('apps.install'));
        self::assertSame(401, $this->api('GET', '/api/v1/apps', null)->status());
    }
}
