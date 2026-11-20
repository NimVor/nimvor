<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Services\EmailHealth;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** Site health: email DNS rules, the overview page and the dashboard widget. */
final class SitesHealthTest extends HttpTestCase
{
    private static function domain(): array
    {
        return ['domain' => 'shop.test', 'dkim_selector' => 'nimvor', 'dkim_txt' => 'v=DKIM1; k=rsa; p=ABCDEF123'];
    }

    /** @param list<array<string, string>> $records @return list<string> */
    private static function codes(array $records): array
    {
        return array_map(static fn (array $i): string => $i['code'] . ':' . $i['level'], EmailHealth::check(self::domain(), $records, 'mail.example.com'));
    }

    public function testACompleteZonePasses(): void
    {
        $records = [
            ['name' => '@', 'type' => 'MX', 'value' => 'mail.example.com.'],
            ['name' => '@', 'type' => 'TXT', 'value' => 'v=spf1 a mx ~all'],
            ['name' => 'nimvor._domainkey', 'type' => 'TXT', 'value' => 'v=DKIM1; k=rsa; p=ABCDEF123'],
            ['name' => '_dmarc', 'type' => 'TXT', 'value' => 'v=DMARC1; p=quarantine'],
        ];
        self::assertSame([], self::codes($records));
    }

    public function testMissingRecordsAreReportedWithTheirSeverity(): void
    {
        self::assertSame(['mx:bad', 'spf:warn', 'dkim:warn', 'dmarc:warn'], self::codes([]));
    }

    public function testWrongRecordsAreCaught(): void
    {
        $records = [
            ['name' => '@', 'type' => 'MX', 'value' => 'mx.elsewhere.net.'],
            ['name' => '@', 'type' => 'TXT', 'value' => 'v=spf1 +all'],
            ['name' => 'nimvor._domainkey', 'type' => 'TXT', 'value' => 'v=DKIM1; k=rsa; p=OTHERKEY'],
            ['name' => '_dmarc', 'type' => 'TXT', 'value' => 'v=DMARC1; p=none'],
        ];
        self::assertSame(['mx:warn', 'spf:bad', 'dkim:bad', 'dmarc:info'], self::codes($records));
    }

    public function testSeveralSpfRecordsAreAnError(): void
    {
        $records = [
            ['name' => '@', 'type' => 'MX', 'value' => 'mail.example.com.'],
            ['name' => '@', 'type' => 'TXT', 'value' => 'v=spf1 a ~all'],
            ['name' => '@', 'type' => 'TXT', 'value' => 'v=spf1 mx ~all'],
            ['name' => 'nimvor._domainkey', 'type' => 'TXT', 'value' => 'v=DKIM1; p=ABCDEF123'],
            ['name' => '_dmarc', 'type' => 'TXT', 'value' => 'v=DMARC1; p=reject'],
        ];
        self::assertSame(['spf:bad'], self::codes($records));
    }

    public function testThePageListsWebsitesWithTheirProblemsAndTheDashboardShowsTheWidget(): void
    {
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('mail.status', ['configured' => false]);
        $sites = $this->app->container->get(WebsiteService::class);
        $sites->create(['domain' => 'plain.test', 'kind' => 'static'], ['role_scope' => 'all', 'id' => 1]);
        $this->login('admin');
        $page = $this->request('GET', '/sites-health');
        self::assertSame(200, $page->status());
        self::assertStringContainsString('plain.test', $page->body());
        self::assertStringContainsString('No backup yet', $page->body());
        self::assertStringContainsString('No HTTPS', $page->body());
        $filtered = $this->request('GET', '/sites-health', [], [], '', ['category' => 'malware']);
        self::assertStringNotContainsString('No backup yet', $filtered->body());
    }

    public function testEmailProblemsInAZoneOnThisPanelAreShownAndCanBeFixed(): void
    {
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('mail.status', ['configured' => true, 'hostname' => 'mail.example.com']);
        $sites = $this->app->container->get(WebsiteService::class);
        $id = $sites->create(['domain' => 'mailsite.test', 'kind' => 'static'], ['role_scope' => 'all', 'id' => 1]);
        $db = $this->app->container->get(\NimVor\Database\Connection::class);
        $now = gmdate('c');
        $db->run("INSERT INTO mail_domains (server_id, domain, website_id, dkim_selector, dkim_txt, created_at) VALUES (1, 'mailsite.test', ?, 'nimvor', 'v=DKIM1; p=KEY', ?)", [$id, $now]);
        $domainId = (int) $db->scalar("SELECT id FROM mail_domains WHERE domain = 'mailsite.test'");
        $zone = $db->fetch("SELECT id FROM dns_zones WHERE name = 'mailsite.test'");
        if ($zone === null) {
            $db->run("INSERT INTO dns_zones (server_id, name, website_id, ttl, primary_ns, hostmaster, serial, created_at, updated_at) VALUES (1, 'mailsite.test', ?, 3600, 'ns1.test', 'h.test', 1, ?, ?)", [$id, $now, $now]);
        }
        $zoneId = (int) $db->scalar("SELECT id FROM dns_zones WHERE name = 'mailsite.test'");
        $db->run("DELETE FROM dns_records WHERE zone_id = ? AND type IN ('MX', 'TXT')", [$zoneId]);
        $this->login('admin');
        $page = $this->request('GET', '/sites-health');
        self::assertStringContainsString('No MX record', $page->body());
        self::assertStringContainsString('No DKIM record', $page->body());
        self::assertStringContainsString('No DMARC record', $page->body());
        self::assertStringContainsString('name="domain_id" value="' . $domainId . '"', $page->body());

        $this->agent->succeed('dns.zone_apply', ['serial' => 2]);
        $this->post('/sites-health/fix-email', ['domain_id' => (string) $domainId]);
        self::assertStringContainsString('email record(s) added', (string) $this->flash('success'));
        self::assertGreaterThan(0, (int) $db->scalar("SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND type = 'MX'", [$zoneId]));
        self::assertStringNotContainsString('No MX record', $this->request('GET', '/sites-health')->body());
    }
}
