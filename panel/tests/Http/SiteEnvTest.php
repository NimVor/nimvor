<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\SiteEnvRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** A website's environment variables: the Environment page, the Websites list window and delivery to the agent. */
final class SiteEnvTest extends HttpTestCase
{
    private int $siteId;
    private int $otherId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $sites = $this->app->container->get(WebsiteService::class);
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3', 'owner_id' => (string) $cust], ['role_scope' => 'all', 'id' => 1]);
        $this->otherId = $sites->create(['domain' => 'else.test', 'kind' => 'php', 'php_version' => '8.3', 'owner_id' => (string) $other], ['role_scope' => 'all', 'id' => 1]);
        $this->db = $this->app->container->get(Connection::class);
    }

    private function switchTo(string $user): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    /** @return array<string, string> */
    private function json(): array
    {
        return ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest'];
    }

    /** @return array<string, string> */
    private function token(): array
    {
        return ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
    }

    public function testTheOwnerSavesVariablesWhichAreStoredEncryptedAndSentToTheAgent(): void
    {
        $this->switchTo('cust');
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $text = "# payments\nSTRIPE_KEY=sk_live_abc123\nexport APP_ENV=\"production\"\nGREETING=' hello '\n";
        $res = $this->request('POST', '/sites/' . $this->siteId . '/env', ['env' => $text] + $this->token(), $this->json());
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame(['STRIPE_KEY' => 'sk_live_abc123', 'APP_ENV' => 'production', 'GREETING' => ' hello '], $this->lastCall('sites.apply')['env']);

        $raw = (string) $this->db->scalar('SELECT vars_enc FROM site_env WHERE website_id = ?', [$this->siteId]);
        self::assertStringStartsWith('v1:', $raw);
        self::assertStringNotContainsString('sk_live_abc123', $raw);
        self::assertSame('sk_live_abc123', $this->app->container->get(SiteEnvRepository::class)->get($this->siteId)['STRIPE_KEY']);

        $page = $this->request('GET', '/sites/' . $this->siteId . '/env')->body();
        self::assertStringContainsString('STRIPE_KEY=sk_live_abc123', $page);
        self::assertStringContainsString('/sites/' . $this->siteId . '/env', $page);

        $audit = (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'website.env' ORDER BY id DESC LIMIT 1");
        self::assertStringContainsString('STRIPE_KEY', $audit);
        self::assertStringNotContainsString('sk_live_abc123', $audit, 'values never reach the audit log');
    }

    public function testAnAdministratorUsesTheListWindow(): void
    {
        $this->login('admin');
        $this->agent->succeed('discovery.inventory', ['websites' => ['nginx_version' => '1.24.0', 'catch_all_vhosts' => 0, 'warnings' => [], 'websites' => []]]);
        $page = $this->request('GET', '/websites', [], [], '', ['view' => 'list'])->body();
        self::assertStringContainsString('/sites/' . $this->siteId . '/env/window', $page);
        $win = $this->request('GET', '/sites/' . $this->siteId . '/env/window');
        self::assertSame(200, $win->status());
        foreach (['name="env"', 'data-nim-site-modal-form', 'shop.test'] as $want) {
            self::assertStringContainsString($want, $win->body(), $want);
        }
        $this->agent->succeed('sites.apply', ['changed' => []]);
        self::assertSame(200, $this->request('POST', '/sites/' . $this->siteId . '/env', ['env' => 'API_URL=https://x.test/v1'] + $this->token(), $this->json())->status());
        self::assertSame('https://x.test/v1', $this->lastCall('sites.apply')['env']['API_URL']);
    }

    public function testBadLinesAreRefusedAndNothingIsSent(): void
    {
        $this->switchTo('cust');
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $before = $this->calls('sites.apply');
        foreach (['lower=1', 'PATH=/tmp', 'LD_PRELOAD=/x.so', 'PHP_INI_SCAN_DIR=/x', "KEY=it's", 'NOEQUALS', "A=1\nA=2", 'HTTP_HOST=evil'] as $bad) {
            $res = $this->request('POST', '/sites/' . $this->siteId . '/env', ['env' => $bad] + $this->token(), $this->json());
            self::assertSame(422, $res->status(), $bad);
            self::assertArrayHasKey('env', json_decode($res->body(), true)['error']['errors']);
        }
        self::assertSame($before, $this->calls('sites.apply'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_env'));
    }

    public function testAnAgentRefusalPutsThePreviousVariablesBack(): void
    {
        $this->switchTo('cust');
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->request('POST', '/sites/' . $this->siteId . '/env', ['env' => 'KEEP=1'] + $this->token(), $this->json());
        $this->agent->fail('sites.apply', 400, 'invalid_request', 'refused');
        $res = $this->request('POST', '/sites/' . $this->siteId . '/env', ['env' => 'KEEP=2'] + $this->token(), $this->json());
        self::assertNotSame(200, $res->status());
        self::assertSame(['KEEP' => '1'], $this->app->container->get(SiteEnvRepository::class)->get($this->siteId));
    }

    public function testAnotherCustomersWebsiteIsNotFound(): void
    {
        $this->switchTo('cust');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherId . '/env')->status());
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherId . '/env/window')->status());
        self::assertSame(404, $this->request('POST', '/sites/' . $this->otherId . '/env', ['env' => 'A=1'] + $this->token(), $this->json())->status());
    }

    public function testBackgroundWorkersReceiveTheWebsitesVariables(): void
    {
        $this->switchTo('cust');
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('workers.apply', ['ok' => true]);
        $this->db->run("INSERT INTO workers (website_id, name, slug, command, directory, env, restart, memory_mb, procs, runtime, enabled, created_at, updated_at) VALUES (?, 'queue', 'queue', 'php artisan queue:work', '', ?, 'always', 256, 1, '', 1, ?, ?)", [$this->siteId, '{"QUEUE":"high"}', gmdate('c'), gmdate('c')]);
        $this->request('POST', '/sites/' . $this->siteId . '/env', ['env' => 'DB_HOST=localhost'] + $this->token(), $this->json());
        $env = (array) $this->lastCall('workers.apply')['env'];
        self::assertSame(['QUEUE' => 'high', 'DB_HOST' => 'localhost'], $env);
    }
}
