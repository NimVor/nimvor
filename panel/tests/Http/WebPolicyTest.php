<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\WebPolicyService;
use NimVor\Tests\Support\HttpTestCase;

/** A website's Security headers & cache page (WebPolicyService, sites.apply spec). */
final class WebPolicyTest extends HttpTestCase
{
    private int $siteId;
    private int $httpsSite;
    private int $otherSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $db = $this->app->container->get(Connection::class);
        foreach ([['shop.test', $cust, 0], ['secure.test', $cust, 1], ['blog.test', $other, 0]] as [$d, $o, $tls]) {
            $db->run(
                "INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, https, force_https, ssl_mode, cert_file, key_file, created_at, updated_at)
                 VALUES (1, ?, 'static', ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, 'x', 'x')",
                [$d, '/home/u/' . $d, '/home/u/' . $d . '/public_html', 'u' . $o, $o, $tls, $tls, $tls ? 'letsencrypt' : 'none', $tls ? '/etc/letsencrypt/live/' . $d . '/fullchain.pem' : '', $tls ? '/etc/letsencrypt/live/' . $d . '/privkey.pem' : '']
            );
        }
        $this->siteId = (int) $db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->httpsSite = (int) $db->scalar("SELECT id FROM websites WHERE domain = 'secure.test'");
        $this->otherSite = (int) $db->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        $this->agent->succeed('sites.apply', ['changed' => []]);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    private function url(int $id, string $tail = ''): string
    {
        return '/sites/' . $id . '/web-policy' . $tail;
    }

    /** @return array<string, string> a valid headers form */
    private function headersForm(array $over = []): array
    {
        return $over + ['clickjacking' => 'sameorigin', 'frame_ancestors' => '', 'csp' => '', 'csp_report_only' => '0', 'referrer' => 'strict-origin-when-cross-origin', 'permissions' => 'restrictive', 'permissions_custom' => '', 'hsts_enabled' => '0'];
    }

    /** @return array<string, string> a valid cache form */
    private function cacheForm(array $over = []): array
    {
        return $over + ['static_ttl' => '0', 'static_ttl_custom' => '', 'dynamic_mode' => 'app', 'dynamic_ttl' => '60', 'bypass_paths' => ''];
    }

