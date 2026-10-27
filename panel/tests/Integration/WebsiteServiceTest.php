<?php

declare(strict_types=1);

namespace WhaCP\Tests\Integration;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Repositories\DiscoverySnapshotRepository;
use WhaCP\Repositories\SettingRepository;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\DiscoveryService;
use WhaCP\Services\PlanLimits;
use WhaCP\Services\ServerContext;
use WhaCP\Services\SettingsService;
use WhaCP\Services\WebsiteService;
use WhaCP\Support\Events;
use WhaCP\Support\Logger;
use WhaCP\Support\ValidationException;
use WhaCP\Tests\Support\DatabaseTestCase;
use WhaCP\Tests\Support\FakeTransport;

final class WebsiteServiceTest extends DatabaseTestCase
{
    private FakeTransport $agent;
    private WebsiteRepository $repo;
    private WebsiteService $svc;
    /** @var array<string, mixed> */
    private array $admin;

    protected function setUp(): void
    {
        parent::setUp();
        (new \WhaCP\Database\Migrator($this->db, dirname(__DIR__, 2) . '/database/migrations'))->migrate();
        $this->agent = new FakeTransport();
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.3', 'fpm_binary' => '/usr/sbin/php-fpm8.3'], ['version' => '8.1', 'fpm_binary' => '']]]);
        $this->agent->succeed('agent.status', ['sites_root' => '/home/whacp', 'write_enabled' => true]);
        $this->agent->succeed('sites.apply', ['changed' => ['x']]);
        $this->agent->succeed('sites.delete', ['removed' => [], 'warnings' => []]);
        $token = tempnam(sys_get_temp_dir(), 't');
        file_put_contents($token, 'tok');
        $client = new AgentClient($this->agent, $token, new Logger('/dev/null'));
        $this->repo = new WebsiteRepository($this->db);
        $settings = new SettingsService(new SettingRepository($this->db), $this->audit);
        $discovery = new DiscoveryService($client, new DiscoverySnapshotRepository($this->db), $settings, $this->audit, new SettingRepository($this->db));
        $this->svc = new WebsiteService($this->repo, $client, $this->audit, new ServerContext(new ArraySession()), new Events(), new PlanLimits($this->db), $discovery);
        $this->admin = $this->users->find($this->createUser('admin', 'admin'));
    }

    /** @return array<string, mixed> */
    private function lastSpec(): array
    {
        foreach (array_reverse($this->agent->requests) as $r) {
            if ($r['path'] === '/v1/actions/sites.apply') {
                return json_decode((string) $r['body'], true);
            }
        }
        self::fail('sites.apply was not called');
    }

    public function testCreateSendsValidatedSpec(): void
    {
        $id = $this->svc->create(['domain' => 'Shop.Example.COM', 'aliases' => "www.shop.example.com\nshop.example.com", 'kind' => 'php', 'php_version' => '8.3', 'redirects' => '/old https://shop.example.com/new 302'], $this->admin);
        $site = $this->repo->find(1, $id, null);
        self::assertSame('shop.example.com', $site['domain']);
        self::assertSame(['www.shop.example.com'], $site['aliases']);
        self::assertSame('shop', $site['system_user']);
        self::assertSame('/home/whacp/shop.example.com/public_html', $site['document_root']);
        self::assertSame('active', $site['status']);
        $spec = $this->lastSpec();
        self::assertSame('8.3', $spec['php_version']);
        self::assertSame([['from' => '/old', 'to' => 'https://shop.example.com/new', 'code' => 302]], $spec['redirects']);
        self::assertTrue($spec['create_user']);
    }

    public function testPhpFpmSettingsReachTheAgent(): void
    {
        $this->svc->create(['domain' => 'fpm.test', 'kind' => 'php', 'php_version' => '8.3', 'pm' => 'dynamic', 'max_children' => '20',
            'start_servers' => '4', 'min_spare_servers' => '2', 'max_spare_servers' => '6', 'max_requests' => '1000', 'request_timeout_seconds' => '120', 'slowlog_seconds' => '5'], $this->admin);
        $php = $this->lastSpec()['php'];
        foreach (['pm' => 'dynamic', 'max_children' => 20, 'start_servers' => 4, 'min_spare_servers' => 2, 'max_spare_servers' => 6, 'max_requests' => 1000, 'request_timeout_seconds' => 120, 'slowlog_seconds' => 5, 'idle_timeout_seconds' => 10] as $k => $v) {
            self::assertSame($v, $php[$k], $k);
        }
        // Without them: the defaults existing websites have.
        $this->svc->create(['domain' => 'plain.test', 'kind' => 'php', 'php_version' => '8.3'], $this->admin);
        $php = $this->lastSpec()['php'];
        self::assertSame(['ondemand', 0, 500, 0, 10], [$php['pm'], $php['start_servers'], $php['max_requests'], $php['request_timeout_seconds'], $php['slowlog_seconds']]);
    }

