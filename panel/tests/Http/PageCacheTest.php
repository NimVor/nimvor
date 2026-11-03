<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** The page cache of a PHP website (Headers & cache → Cache rules): who may change it, what the agent is sent. */
final class PageCacheTest extends HttpTestCase
{
    private Connection $db;
    private int $phpSite;
    private int $staticSite;
    private int $otherSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        foreach ([['shop.test', 'php', $cust], ['plain.test', 'static', $cust], ['blog.test', 'php', $other]] as [$d, $kind, $o]) {
            $this->db->run(
                "INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, https, force_https, ssl_mode, cert_file, key_file, created_at, updated_at)
                 VALUES (1, ?, ?, ?, ?, ?, ?, 'active', 0, 0, 'none', '', '', 'x', 'x')",
                [$d, $kind, '/home/u/' . $d, '/home/u/' . $d . '/public_html', 'u' . $o, $o]
            );
        }
        $this->phpSite = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->staticSite = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'plain.test'");
        $this->otherSite = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('sites.page_cache_purge', ['purged' => true]);
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

    /** @param array<string, string> $over */
    private function form(array $over = []): array
    {
        return $over + ['static_ttl' => '0', 'static_ttl_custom' => '', 'dynamic_mode' => 'app', 'dynamic_ttl' => '60', 'bypass_paths' => ''];
    }

    public function testTurningItOnSendsTheLifetimeToTheAgent(): void
    {
        $this->login('cust');
        $this->post($this->url($this->phpSite, '/cache'), $this->form(['page_cache' => '1', 'page_cache_ttl' => '1800']));
        self::assertStringContainsString('saved', (string) $this->flash('success'));
        self::assertSame(['enabled' => true, 'ttl' => 1800], $this->lastCall('sites.apply')['cache']['page_cache']);
        $page = $this->request('GET', $this->url($this->phpSite))->body();
        self::assertStringContainsString('Empty the page cache', $page);
        self::assertStringContainsString('X-Page-Cache', $page);
    }

    public function testOffSendsNothing(): void
    {
        $this->login('cust');
        $this->post($this->url($this->phpSite, '/cache'), $this->form(['page_cache' => '1', 'page_cache_ttl' => '600']));
        $this->post($this->url($this->phpSite, '/cache'), $this->form(['static_ttl' => '3600']));
        self::assertArrayNotHasKey('page_cache', $this->lastCall('sites.apply')['cache']);
        self::assertStringNotContainsString('Empty the page cache', $this->request('GET', $this->url($this->phpSite))->body());
    }

    public function testOnlyTheListedLifetimesAreAccepted(): void
    {
        $this->login('cust');
        foreach (['0', '1', '90', '999999', 'abc', ''] as $bad) {
            $before = $this->calls('sites.apply');
            $this->post($this->url($this->phpSite, '/cache'), $this->form(['page_cache' => '1', 'page_cache_ttl' => $bad]));
            self::assertSame($before, $this->calls('sites.apply'), 'ttl ' . $bad);
        }
    }

    public function testAStaticWebsiteHasNoPageCache(): void
    {
        $this->login('cust');
        $before = $this->calls('sites.apply');
        $this->post($this->url($this->staticSite, '/cache'), $this->form(['page_cache' => '1', 'page_cache_ttl' => '600']));
        self::assertSame($before, $this->calls('sites.apply'));
        self::assertStringNotContainsString('Page cache (Nginx', $this->request('GET', $this->url($this->staticSite))->body());
    }

    public function testAStaleChoiceIsNotSentForANonPhpWebsite(): void
    {
        $this->db->run("UPDATE websites SET web_policy = ? WHERE id = ?", [json_encode(['cache' => ['static_ttl' => 60, 'dynamic_mode' => 'app', 'dynamic_ttl' => 60, 'bypass_paths' => [], 'page_cache' => ['enabled' => true, 'ttl' => 600]]]), $this->staticSite]);
        $site = $this->db->fetch('SELECT * FROM websites WHERE id = ?', [$this->staticSite]);
        $site['web_policy'] = json_decode((string) $site['web_policy'], true);
        $parts = \NimVor\Services\WebPolicyService::specParts($site);
        self::assertArrayNotHasKey('page_cache', $parts['cache']);
    }

    public function testPurgeCallsTheAgentForTheOwnersWebsiteOnly(): void
    {
        $this->login('cust');
        $this->post($this->url($this->phpSite, '/cache'), $this->form(['page_cache' => '1', 'page_cache_ttl' => '600']));
        $this->post($this->url($this->phpSite, '/cache/purge'));
        self::assertSame('shop.test', $this->lastCall('sites.page_cache_purge')['domain']);
        self::assertStringContainsString('emptied', (string) $this->flash('success'));
        self::assertStringContainsString('website.page_cache_purge', (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs')));
        // Another customer's website: nothing is sent.
        $before = $this->calls('sites.page_cache_purge');
        $this->post($this->url($this->otherSite, '/cache/purge'));
        self::assertSame($before, $this->calls('sites.page_cache_purge'));
    }

    public function testPurgeNeedsThePageCacheToBeOn(): void
    {
        $this->login('cust');
        $before = $this->calls('sites.page_cache_purge');
        $this->post($this->url($this->phpSite, '/cache/purge'));
        self::assertSame($before, $this->calls('sites.page_cache_purge'));
        self::assertNotSame('', (string) ($this->flash('error') ?? ''));
    }

    public function testAnotherCustomerCannotChangeIt(): void
    {
        $this->switchTo('other');
        $before = $this->calls('sites.apply');
        $this->post($this->url($this->phpSite, '/cache'), $this->form(['page_cache' => '1', 'page_cache_ttl' => '600']));
        self::assertSame($before, $this->calls('sites.apply'));
    }
}