    public function testThePageIsReachableFromTheOverviewAndShowsRecommendedDefaults(): void
    {
        $this->login('admin');
        $overview = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('href="' . $this->url($this->siteId) . '"', $overview);
        $page = $this->request('GET', $this->url($this->siteId))->body();
        foreach (['Security headers', 'Cache rules', 'Framing protection', 'Content Security Policy', 'Report-only', 'Referrer policy', 'Permissions policy', 'HSTS', 'Never cached publicly', 'name="clickjacking"', 'name="static_ttl"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertMatchesRegularExpression('/<option value="sameorigin" selected>/', $page);
        self::assertStringNotContainsString('Restore defaults', $page, 'nothing to restore before saving');
        self::assertStringContainsString('X-Content-Type-Options: nosniff', $page, 'the fixed header is explained');
    }

    public function testSavingHeadersSendsThemWithTheSpecAndKeepsServerRulesOut(): void
    {
        $this->login('admin');
        $this->post($this->url($this->siteId, '/headers'), $this->headersForm([
            'clickjacking' => 'allow', 'frame_ancestors' => "https://partner.example.com\nhttps://*.cdn.example.org",
            'csp' => "default-src 'self';  img-src 'self' https: data:", 'csp_report_only' => '1',
            'referrer' => 'no-referrer', 'permissions' => 'custom', 'permissions_custom' => 'geolocation=self, camera=()',
        ]));
        self::assertStringContainsString('saved', (string) $this->flash('success'));
        $spec = $this->lastCall('sites.apply');
        self::assertSame([
            'clickjacking' => 'allow', 'frame_ancestors' => ['https://partner.example.com', 'https://*.cdn.example.org'],
            'csp' => "default-src 'self'; img-src 'self' https: data:", 'csp_report_only' => true,
            'referrer' => 'no-referrer', 'permissions' => 'custom', 'permissions_custom' => 'camera=(), geolocation=(self)',
            'hsts' => ['enabled' => false, 'max_age' => 0, 'include_subdomains' => false, 'preload' => false],
        ], $spec['headers']);
        self::assertArrayNotHasKey('cache', $spec, 'the other section stays untouched');
        // Nothing the customer cannot choose travels with it.
        self::assertStringNotContainsString('nosniff', (string) json_encode($spec));
        $page = $this->request('GET', $this->url($this->siteId))->body();
        self::assertStringContainsString('Restore defaults', $page);
        self::assertStringContainsString('camera=(), geolocation=(self)', $page);
    }

    public function testSavingCacheRules(): void
    {
        $this->login('admin');
        $this->post($this->url($this->siteId, '/cache'), $this->cacheForm(['static_ttl' => '604800', 'dynamic_mode' => 'public', 'dynamic_ttl' => '120', 'bypass_paths' => "/members/\n/private\n/members"]));
        self::assertStringContainsString('saved', (string) $this->flash('success'));
        self::assertSame(
            ['static_ttl' => 604800, 'dynamic_mode' => 'public', 'dynamic_ttl' => 120, 'bypass_paths' => ['/members', '/private']],
            $this->lastCall('sites.apply')['cache']
        );
        // A custom lifetime overrides the preset.
        $this->post($this->url($this->siteId, '/cache'), $this->cacheForm(['static_ttl' => '3600', 'static_ttl_custom' => '7200']));
        self::assertSame(7200, $this->lastCall('sites.apply')['cache']['static_ttl']);
        // Modes that need no lifetime do not keep one.
        $this->post($this->url($this->siteId, '/cache'), $this->cacheForm(['dynamic_mode' => 'no-store', 'dynamic_ttl' => '999999']));
        self::assertSame(60, $this->lastCall('sites.apply')['cache']['dynamic_ttl']);
    }

    public function testInvalidHeaderInputStaysInThePanel(): void
    {
        $this->login('admin');
        $bad = [
            ['clickjacking' => 'everyone'],
            ['clickjacking' => 'allow', 'frame_ancestors' => ''],
            ['clickjacking' => 'allow', 'frame_ancestors' => 'http://plain.example.com'],
            ['csp' => "default-src 'self'; frame-ancestors *"],
            ['csp' => "default-src 'self'\"; add_header X-Evil 1"],
            ['csp' => "script-src 'unsafe-inline' javascript:"],
            ['csp' => 'foo-src x'],
            ['csp' => "default-src 'self' \$host"],
            ['csp' => str_repeat('a', WebPolicyService::MAX_CSP + 1)],
            ['referrer' => 'unsafe-url'],
            ['permissions' => 'yolo'],
            ['permissions' => 'custom', 'permissions_custom' => ''],
            ['permissions' => 'custom', 'permissions_custom' => 'camera=(*.evil.com)'],
            ['permissions' => 'custom', 'permissions_custom' => 'nonsense=()'],
            ['hsts_enabled' => '1'], // plain-HTTP site
        ];
        foreach ($bad as $i => $over) {
            $this->post($this->url($this->siteId, '/headers'), $this->headersForm($over));
            self::assertNotEmpty((array) $this->flash('errors'), 'case ' . $i . ' must be rejected');
        }
        self::assertSame(0, $this->calls('sites.apply'));
        self::assertSame('', (string) $this->app->container->get(Connection::class)->scalar('SELECT web_policy FROM websites WHERE id = ?', [$this->siteId]));
    }

    public function testInvalidCacheInputStaysInThePanel(): void
    {
        $this->login('admin');
        $bad = [
            ['static_ttl' => '5'], ['static_ttl' => '99999999'], ['static_ttl' => 'soon'], ['static_ttl' => '0', 'static_ttl_custom' => '10'],
            ['dynamic_mode' => 'forever'], ['dynamic_mode' => 'public', 'dynamic_ttl' => '5'], ['dynamic_mode' => 'public', 'dynamic_ttl' => '86400'],
            ['bypass_paths' => 'members'], ['bypass_paths' => '/a/../b'], ['bypass_paths' => '/a b'], ['bypass_paths' => "/x;y"], ['bypass_paths' => '/x"}'],
            ['bypass_paths' => implode("\n", array_map(static fn (int $i): string => '/p' . $i, range(1, WebPolicyService::MAX_BYPASS + 1)))],
        ];
        foreach ($bad as $i => $over) {
            $this->post($this->url($this->siteId, '/cache'), $this->cacheForm($over));
            self::assertNotEmpty((array) $this->flash('errors'), 'case ' . $i . ' must be rejected');
        }
        self::assertSame(0, $this->calls('sites.apply'));
    }

    public function testHstsIsOnlyForVerifiedHttpsWebsites(): void
    {
        $this->login('admin');
        // Plain HTTP: the controls are disabled and a request is refused.
        $page = $this->request('GET', $this->url($this->siteId))->body();
        self::assertStringContainsString('HSTS needs working HTTPS', $page);
        self::assertMatchesRegularExpression('/id="wp-hsts"[^>]* disabled/', $page);
        $this->post($this->url($this->siteId, '/headers'), $this->headersForm(['hsts_enabled' => '1', 'hsts_max_age' => '31536000']));
        self::assertArrayHasKey('hsts_enabled', (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('sites.apply'));

        // HTTPS with a certificate and the redirect: allowed and bounded.
        $secure = $this->url($this->httpsSite);
        self::assertDoesNotMatchRegularExpression('/id="wp-hsts"[^>]* disabled/', $this->request('GET', $secure)->body());
        foreach ([['hsts_max_age' => '10'], ['hsts_max_age' => '999999999'], ['hsts_max_age' => '86400', 'hsts_preload' => '1', 'hsts_include_subdomains' => '1'], ['hsts_max_age' => '31536000', 'hsts_preload' => '1']] as $i => $over) {
            $this->post($secure . '/headers', $this->headersForm(['hsts_enabled' => '1'] + $over));
            self::assertNotEmpty((array) $this->flash('errors'), 'HSTS case ' . $i);
        }
        self::assertSame(0, $this->calls('sites.apply'));
        $this->post($secure . '/headers', $this->headersForm(['hsts_enabled' => '1', 'hsts_max_age' => '31536000', 'hsts_include_subdomains' => '1', 'hsts_preload' => '1']));
        self::assertSame(['enabled' => true, 'max_age' => 31536000, 'include_subdomains' => true, 'preload' => true], $this->lastCall('sites.apply')['headers']['hsts']);

        // If HTTPS is later turned off, a saved HSTS choice is not sent.
        $db = $this->app->container->get(Connection::class);
        $db->run("UPDATE websites SET force_https = 0 WHERE id = ?", [$this->httpsSite]);
        $this->post($secure . '/cache', $this->cacheForm(['static_ttl' => '3600']));
        self::assertFalse($this->lastCall('sites.apply')['headers']['hsts']['enabled'], 'HSTS follows the website\'s real HTTPS state');
    }

    public function testRestoreDefaultsRemovesTheChoices(): void
    {
        $this->login('admin');
        $db = $this->app->container->get(Connection::class);
        $this->post($this->url($this->siteId, '/headers'), $this->headersForm(['clickjacking' => 'deny']));
        $this->post($this->url($this->siteId, '/cache'), $this->cacheForm(['static_ttl' => '3600']));
        $this->post($this->url($this->siteId, '/reset'), ['section' => 'headers']);
        $spec = $this->lastCall('sites.apply');
        self::assertArrayNotHasKey('headers', $spec);
        self::assertArrayHasKey('cache', $spec, 'the cache rules stay');
        self::assertStringContainsString('back to the defaults', (string) $this->flash('success'));
        $this->post($this->url($this->siteId, '/reset'), ['section' => 'all']);
        $spec = $this->lastCall('sites.apply');
        self::assertArrayNotHasKey('headers', $spec);
        self::assertArrayNotHasKey('cache', $spec);
        self::assertSame([], json_decode((string) $db->scalar('SELECT web_policy FROM websites WHERE id = ?', [$this->siteId]), true));
        $before = $this->calls('sites.apply');
        $this->post($this->url($this->siteId, '/reset'), ['section' => 'nonsense']);
        self::assertSame($before, $this->calls('sites.apply'));
    }

    public function testWhenTheAgentRefusesThePreviousSettingsStay(): void
    {
        $this->login('admin');
        $db = $this->app->container->get(Connection::class);
        $this->post($this->url($this->siteId, '/headers'), $this->headersForm(['clickjacking' => 'deny']));
        $saved = (string) $db->scalar('SELECT web_policy FROM websites WHERE id = ?', [$this->siteId]);
        self::assertStringContainsString('deny', $saved);

        $this->agent->fail('sites.apply', 409, 'failed', 'The website configuration was not applied: nginx -t failed.');
        $this->post($this->url($this->siteId, '/headers'), $this->headersForm(['clickjacking' => 'off', 'referrer' => 'no-referrer']));
        self::assertStringContainsString('not applied', (string) $this->flash('error'));
        self::assertSame($saved, (string) $db->scalar('SELECT web_policy FROM websites WHERE id = ?', [$this->siteId]), 'the panel keeps what the server still runs');
        $this->post($this->url($this->siteId, '/cache'), $this->cacheForm(['static_ttl' => '3600']));
        self::assertSame($saved, (string) $db->scalar('SELECT web_policy FROM websites WHERE id = ?', [$this->siteId]));
        $this->post($this->url($this->siteId, '/reset'), ['section' => 'all']);
        self::assertSame($saved, (string) $db->scalar('SELECT web_policy FROM websites WHERE id = ?', [$this->siteId]));
    }

    public function testOwnersManageTheirOwnWebsiteOnly(): void
    {
        $this->switchTo('cust');
        self::assertSame(200, $this->request('GET', $this->url($this->siteId))->status());
        $this->post($this->url($this->siteId, '/headers'), $this->headersForm(['clickjacking' => 'deny']));
        self::assertSame(1, $this->calls('sites.apply'));

        self::assertSame(404, $this->request('GET', $this->url($this->otherSite))->status());
        $this->post($this->url($this->otherSite, '/headers'), $this->headersForm());
        $this->post($this->url($this->otherSite, '/cache'), $this->cacheForm(['static_ttl' => '3600']));
        $this->post($this->url($this->otherSite, '/reset'), ['section' => 'all']);
        self::assertSame(1, $this->calls('sites.apply'), "another customer's website is never touched");
        self::assertSame('', (string) $this->app->container->get(Connection::class)->scalar('SELECT web_policy FROM websites WHERE id = ?', [$this->otherSite]));
    }

    public function testAnonymousVisitorsAreSentToSignIn(): void
    {
        $r = $this->request('GET', $this->url($this->siteId));
        self::assertContains($r->status(), [302, 303, 401, 403]);
        self::assertSame(0, $this->calls('sites.apply'));
    }

    public function testExistingSslPageAndLegacyHeadersKeepWorking(): void
    {
        $this->login('admin');
        // The older switch still works and is sent while no policy is saved.
        $this->post('/sites/' . $this->httpsSite . '/security-headers', ['security_headers' => '1']);
        self::assertTrue($this->lastCall('sites.apply')['security_headers']);
        self::assertArrayNotHasKey('headers', $this->lastCall('sites.apply'));
        // Saving the new policy sends it next to the flag; the agent lets the
        // policy win, so no header is duplicated.
        $this->post($this->url($this->httpsSite, '/headers'), $this->headersForm());
        $spec = $this->lastCall('sites.apply');
        self::assertTrue($spec['security_headers']);
        self::assertSame('sameorigin', $spec['headers']['clickjacking']);
    }
}
