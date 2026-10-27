<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Services\SettingsService;
use WhaCP\Tests\Support\HttpTestCase;

/** The central settings page: categories over the same settings. */
final class CentralSettingsTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
    }

    public function testCategoriesAndLinks(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/settings')->body();
        foreach (['id="pane-general"', 'id="pane-dns"', 'id="pane-ssl"', 'id="pane-email"', 'id="pane-limits"', 'name="dns_nameservers"', 'href="/php"', 'href="/plans"', 'href="/mail/admin/greylisting"', 'SMTP server'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        foreach (SettingsService::definitions() as $key => $def) {
            self::assertArrayHasKey('category', $def, $key);
        }
    }

    public function testSavingOneCategoryReturnsToItsTab(): void
    {
        $this->login('admin');
        $res = $this->post('/settings', ['tab' => 'dns', 'dns_hostmaster' => 'hostmaster@example.test']);
        self::assertSame('/settings#dns', $res->header('location'));
        self::assertSame('hostmaster@example.test', $this->app->container->get(SettingsService::class)->string('dns_hostmaster'));
        self::assertSame('WhaCP', $this->app->container->get(SettingsService::class)->string('panel_name'), 'other settings unchanged');
        $res = $this->post('/settings', ['tab' => '"><script>', 'panel_name' => 'Panel']);
        self::assertSame('/settings', $res->header('location'));
    }
}
