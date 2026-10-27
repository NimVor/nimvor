<?php

declare(strict_types=1);

namespace NimVor\Tests\Support;

use NimVor\Agent\AgentClient;
use NimVor\Database\Migrator;
use NimVor\Http\Session\ArraySession;
use NimVor\Repositories\DiscoverySnapshotRepository;
use NimVor\Repositories\SettingRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Crypto;
use NimVor\Services\DiscoveryService;
use NimVor\Services\PlanLimits;
use NimVor\Services\ServerContext;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Support\Events;
use NimVor\Support\Logger;

/**
 * Database-backed harness with a fake agent for service tests.
 */
abstract class PanelHarness extends DatabaseTestCase
{
    protected FakeTransport $agent;
    protected AgentClient $client;
    protected WebsiteRepository $websites;
    protected WebsiteService $websiteService;
    protected ServerContext $server;
    protected Crypto $crypto;
    protected Events $events;
    protected DiscoveryService $discovery;
    /** @var array<string, mixed> */
    protected array $admin;

    protected function setUp(): void
    {
        parent::setUp();
        (new Migrator($this->db, dirname(__DIR__, 2) . '/database/migrations'))->migrate();
        $this->roles->syncDefaults();
        $this->agent = new FakeTransport();
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.3', 'fpm_binary' => '/usr/sbin/php-fpm8.3']]]);
        $this->agent->succeed('agent.status', ['sites_root' => '/home/nimvor', 'write_enabled' => true]);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $token = tempnam(sys_get_temp_dir(), 't');
        file_put_contents($token, 'tok');
        $this->client = new AgentClient($this->agent, $token, new Logger('/dev/null'));
        $this->websites = new WebsiteRepository($this->db);
        $this->server = new ServerContext(new ArraySession());
        $this->events = new Events();
        $this->crypto = new Crypto(sys_get_temp_dir() . '/nimvor-test-' . bin2hex(random_bytes(4)) . '.key');
        $settings = new SettingsService(new SettingRepository($this->db), $this->audit);
        $this->discovery = new DiscoveryService($this->client, new DiscoverySnapshotRepository($this->db), $settings, $this->audit, new SettingRepository($this->db));
        $this->websiteService = new WebsiteService($this->websites, $this->client, $this->audit, $this->server, $this->events, new PlanLimits($this->db), $this->discovery);
        $this->admin = $this->users->find($this->createUser('admin', 'admin'));
    }

    /** @return array<string, mixed> */
    protected function lastCall(string $action): array
    {
        foreach (array_reverse($this->agent->requests) as $r) {
            if ($r['path'] === '/v1/actions/' . $action) {
                return (array) json_decode((string) $r['body'], true);
            }
        }
        self::fail($action . ' was not called');
    }

    /** @param array<string, mixed> $owner */
    protected function makeSite(string $domain, array $owner): array
    {
        $id = $this->websiteService->create(['domain' => $domain, 'kind' => 'static'], $owner);
        return $this->websites->find(1, $id, null);
    }
}