    public function testValidationErrors(): void
    {
        $cases = [
            [['domain' => 'not a domain'], 'domain'],
            [['domain' => 'a.test', 'php_version' => '8.1'], 'php_version'],
            [['domain' => 'a.test', 'php_version' => '8.3', 'aliases' => 'bad_alias!'], 'aliases'],
            [['domain' => 'a.test', 'kind' => 'proxy', 'proxy_pass' => 'http://example.com'], 'proxy_pass'],
            [['domain' => 'a.test', 'php_version' => '8.3', 'system_user' => 'root'], 'system_user'],
            [['domain' => 'a.test', 'php_version' => '8.3', 'redirects' => '/x https://y/$1;'], 'redirects'],
            [['domain' => 'a.test', 'php_version' => '8.3', 'document_root_sub' => '../etc'], 'document_root_sub'],
            [['domain' => 'a.test', 'php_version' => '8.3', 'pm' => 'adaptive'], 'pm'],
            [['domain' => 'a.test', 'php_version' => '8.3', 'pm' => 'dynamic', 'max_children' => '4', 'min_spare_servers' => '3', 'start_servers' => '2', 'max_spare_servers' => '4'], 'start_servers'],
            [['domain' => 'a.test', 'php_version' => '8.3', 'request_timeout_seconds' => '-1'], 'request_timeout_seconds'],
            [['domain' => 'a.test', 'php_version' => '8.3', 'max_requests' => '0'], 'max_requests'],
        ];
        foreach ($cases as [$input, $field]) {
            try {
                $this->svc->create($input + ['kind' => 'php'], $this->admin);
                self::fail("accepted: " . json_encode($input));
            } catch (ValidationException $e) {
                self::assertArrayHasKey($field, $e->errors(), json_encode($input));
            }
        }
    }

    public function testDomainsAreUniqueAcrossWebsites(): void
    {
        $this->svc->create(['domain' => 'a.test', 'aliases' => 'www.a.test', 'kind' => 'static'], $this->admin);
        try {
            $this->svc->create(['domain' => 'b.test', 'aliases' => 'www.a.test', 'kind' => 'static'], $this->admin);
            self::fail('duplicate alias accepted');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('aliases', $e->errors());
        }
    }

    public function testFailedCreateLeavesNoRecord(): void
    {
        $this->agent->fail('sites.apply', 409, 'conflict', 'a.test is already configured in /etc/nginx/sites-enabled/a (configuration not managed by NimVor, enabled).');
        try {
            $this->svc->create(['domain' => 'a.test', 'kind' => 'static'], $this->admin);
            self::fail('expected agent error');
        } catch (AgentException $e) {
            self::assertStringContainsString('not managed by NimVor', $e->getMessage());
        }
        self::assertNull($this->repo->findByDomain(1, 'a.test'));
        self::assertNull($this->repo->domainOwner(1, 'a.test'));
    }

    public function testFailedUpdateRestoresPreviousState(): void
    {
        $id = $this->svc->create(['domain' => 'a.test', 'kind' => 'php', 'php_version' => '8.3', 'memory_limit_mb' => 128], $this->admin);
        $site = $this->repo->find(1, $id, null);
        $this->agent->fail('sites.apply', 409, 'failed', 'nginx -t rejected the new configuration.');
        try {
            $this->svc->update($site, ['aliases' => 'www.a.test', 'memory_limit_mb' => 512, 'kind' => 'php', 'php_version' => '8.3']);
            self::fail('expected failure');
        } catch (AgentException) {
        }
        $after = $this->repo->find(1, $id, null);
        self::assertSame([], $after['aliases']);
        self::assertSame(128, $after['php_settings']['memory_limit_mb']);
    }

    public function testDisableAndDelete(): void
    {
        $id = $this->svc->create(['domain' => 'a.test', 'kind' => 'static'], $this->admin);
        $this->svc->setEnabled($this->repo->find(1, $id, null), false);
        self::assertFalse($this->lastSpec()['enabled']);
        self::assertSame('disabled', $this->repo->find(1, $id, null)['status']);
        $this->svc->delete($this->repo->find(1, $id, null), true, false);
        self::assertNull($this->repo->find(1, $id, null));
        $actions = array_column($this->auditRows(), 'action');
        self::assertContains('website.delete', $actions);
        self::assertContains('website.disable', $actions);
    }

    public function testCustomersOnlySeeTheirOwnWebsites(): void
    {
        $alice = $this->users->find($this->createUser('alice', 'customer'));
        $bob = $this->users->find($this->createUser('bob', 'customer'));
        $aliceSite = $this->svc->create(['domain' => 'alice.test', 'kind' => 'static', 'owner_id' => $bob['id']], $alice);
        $this->svc->create(['domain' => 'bob.test', 'kind' => 'static'], $bob);

        self::assertSame(['alice.test'], array_column($this->repo->all(1, $alice), 'domain'), 'owner is forced to the creator');
        self::assertNull($this->repo->find(1, $aliceSite, $bob));
        self::assertCount(2, $this->repo->all(1, $this->admin));
    }
}
