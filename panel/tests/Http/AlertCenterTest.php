<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\MonitoringService;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** The alert center: new rules, severities, acknowledge, resolve, de-duplication and thresholds. */
final class AlertCenterTest extends HttpTestCase
{
    private Connection $conn;
    private MonitoringService $mon;
    private int $site;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->seedUser('plain', 'customer');
        $this->conn = $this->app->container->get(Connection::class);
        $this->site = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $this->app->container->get(UserRepository::class)->find($admin));
        $this->agent->succeed('system.info', ['system' => ['cpu' => ['cores' => 2]]]);
        $this->agent->succeed('system.metrics', ['cpu_percent' => 1, 'memory' => ['used_percent' => 10], 'load' => ['load1' => 0.1], 'disks' => [['mount_point' => '/', 'used_percent' => 10]], 'network' => []]);
        $this->agent->succeed('services.status', []);
        $this->agent->succeed('sites.usage', []);
        $this->agent->succeed('discovery.certificates', []);
        $this->mon = $this->app->container->get(MonitoringService::class);
        $this->mon->sender = static fn (): bool => true;
    }

    private function check(int $status, int $ms, int $ago): void
    {
        $this->conn->run('INSERT INTO website_checks (server_id, website_id, domain, checked_at, status, ms, error) VALUES (1, ?, ?, ?, ?, ?, ?)', [$this->site, 'shop.test', time() - $ago, $status, $status > 0 ? $ms : null, $status > 0 ? '' : 'timeout']);
    }

    /** @return list<array<string, mixed>> */
    private function open(): array
    {
        return $this->mon->openAlerts();
    }

    public function testASiteIsReportedDownOnlyAfterTheConfiguredFailuresInARow(): void
    {
        $this->check(0, 0, 600);
        $this->mon->run();
        self::assertSame([], $this->open(), 'one failed check is not an alert (two are needed)');
        $this->check(0, 0, 300);
        $this->mon->run(time());
        $a = $this->open();
        self::assertCount(1, $a);
        self::assertSame(['website', 'shop.test', 'critical'], [$a[0]['rule'], $a[0]['subject'], $a[0]['severity']]);
        // It answers again: resolved by itself.
        $this->check(200, 50, 10);
        $this->mon->run(time());
        self::assertSame([], $this->open());
        self::assertCount(1, $this->mon->recentAlerts());
    }

    public function testAWebsiteLeftOutOfTheChecksRaisesNoAlertAndItsOpenAlertCloses(): void
    {
        $this->check(0, 0, 600);
        $this->check(0, 0, 300);
        $this->mon->run(time());
        self::assertCount(1, $this->open());

        $this->login('admin');
        $token = ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
        $this->request('POST', '/sites/' . $this->site . '/monitoring', $token + ['enabled' => '0']);
        self::assertSame(1, (int) $this->conn->scalar('SELECT monitor_disabled FROM websites WHERE id = ?', [$this->site]));
        self::assertSame(1, (int) $this->conn->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'website.monitoring_off'"));
        $this->mon->run(time());
        self::assertSame([], $this->open(), 'its alert closes at the next pass');
        self::assertSame(2, (int) $this->conn->scalar('SELECT COUNT(*) FROM website_checks WHERE website_id = ?', [$this->site]), 'earlier results are kept');

        $this->request('POST', '/sites/' . $this->site . '/monitoring', $token + ['enabled' => '1']);
        self::assertSame(0, (int) $this->conn->scalar('SELECT monitor_disabled FROM websites WHERE id = ?', [$this->site]));
        $this->mon->run(time());
        self::assertCount(1, $this->open(), 'checked again: still failing, so the alert comes back');
    }

    public function testAlertsAreNotRaisedTwiceForTheSameProblem(): void
    {
        $this->check(502, 10, 600);
        $this->check(502, 10, 300);
        $this->mon->run();
        $this->mon->run(time() + 300);
        $this->mon->run(time() + 600);
        self::assertCount(1, $this->open());
        self::assertSame(1, (int) $this->conn->scalar("SELECT COUNT(*) FROM alerts WHERE rule = 'website'"));
    }

    public function testThresholdsComeFromSettingsAndZeroTurnsARuleOff(): void
    {
        $this->check(200, 9000, 900);
        $this->check(200, 9000, 600);
        $this->check(200, 9000, 300);
        $this->mon->run();
        self::assertSame([], $this->open(), 'the slow rule is off by default');
        $this->app->container->get(SettingsService::class)->update(['alert_site_slow_ms' => '5000'], 1);
        $this->mon->run(time() + 60);
        $a = $this->open();
        self::assertCount(1, $a);
        self::assertSame('slow', $a[0]['rule']);
        self::assertSame('warning', $a[0]['severity']);
    }

    public function testSecurityUpdatesAndAnUnreadableSourceKeepsTheAlertOpen(): void
    {
        $this->agent->succeed('packages.updates', ['count' => 5, 'security' => 2]);
        $this->mon->run();
        $a = $this->open();
        self::assertCount(1, $a);
        self::assertSame('updates', $a[0]['rule']);
        // The agent cannot answer for updates now: not "resolved".
        $this->agent->fail('packages.updates', 500, 'failed', 'broken');
        $this->mon->run(time() + 300);
        self::assertCount(1, $this->open());
        // It answers with none waiting: resolved.
        $this->agent->succeed('packages.updates', ['count' => 0, 'security' => 0]);
        $this->mon->run(time() + 600);
        self::assertSame([], $this->open());
    }

    public function testAcknowledgeKeepsItOpenAndResolveByHandReopensAcknowledgedNotAsNew(): void
    {
        $this->check(0, 0, 600);
        $this->check(0, 0, 300);
        $this->mon->run();
        $id = (int) $this->open()[0]['id'];
        self::assertTrue($this->mon->acknowledge($id, 1));
        self::assertFalse($this->mon->acknowledge($id, 1), 'already acknowledged');
        self::assertNotNull($this->open()[0]['acknowledged_at']);

        self::assertTrue($this->mon->resolve($id, 1));
        self::assertSame([], $this->open());
        $notes = (int) $this->conn->scalar("SELECT COUNT(*) FROM notifications WHERE dedupe_key LIKE 'alert:%:open'");
        // Still failing on the next pass: the same row comes back, acknowledged, with no new alert or notification.
        $this->mon->run(time() + 300);
        $a = $this->open();
        self::assertCount(1, $a);
        self::assertSame($id, (int) $a[0]['id']);
        self::assertNotNull($a[0]['acknowledged_at']);
        self::assertSame($notes, (int) $this->conn->scalar("SELECT COUNT(*) FROM notifications WHERE dedupe_key LIKE 'alert:%:open'"));
    }

    public function testTheWidgetCountsBySeverityAndLetsAnAdministratorActOnAnAlert(): void
    {
        $this->check(0, 0, 600);
        $this->check(0, 0, 300);
        $this->agent->succeed('packages.updates', ['count' => 5, 'security' => 3]);
        $this->mon->run();
        $this->login('admin');
        $widget = static fn (self $t): array => (array) json_decode($t->request('GET', '/dashboard/widgets/alert_center', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest'])->body(), true);
        $w = $widget($this);
        self::assertSame('ok', $w['state']);
        self::assertStringContainsString('shop.test does not answer', $w['html']);
        self::assertStringContainsString('3 security updates are waiting', $w['html']);
        self::assertLessThan(strpos($w['html'], '3 security updates'), strpos($w['html'], 'shop.test does not answer'), 'critical first');

        $id = (int) $this->open()[0]['id'];
        $token = ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
        $res = $this->request('POST', '/dashboard/alerts/' . $id . '/ack', $token);
        self::assertContains($res->status(), [302, 303]);
        $w = $widget($this);
        self::assertStringContainsString('acknowledged', $w['html'], 'the widget shows it as acknowledged straight away');
        self::assertSame(1, (int) $this->conn->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'alert.acknowledge'"));

        $this->request('POST', '/dashboard/alerts/' . $id . '/resolve', $token);
        self::assertCount(1, $this->open(), 'only the updates alert is left');
        self::assertSame(1, (int) $this->conn->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'alert.resolve'"));
    }

    public function testSomeoneWhoMayNotManageTheServerCannotActOnAlerts(): void
    {
        $this->check(0, 0, 600);
        $this->check(0, 0, 300);
        $this->mon->run();
        $id = (int) $this->open()[0]['id'];
        $this->login('plain');
        $token = ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
        $res = $this->request('POST', '/dashboard/alerts/' . $id . '/resolve', $token);
        self::assertNotSame(200, $res->status());
        self::assertCount(1, $this->open(), 'it is still open');
        self::assertNull($this->open()[0]['acknowledged_at']);
    }
}
