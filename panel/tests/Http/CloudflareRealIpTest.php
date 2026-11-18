<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Services\RealIpService;
use NimVor\Tests\Support\HttpTestCase;

/** Server → Cloudflare: restoring the visitor's own address behind Cloudflare. */
final class CloudflareRealIpTest extends HttpTestCase
{
    private function agentStatus(bool $enabled, bool $custom = false): void
    {
        $this->agent->succeed('realip.status', ['enabled' => $enabled, 'custom' => $custom, 'ranges' => ['173.245.48.0/20', '2400:cb00::/32'], 'header' => 'CF-Connecting-IP', 'file_present' => $enabled]);
    }

    public function testThePageShowsTheSettingEvenWithoutAnAccount(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->agentStatus(false);
        $body = $this->request('GET', '/cloudflare')->body();
        self::assertStringContainsString('Visitor addresses behind Cloudflare', $body);
        self::assertStringContainsString('Restore visitor addresses', $body);
        self::assertStringContainsString('2 Cloudflare networks trusted (built-in list)', $body);
        self::assertStringContainsString('Connect Cloudflare', $body, 'the account form is still there');
        self::assertStringNotContainsString('did not write', $body);
    }

    public function testExistingRealIpSettingsAreShownWithTheirFileAndLine(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->agent->succeed('realip.status', ['enabled' => false, 'custom' => false, 'ranges' => ['173.245.48.0/20'], 'header' => 'CF-Connecting-IP', 'file_present' => false, 'existing' => ['/etc/nginx/conf.d/cloudflare.conf:3: real_ip_header CF-Connecting-IP;', '/etc/nginx/nginx.conf:9: set_real_ip_from <b>x</b>;']]);
        $body = $this->request('GET', '/cloudflare')->body();
        self::assertStringContainsString('did not write', $body);
        self::assertStringContainsString('/etc/nginx/conf.d/cloudflare.conf:3: real_ip_header CF-Connecting-IP;', $body);
        self::assertStringContainsString('set_real_ip_from &lt;b&gt;x&lt;/b&gt;;', $body, 'escaped');
    }

    public function testTurningItOnAndOffCallsTheAgentAndIsAudited(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->agent->succeed('realip.set', ['enabled' => true]);
        $res = $this->post('/cloudflare/real-ip', ['enabled' => '1']);
        self::assertSame('/cloudflare', $res->header('Location'));
        self::assertSame(['enabled' => true], $this->lastCall('realip.set'));
        self::assertStringContainsString('restored', (string) $this->flash('success'));

        $this->post('/cloudflare/real-ip', ['enabled' => '0']);
        self::assertSame(['enabled' => false], $this->lastCall('realip.set'));
        self::assertSame(2, (int) $this->app->container->get(\NimVor\Database\Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'realip.set'"));
    }

    public function testRefreshApplyTheNetworksCloudflarePublishes(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->agentStatus(true);
        $this->agent->succeed('realip.set', ['enabled' => true]);
        $svc = $this->app->container->get(RealIpService::class);
        $asked = [];
        $svc->setFetcher(function (string $url) use (&$asked): array {
            $asked[] = $url;
            return ['status' => 200, 'body' => ['success' => true, 'result' => ['ipv4_cidrs' => ['203.0.113.0/24', 'bad value'], 'ipv6_cidrs' => ['2001:db8::/32']]]];
        });
        $res = $this->post('/cloudflare/real-ip/refresh');
        self::assertSame('/cloudflare', $res->header('Location'));
        self::assertSame([RealIpService::IPS_URL], $asked);
        $call = $this->lastCall('realip.set');
        self::assertTrue($call['enabled'], 'the switch keeps its position');
        self::assertSame(['203.0.113.0/24', '2001:db8::/32'], $call['ranges'], 'only well-formed networks are sent');
        self::assertStringContainsString('2 networks', (string) $this->flash('success'));
    }

    public function testABadAnswerFromCloudflareChangesNothing(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->agentStatus(false);
        $svc = $this->app->container->get(RealIpService::class);
        $svc->setFetcher(static fn (string $url): array => ['status' => 200, 'body' => ['result' => []]]);
        $this->post('/cloudflare/real-ip/refresh');
        self::assertSame(0, $this->calls('realip.set'));
        self::assertStringContainsString('Nothing was changed', (string) $this->flash('error'));

        $svc->setFetcher(static function (string $url): array {
            throw new \RuntimeException('offline');
        });
        $this->post('/cloudflare/real-ip/refresh');
        self::assertSame(0, $this->calls('realip.set'));
        self::assertStringContainsString('could not be reached', (string) $this->flash('error'));
    }

    public function testAnAgentFailureIsShownAndOnlyAdministratorsMayChangeIt(): void
    {
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->login('admin');
        $this->agent->fail('realip.set', 409, 'validation_failed', 'Nginx refused the file.');
        $res = $this->post('/cloudflare/real-ip', ['enabled' => '1']);
        self::assertSame('/cloudflare', $res->header('Location'));
        self::assertNotNull($this->flash('error'));

        $this->login('cust');
        self::assertSame(403, $this->post('/cloudflare/real-ip', ['enabled' => '1'])->status());
        self::assertSame(403, $this->post('/cloudflare/real-ip/refresh')->status());
    }
}
