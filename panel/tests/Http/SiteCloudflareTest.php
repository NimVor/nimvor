<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\CloudflareSiteService;
use WhaCP\Services\FeatureService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Site → Cloudflare: a customer manages their own zone with their own token. */
final class SiteCloudflareTest extends HttpTestCase
{
    private int $site;
    private int $cust;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->site = $this->app->container->get(WebsiteService::class)->create(
            ['domain' => 'shop.test', 'kind' => 'static'],
            (array) $this->app->container->get(UserRepository::class)->find($this->cust),
        );
    }

    private function fakeCf(): void
    {
        $cf = $this->app->container->get(CloudflareSiteService::class);
        $cf->setTransport(static function (string $method, string $url, ?array $body, string $token): array {
            $ok = static fn (mixed $result): array => ['status' => 200, 'body' => ['success' => true, 'result' => $result]];
            return match (true) {
                str_contains($url, '/user/tokens/verify') => $ok(['status' => 'active']),
                str_contains($url, '/zones?per_page=50') => $ok([
                    ['id' => str_repeat('a', 32), 'name' => 'shop.test', 'status' => 'active'],
                ]),
                preg_match('#/settings/ssl$#', $url) === 1 => $ok(['value' => 'full']),
                preg_match('#/settings/always_use_https$#', $url) === 1 => $ok(['value' => 'on']),
                preg_match('#/dns_records\?#', $url) === 1 => $ok([
                    ['id' => str_repeat('b', 32), 'type' => 'A', 'name' => 'shop.test', 'content' => '203.0.113.5', 'proxied' => false, 'proxiable' => true],
                ]),
                default => $ok([]),
            };
        });
    }

    public function testCustomerConnectsAndManagesTheirZone(): void
    {
        $this->fakeCf();
        $this->login('cust');
        $base = '/sites/' . $this->site . '/cloudflare';

        $this->post($base . '/connect', ['token' => 'cf-token-abcdef_ABCDEF12345']);
        self::assertSame('shop.test', $this->db->scalar('SELECT zone_name FROM cloudflare_sites WHERE website_id = ?', [$this->site]));
        // The token is stored encrypted.
        $stored = (string) $this->db->scalar('SELECT token_enc FROM cloudflare_sites WHERE website_id = ?', [$this->site]);
        self::assertStringNotContainsString('cf-token-abcdef', $stored);

        $page = $this->request('GET', $base)->body();
        self::assertStringContainsString('Connected to the Cloudflare zone', $page);
        self::assertStringContainsString('shop.test', $page);

        $this->post($base . '/ssl', ['mode' => 'strict']);
        self::assertSame('cloudflare.site_ssl', $this->db->scalar("SELECT action FROM audit_logs WHERE action = 'cloudflare.site_ssl'"));

        $this->post($base . '/disconnect', []);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM cloudflare_sites WHERE website_id = ?', [$this->site]));
    }

    public function testBadTokenIsRejected(): void
    {
        $this->login('cust');
        $this->post('/sites/' . $this->site . '/cloudflare/connect', ['token' => 'short']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM cloudflare_sites'));
        self::assertNotNull($this->flash('error'));
    }

    public function testCloudflareIsGatedByFeature(): void
    {
        $this->db->run("INSERT INTO plans (name, created_at, updated_at) VALUES ('P', 'x', 'x')");
        $plan = (int) $this->db->lastInsertId();
        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'NoCF', 'features' => array_values(array_diff(array_keys(FeatureService::FEATURES), ['cloudflare']))], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $plan]);
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [$plan, $this->cust]);
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/sites/' . $this->site . '/cloudflare')->status());
    }
}
