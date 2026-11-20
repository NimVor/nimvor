<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** A new website starts with statistics on and its account in Anti-intrusion, where those are installed / switched on. */
final class NewWebsiteDefaultsTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
    }

    private function create(string $domain): void
    {
        $this->app->container->get(WebsiteService::class)->create(['domain' => $domain, 'kind' => 'static', 'system_user' => 'nvsite'], ['role_scope' => 'all', 'id' => 1]);
    }

    public function testStatisticsAreTurnedOnWhenAwstatsIsInstalled(): void
    {
        $this->agent->succeed('stats.status', ['installed' => true, 'enabled' => false, 'months' => []]);
        $this->agent->succeed('stats.enable', ['ok' => true]);
        $this->create('stats.test');
        $call = $this->lastCall('stats.enable');
        self::assertSame('stats.test', $call['domain']);
        self::assertTrue($call['enabled']);
    }

    public function testNothingIsDoneWithoutAwstats(): void
    {
        $this->agent->succeed('stats.status', ['installed' => false, 'enabled' => false, 'months' => []]);
        $this->create('nostats.test');
        self::assertSame(0, $this->calls('stats.enable'));
    }

    public function testTheAccountJoinsAntiIntrusionWhenItIsOn(): void
    {
        $this->agent->succeed('intrusion.status', ['enabled' => true, 'accounts' => [
            ['name' => 'old1', 'kind' => 'website', 'log' => true, 'protect' => false],
            ['name' => 'nvsite', 'kind' => 'website', 'log' => false, 'protect' => false],
        ]]);
        $this->agent->succeed('intrusion.configure', ['ok' => true]);
        $this->create('guard.test');
        $call = $this->lastCall('intrusion.configure');
        self::assertTrue($call['enabled']);
        self::assertSame(['old1' => ['log' => true, 'protect' => false], 'nvsite' => ['log' => true, 'protect' => true]], $call['accounts']);
    }

    public function testAnAccountAnAdministratorSetUpIsLeftAlone(): void
    {
        $this->agent->succeed('intrusion.status', ['enabled' => true, 'accounts' => [['name' => 'nvsite', 'kind' => 'website', 'log' => true, 'protect' => false]]]);
        $this->agent->succeed('intrusion.configure', ['ok' => true]);
        $this->create('kept.test');
        self::assertSame(0, $this->calls('intrusion.configure'));
    }
}
