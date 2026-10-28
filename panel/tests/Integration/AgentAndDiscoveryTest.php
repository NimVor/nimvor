<?php

declare(strict_types=1);

namespace NimVor\Tests\Integration;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Agent\AgentUnavailableException;
use NimVor\Repositories\DiscoverySnapshotRepository;
use NimVor\Repositories\SettingRepository;
use NimVor\Services\DiscoveryService;
use NimVor\Services\SettingsService;
use NimVor\Support\Logger;
use NimVor\Tests\Support\DatabaseTestCase;
use NimVor\Tests\Support\FakeTransport;

final class AgentAndDiscoveryTest extends DatabaseTestCase
{
    private FakeTransport $transport;
    private string $tokenFile;

    protected function setUp(): void
    {
        parent::setUp();
        $this->transport = new FakeTransport();
        $this->tokenFile = tempnam(sys_get_temp_dir(), 'nimvor-token');
        file_put_contents($this->tokenFile, "secret-token-value\n");
    }

    protected function tearDown(): void
    {
        @unlink($this->tokenFile);
    }

    private function client(?string $tokenFile = null): AgentClient
    {
        return new AgentClient($this->transport, $tokenFile ?? $this->tokenFile, new Logger('/dev/null'));
    }

    public function testCallSendsTokenAndReturnsData(): void
    {
        $this->transport->succeed('system.info', ['hostname' => 'web1']);
        self::assertSame(['hostname' => 'web1'], $this->client()->call('system.info'));
        $req = $this->transport->requests[0];
        self::assertSame('POST', $req['method']);
        self::assertSame('/v1/actions/system.info', $req['path']);
        self::assertSame('secret-token-value', $req['token']);
        self::assertSame('{}', $req['body']);
    }

    public function testInvalidActionNamesAreRejectedLocally(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        $this->client()->call('../../v1/health');
    }

    public function testAgentErrorsBecomeFriendlyExceptions(): void
    {
        $this->transport->fail('discovery.inventory', 403, 'write_disabled', 'read-only');
        try {
            $this->client()->call('discovery.inventory');
            self::fail('expected exception');
        } catch (AgentException $e) {
            self::assertSame('write_disabled', $e->errorCode());
            self::assertStringContainsString('read-only', $e->remedy());
        }
    }

    public function testInvalidJsonIsHandled(): void
    {
        $this->transport->responses['/v1/actions/x'] = new \NimVor\Agent\AgentHttpResponse(200, '<html>');
        $this->expectException(AgentException::class);
        $this->client()->call('x');
    }

    public function testMissingTokenFile(): void
    {
        $this->expectException(AgentUnavailableException::class);
        $this->client('/nonexistent/token')->call('system.info');
    }

    public function testDiscoveryCachesSnapshots(): void
    {
        $inventory = ['websites' => ['websites' => [
            ['id' => 'a1', 'domain' => 'a.test', 'enabled' => true, 'https' => true, 'php' => ['version' => '8.3'], 'ssl' => ['days_remaining' => 5]],
            ['id' => 'b2', 'domain' => 'b.test', 'enabled' => false, 'https' => false, 'php' => ['version' => '8.3']],
            ['id' => 'c3', 'domain' => 'c.test', 'enabled' => true, 'https' => true, 'php' => null, 'ssl' => ['days_remaining' => 80]],
        ]]];
        $this->transport->succeed('discovery.inventory', $inventory);
        $settings = new SettingsService(new SettingRepository($this->db), $this->audit);
        $snapshots = new DiscoverySnapshotRepository($this->db);
        $svc = new DiscoveryService($this->client(), $snapshots, $settings, $this->audit, new SettingRepository($this->db));

        $first = $svc->inventory(false, null);
        self::assertFalse($first['from_cache']);
        $second = $svc->inventory(false, null);
        self::assertTrue($second['from_cache']);
        self::assertCount(1, $this->transport->requests, 'second call must use the snapshot');

        $svc->inventory(true, null);
        self::assertCount(2, $this->transport->requests, 'refresh must ask the agent');
        self::assertSame(3, $snapshots->latest()['website_count'] ?? null);

        self::assertSame('b.test', DiscoveryService::website($second['data'], 'b2')['domain'] ?? null);
        self::assertNull(DiscoveryService::website($second['data'], 'zz'));
        $sum = DiscoveryService::summary($second['data'], 14);
        self::assertSame(['total' => 3, 'enabled' => 2, 'https' => 2, 'php_versions' => ['8.3' => 2], 'expiring' => 1], $sum);
    }

    public function testDiscoveryPropagatesAgentOutage(): void
    {
        $this->transport->down = true;
        $settings = new SettingsService(new SettingRepository($this->db), $this->audit);
        $svc = new DiscoveryService($this->client(), new DiscoverySnapshotRepository($this->db), $settings, $this->audit, new SettingRepository($this->db));
        $this->expectException(AgentUnavailableException::class);
        $svc->inventory(false, null);
    }

    public function testSnapshotRetention(): void
    {
        $repo = new DiscoverySnapshotRepository($this->db);
        for ($i = 0; $i < 5; $i++) {
            $repo->store(['n' => $i], $i, null, 3);
        }
        self::assertSame(3, (int) $this->db->scalar('SELECT COUNT(*) FROM discovery_snapshots'));
        self::assertSame(['n' => 4], $repo->latest()['data'] ?? null);
    }
}
