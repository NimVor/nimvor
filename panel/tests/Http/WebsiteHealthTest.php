<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteMonitor;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** Website health on the dashboard: real HTTP checks, outcomes, uptime and honest empty states. */
final class WebsiteHealthTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        foreach (['fast.test', 'slow.test', 'broken.test', 'gone.test', 'locked.test'] as $d) {
            $sites->create(['domain' => $d, 'kind' => 'static'], (array) $users->find($admin));
        }
    }

    private function monitor(): WebsiteMonitor
    {
        return $this->app->container->get(WebsiteMonitor::class);
    }

    private function probe(array $results, bool $supported = true): void
    {
        $this->agent->succeed('sites.probe', ['supported' => $supported, 'results' => $results]);
    }

    private function widget(): array
    {
        $res = $this->request('GET', '/dashboard/widgets/website_health', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertSame(200, $res->status(), $res->body());
        return (array) json_decode($res->body(), true);
    }

    public function testBeforeTheFirstCheckThereAreNoNumbersAtAll(): void
    {
        $this->login('admin');
        $w = $this->widget();
        self::assertSame('ok', $w['state']);
        self::assertStringContainsString('No check has run yet', $w['html']);
        self::assertStringNotContainsString('Uptime 24 h', $w['html']);
        self::assertNull($this->monitor()->summary());
    }

    public function testOutcomesFollowTheStatusCode(): void
    {
        self::assertSame('down', WebsiteMonitor::outcome(0));
        self::assertSame('up', WebsiteMonitor::outcome(200));
        self::assertSame('up', WebsiteMonitor::outcome(301));
        self::assertSame('auth', WebsiteMonitor::outcome(401));
        self::assertSame('auth', WebsiteMonitor::outcome(403));
        self::assertSame('client_error', WebsiteMonitor::outcome(404));
        self::assertSame('server_error', WebsiteMonitor::outcome(502));
    }

    public function testACheckIsRecordedAndSummarised(): void
    {
        $this->probe([
            ['domain' => 'fast.test', 'status' => 200, 'ms' => 40, 'scheme' => 'http'],
            ['domain' => 'slow.test', 'status' => 301, 'ms' => 3100, 'scheme' => 'http'],
            ['domain' => 'broken.test', 'status' => 502, 'ms' => 120, 'scheme' => 'http'],
            ['domain' => 'gone.test', 'status' => 0, 'ms' => 0, 'error' => 'timeout', 'scheme' => 'http'],
            ['domain' => 'locked.test', 'status' => 403, 'ms' => 60, 'scheme' => 'http'],
        ]);
        self::assertSame(5, $this->monitor()->check());
        $sent = $this->lastCall('sites.probe');
        self::assertCount(5, $sent['sites']);

        $s = (array) $this->monitor()->summary();
        self::assertSame(['up' => 2, 'auth' => 1, 'client_error' => 0, 'server_error' => 1, 'down' => 1], $s['counts']);
        self::assertSame('slow.test', $s['slowest'][0]['domain']);
        self::assertSame(1, $s['slow']);
        self::assertSame(60.0, (float) $s['uptime_24h']);
        $domains = array_column($s['problems'], 'domain');
        sort($domains);
        self::assertSame(['broken.test', 'gone.test'], $domains);

        $this->login('admin');
        $html = $this->widget()['html'];
        self::assertStringContainsString('Failing now', $html);
        self::assertStringContainsString('no answer in time', $html);
        self::assertStringContainsString('HTTP 502', $html);
        self::assertStringContainsString('counted as online', $html);
    }

    public function testNothingIsRecordedWhenTheAgentCannotCheck(): void
    {
        $this->probe([['domain' => 'fast.test', 'status' => 0, 'error' => 'unsupported', 'scheme' => 'http']], false);
        self::assertNull($this->monitor()->check());
        self::assertNull($this->monitor()->summary(), 'an unsupported check is not "down"');
    }

    public function testCheckNowIsLimitedToOncePerMinuteAndNeedsALogin(): void
    {
        $this->probe([['domain' => 'fast.test', 'status' => 200, 'ms' => 40, 'scheme' => 'http']]);
        self::assertContains($this->post('/dashboard/websites/check')->status(), [302, 303]);
        self::assertSame(0, $this->calls('sites.probe'), 'not signed in');

        $this->login('admin');
        $token = ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
        $this->request('POST', '/dashboard/websites/check', $token);
        self::assertSame(1, $this->calls('sites.probe'));
        $this->request('POST', '/dashboard/websites/check', $token);
        self::assertSame(1, $this->calls('sites.probe'), 'a second press within a minute does not probe again');
    }

    public function testOldChecksArePruned(): void
    {
        $this->probe([['domain' => 'fast.test', 'status' => 200, 'ms' => 40, 'scheme' => 'http']]);
        $db = $this->app->container->get(\NimVor\Database\Connection::class);
        $db->run("INSERT INTO website_checks (server_id, website_id, domain, checked_at, status, ms, error) VALUES (1, 1, 'fast.test', ?, 200, 10, '')", [time() - 15 * 86400]);
        $this->monitor()->check();
        self::assertSame(1, (int) $db->scalar('SELECT COUNT(*) FROM website_checks'));
    }
}
