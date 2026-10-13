<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Request;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\ApiTokenService;
use WhaCP\Services\FeatureService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Feature lists on plans, enforced by the server. */
final class FeatureManagerTest extends HttpTestCase
{
    private Connection $db;
    private int $cust;
    private int $reseller;
    private int $sub;
    private int $plan;
    private int $site;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->reseller = $this->seedUser('resell', 'reseller');
        $this->sub = $this->seedUser('sub', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$this->reseller, $this->sub]);
        $this->db->run("INSERT INTO plans (name, created_at, updated_at) VALUES ('Basic', 'x', 'x')");
        $this->plan = (int) $this->db->lastInsertId();
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [$this->plan, $this->cust]);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $users = $this->app->container->get(UserRepository::class);
        $this->site = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($this->cust));
    }

    private function switchTo(string $username): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($username);
    }

    /** Every feature except the given ones. @param list<string> $off */
    private function except(array $off): array
    {
        return array_values(array_diff(array_keys(FeatureService::FEATURES), $off));
    }

    public function testAnAdministratorBuildsAListAndAssignsItToAPlan(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/features')->body();
        self::assertStringContainsString('No feature lists yet', $page);
        $this->post('/features', ['name' => 'Basic Hosting', 'description' => 'No databases', 'features' => $this->except(['mysql', 'redis', 'cron'])]);
        $list = (int) $this->db->scalar("SELECT id FROM feature_lists WHERE name = 'Basic Hosting'");
        self::assertEqualsCanonicalizing(['mysql', 'redis', 'cron'], array_column($this->db->fetchAll('SELECT feature FROM feature_list_disabled WHERE list_id = ?', [$list]), 'feature'));
        self::assertStringContainsString(count(FeatureService::FEATURES) - 3 . ' of ' . count(FeatureService::FEATURES), $this->request('GET', '/features')->body());
        self::assertSame('feature_list.save', $this->db->scalar("SELECT action FROM audit_logs WHERE action = 'feature_list.save'"));

        $this->post('/plans', ['plan_id' => (string) $this->plan, 'name' => 'Basic', 'feature_list_id' => (string) $list] + array_fill_keys(array_keys(\WhaCP\Services\PlanService::FIELDS), '0'));
        self::assertSame($list, (int) $this->db->scalar('SELECT feature_list_id FROM plans WHERE id = ?', [$this->plan]));
    }

    public function testDisabledFeaturesAreRefusedByTheServerNotOnlyHidden(): void
    {
        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'Basic', 'features' => $this->except(['mysql', 'redis', 'stats'])], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $this->plan]);
        $token = $this->app->container->get(ApiTokenService::class)->create($this->cust, ['name' => 't', 'ability' => 'write', 'expires_days' => 30]);
        $this->login('cust');

        $site = $this->request('GET', '/sites/' . $this->site)->body();
        self::assertStringNotContainsString('/sites/' . $this->site . '/databases', $site);
        self::assertStringNotContainsString('/sites/' . $this->site . '/redis', $site);
        self::assertStringContainsString('/sites/' . $this->site . '/files', $site, 'included features stay');

        foreach (['/databases', '/sites/' . $this->site . '/databases', '/sites/' . $this->site . '/redis', '/sites/' . $this->site . '/stats'] as $path) {
            $res = $this->request('GET', $path);
            self::assertSame(403, $res->status(), $path);
        }
        self::assertStringContainsString('not included in your hosting plan', $this->request('GET', '/sites/' . $this->site . '/redis')->body());
        self::assertSame(403, $this->post('/databases', ['name' => 'x', 'website_id' => (string) $this->site])->status());
        self::assertSame(403, $this->post('/sites/' . $this->site . '/stats/enable', ['enabled' => '1'])->status());
        self::assertSame(200, $this->request('GET', '/sites/' . $this->site . '/files')->status());
        self::assertGreaterThan(0, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'security.access_denied' AND details LIKE '%feature%'"));

        // The API follows the same rules.
        $api = fn (string $path) => $this->app->handle(new Request('GET', $path, [], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $token]));
        self::assertSame(403, $api('/api/v1/databases')->status());
        self::assertSame(200, $api('/api/v1/websites')->status());

        // Administrators are never limited.
        $this->switchTo('admin');
        self::assertSame(200, $this->request('GET', '/sites/' . $this->site . '/redis')->status());
    }

    public function testAResellersPlanLimitsItsCustomers(): void
    {
        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'No cron', 'features' => $this->except(['cron'])], 1);
        $this->db->run("INSERT INTO plans (name, feature_list_id, created_at, updated_at) VALUES ('Reseller', ?, 'x', 'x')", [$list]);
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [(int) $this->db->lastInsertId(), $this->reseller]);
        $users = $this->app->container->get(UserRepository::class);
        $subSite = $this->app->container->get(WebsiteService::class)->create(['domain' => 'sub.test', 'kind' => 'static'], (array) $users->find($this->sub));
        $this->login('sub');
        self::assertSame(403, $this->request('GET', '/sites/' . $subSite . '/cron')->status(), 'the customer has no plan of its own');
        self::assertSame(200, $this->request('GET', '/sites/' . $subSite . '/files')->status());
    }

    public function testSiteCreationTypesAreGatedByTheList(): void
    {
        $this->login('cust'); // plan with no list: default-off types (proxy/node/python) off
        $form = $this->request('GET', '/websites/new')->body();
        self::assertStringContainsString('PHP application', $form);
        self::assertStringContainsString('Static site', $form);
        self::assertStringNotContainsString('Reverse proxy', $form, 'proxy type hidden when not included');
        self::assertStringNotContainsString('Node.js', $form);

        // Enforced server-side even if the form is bypassed.
        $before = (int) $this->db->scalar('SELECT COUNT(*) FROM websites');
        $this->post('/websites/new', ['domain' => 'node.test', 'kind' => 'proxy', 'proxy_pass' => 'http://127.0.0.1:3000']);
        self::assertArrayHasKey('kind', (array) $this->flash('errors'));
        self::assertSame($before, (int) $this->db->scalar('SELECT COUNT(*) FROM websites'));

        // A static site (on by default) is allowed.
        $this->post('/websites/new', ['domain' => 'static2.test', 'kind' => 'static']);
        self::assertNotNull($this->db->scalar("SELECT id FROM websites WHERE domain = 'static2.test'"));

        // Opt the plan into every type: proxy is now shown and allowed.
        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'All types', 'features' => $this->except([])], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $this->plan]);
        self::assertStringContainsString('Reverse proxy', $this->request('GET', '/websites/new')->body());
    }

    public function testScheduledBackupsHiddenFromCustomerUnlessOptedIn(): void
    {
        $serverId = (int) $this->db->scalar('SELECT server_id FROM websites WHERE id = ?', [$this->site]);
        $this->db->run('INSERT INTO backup_schedules (server_id, website_id, kind, frequency, created_at) VALUES (?, ?, ?, ?, ?)', [$serverId, $this->site, 'site', 'daily', 'x']);
        $sched = (int) $this->db->lastInsertId();
        $ins = 'INSERT INTO backups (server_id, website_id, schedule_id, kind, domain, backup_id, label, manifest, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)';
        $this->db->run($ins, [$serverId, $this->site, null, 'site', 'shop.test', 'manual01', 'MANUALBACKUP', '{}', 'succeeded', 'x']);
        $this->db->run($ins, [$serverId, $this->site, $sched, 'site', 'shop.test', 'auto0001', 'AUTOBACKUP', '{}', 'succeeded', 'x']);
        $this->agent->succeed('backups.refresh', ['ok' => true]);

        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->site . '/backups')->body();
        self::assertStringContainsString('MANUALBACKUP', $page);
        self::assertStringNotContainsString('AUTOBACKUP', $page, 'scheduled backups are hidden by default');

        // Opt in: the scheduled copy appears.
        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'Auto', 'features' => $this->except([])], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $this->plan]);
        self::assertStringContainsString('AUTOBACKUP', $this->request('GET', '/sites/' . $this->site . '/backups')->body());
    }

    public function testSubdomainsAndAddonsAreSeparateFeatures(): void
    {
        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'NoAddons', 'features' => $this->except(['addon_domains'])], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $this->plan]);
        $this->login('cust');
        self::assertSame(200, $this->request('GET', '/sites/' . $this->site . '/subdomains')->status());
        self::assertSame(403, $this->request('GET', '/sites/' . $this->site . '/addons')->status());
        self::assertSame(403, $this->post('/sites/' . $this->site . '/addons', ['type' => 'addon', 'domain' => 'x.test'])->status());
    }

    public function testSiteSecurityAndHealthPagesAreGated(): void
    {
        $this->login('cust');
        $sec = $this->request('GET', '/sites/' . $this->site . '/security')->body();
        self::assertStringContainsString('HTTPS is off', $sec);
        self::assertStringContainsString('You can fix this', $sec);
        self::assertStringContainsString('No successful backup yet', $this->request('GET', '/sites/' . $this->site . '/health')->body());

        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'NoSecHealth', 'features' => $this->except(['site_security', 'site_health'])], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $this->plan]);
        self::assertSame(403, $this->request('GET', '/sites/' . $this->site . '/security')->status());
        self::assertSame(403, $this->request('GET', '/sites/' . $this->site . '/health')->status());
    }

    public function testMemcachedPageIsGatedAndInformational(): void
    {
        $this->agent->succeed('memcached.status', ['installed' => true, 'active' => true, 'config' => ['port' => 11211]]);
        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->site . '/memcached')->body();
        self::assertStringContainsString('127.0.0.1', $page);
        self::assertStringContainsString('Connect your application', $page);

        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'NoCache', 'features' => $this->except(['memcached'])], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $this->plan]);
        self::assertSame(403, $this->request('GET', '/sites/' . $this->site . '/memcached')->status());
    }

    public function testWebFirewallIsGatedByItsFeature(): void
    {
        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'NoFirewall', 'features' => $this->except(['firewall'])], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $this->plan]);
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/sites/' . $this->site . '/firewall')->status());
        self::assertStringNotContainsString('/sites/' . $this->site . '/firewall', $this->request('GET', '/sites/' . $this->site)->body());
    }

    public function testDefaultOffFeaturesAreOffUnlessAListOptsIn(): void
    {
        $features = $this->app->container->get(FeatureService::class);
        $users = $this->app->container->get(UserRepository::class);
        $cust = (array) $users->find($this->cust);

        // A list that opts into nothing extra: default-off features stay off,
        // default-on ones (type_php) stay on.
        $list = $features->save(null, ['name' => 'NoAuto', 'features' => $this->except(['auto_backups', 'type_nodejs'])], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $this->plan]);
        self::assertFalse($features->enabledFor($cust, 'auto_backups'), 'scheduled backups off by default');
        self::assertFalse($features->enabledFor($cust, 'type_nodejs'), 'node sites off by default');
        self::assertTrue($features->enabledFor($cust, 'type_php'), 'php sites on by default');
        self::assertTrue($features->enabledFor($cust, 'type_proxy'), 'opted-in default-off feature is on');
        self::assertSame([], array_column($this->db->fetchAll('SELECT feature FROM feature_list_enabled WHERE list_id = ? AND feature = ?', [$list, 'auto_backups']), 'feature'));

        // Opt in: now on.
        $features->save($list, ['name' => 'NoAuto', 'features' => $this->except([])], 1);
        self::assertTrue($features->enabledFor($cust, 'auto_backups'));
        self::assertTrue($features->enabledFor($cust, 'type_nodejs'));

        // A plan with no list: default-off features are off, default-on on.
        // A new service instance avoids the request cache after a raw update.
        $this->db->run('UPDATE plans SET feature_list_id = NULL WHERE id = ?', [$this->plan]);
        $fresh = new FeatureService($this->db);
        self::assertContains('auto_backups', $fresh->disabledFor($cust));
        self::assertNotContains('type_php', $fresh->disabledFor($cust));
    }

    public function testOnlyAdministratorsManageListsAndDeletingOneRestoresEveryFeature(): void
    {
        $features = $this->app->container->get(FeatureService::class);
        $list = $features->save(null, ['name' => 'Tiny', 'features' => []], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $this->plan]);
        $this->login('resell');
        self::assertSame(403, $this->request('GET', '/features')->status());
        $this->post('/features/' . $list . '/delete');
        self::assertNotNull($this->db->scalar('SELECT id FROM feature_lists WHERE id = ?', [$list]));
        // A reseller's plan cannot pick a feature list.
        $this->post('/plans', ['name' => 'Mine', 'feature_list_id' => (string) $list] + array_fill_keys(array_keys(\WhaCP\Services\PlanService::FIELDS), '0'));
        self::assertNull($this->db->scalar("SELECT feature_list_id FROM plans WHERE name = 'Mine'"));

        $this->switchTo('admin');
        $this->post('/features/' . $list . '/delete');
        self::assertNull($this->db->scalar('SELECT feature_list_id FROM plans WHERE id = ?', [$this->plan]));
        $this->switchTo('cust');
        self::assertSame(200, $this->request('GET', '/sites/' . $this->site . '/redis')->status());
    }
}
