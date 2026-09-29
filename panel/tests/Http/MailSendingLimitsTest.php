<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Services\MailRateService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Outgoing mail limits: settings, use, reset, alerts, access. */
final class MailSendingLimitsTest extends HttpTestCase
{
    private const STATE = [
        'configured' => true,
        'service' => ['active' => true, 'postfix' => true],
        'accounts' => ['blog', 'shop'],
        'config' => ['enabled' => true, 'per_hour' => 200, 'accounts' => ['shop' => 50]],
        'usage' => [
            'hour' => 1790000000,
            'accounts' => [['uid' => 1003, 'name' => 'shop', 'sent' => 50, 'limit' => 50, 'blocked' => true], ['uid' => 1004, 'name' => 'blog', 'sent' => 3, 'limit' => 200, 'blocked' => false]],
            'events' => [],
        ],
    ];

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('mail.rate_get', self::STATE);
        $this->agent->succeed('mail.rate_set', self::STATE);
        $this->agent->succeed('mail.rate_reset', self::STATE);
    }

    public function testPageShowsUseAndLimits(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/mail/admin/sending-limits')->body();
        foreach (['Recipients per website account and hour', 'name="accounts[shop]" value="50"', 'name="accounts[blog]" value=""', 'Limit reached', 'name="account" value="shop"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('name="account" value="blog"', $page, 'no reset for an account under its limit');
    }

    public function testSaveValidatesAndSends(): void
    {
        $this->login('admin');
        $this->post('/mail/admin/sending-limits', ['enabled' => '1', 'per_hour' => '100', 'accounts' => ['shop' => '20', 'blog' => '']]);
        $call = $this->lastCall('mail.rate_set');
        self::assertSame([true, 100], [$call['enabled'], $call['per_hour']]);
        self::assertEquals(['shop' => 20], (array) $call['accounts']);
        self::assertStringContainsString('they are on', (string) $this->flash('success'));

        $before = $this->calls('mail.rate_set');
        foreach ([['per_hour' => '-1'], ['per_hour' => 'x'], ['per_hour' => '10', 'accounts' => ['root' => '5']], ['per_hour' => '10', 'accounts' => ['shop' => '2000000']]] as $bad) {
            $this->post('/mail/admin/sending-limits', ['enabled' => '1'] + $bad);
            self::assertNotNull($this->flash('error'), json_encode($bad));
        }
        self::assertSame($before, $this->calls('mail.rate_set'), 'nothing invalid reached the agent');
    }

    public function testTurningOnReappliesPhpWebsitesFirst(): void
    {
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $sites = $this->app->container->get(WebsiteService::class);
        $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3', 'system_user' => 'shop'], ['role_scope' => 'all', 'id' => 1]);
        $sites->create(['domain' => 'proxy.test', 'kind' => 'proxy', 'proxy_pass' => 'http://127.0.0.1:3000', 'system_user' => 'proxy'], ['role_scope' => 'all', 'id' => 1]);
        $off = self::STATE;
        $off['config']['enabled'] = false;
        $this->agent->succeed('mail.rate_get', $off);
        $this->login('admin');
        $applies = $this->calls('sites.apply');
        $this->post('/mail/admin/sending-limits', ['enabled' => '1', 'per_hour' => '100']);
        self::assertSame($applies + 1, $this->calls('sites.apply'), 'the PHP website re-applied, not the proxy');
        self::assertSame(1, $this->calls('mail.rate_set'));

        // A website that cannot be updated keeps the limits off.
        $this->agent->fail('sites.apply', 500, 'failed', 'php-fpm refused');
        $this->post('/mail/admin/sending-limits', ['enabled' => '1', 'per_hour' => '100']);
        self::assertStringContainsString('shop.test', (string) $this->flash('error'));
        self::assertSame(1, $this->calls('mail.rate_set'), 'not turned on');
        // Already on: saving does not re-apply the websites.
        $this->agent->succeed('mail.rate_get', self::STATE);
        $this->post('/mail/admin/sending-limits', ['enabled' => '1', 'per_hour' => '100']);
        self::assertSame(2, $this->calls('mail.rate_set'));
    }

    public function testReset(): void
    {
        $this->login('admin');
        $this->post('/mail/admin/sending-limits/reset', ['account' => 'shop']);
        self::assertSame('shop', $this->lastCall('mail.rate_reset')['account']);
        $this->post('/mail/admin/sending-limits/reset', ['account' => '']);
        self::assertSame('', $this->lastCall('mail.rate_reset')['account']);
    }

    public function testCustomersHaveNoAccess(): void
    {
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/mail/admin/sending-limits')->status());
        $this->post('/mail/admin/sending-limits', ['enabled' => '0', 'per_hour' => '0']);
        $this->post('/mail/admin/sending-limits/reset', ['account' => 'shop']);
        self::assertSame(0, $this->calls('mail.rate_set'));
        self::assertSame(0, $this->calls('mail.rate_reset'));
    }

    public function testLimitReachedNotifiesOnce(): void
    {
        $db = $this->app->container->get(Connection::class);
        $cust = (int) $db->scalar("SELECT id FROM users WHERE username = 'cust'");
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3', 'system_user' => 'shop'], ['role_scope' => 'all', 'id' => 1]);
        $db->run('UPDATE websites SET owner_id = ? WHERE id = ?', [$cust, $siteId]);
        $state = self::STATE;
        $state['usage']['events'] = [['uid' => 1003, 'name' => 'shop', 'time' => time() - 60, 'limit' => 50], ['uid' => 1004, 'name' => 'blog', 'time' => time() - 3 * 86400, 'limit' => 200]];
        $this->agent->succeed('mail.rate_get', $state);
        $svc = $this->app->container->get(MailRateService::class);
        self::assertSame(['Mail sending limit reached: shop'], $svc->alerts());
        self::assertSame([], $svc->alerts(), 'reported once');
        $rows = $db->fetchAll("SELECT title, website_id, owner_id FROM notifications WHERE kind = 'mail.limit' ORDER BY id");
        self::assertCount(2, $rows);
        self::assertNull($rows[0]['website_id'], 'the administrators\' notice');
        self::assertSame([$siteId, $cust], [(int) $rows[1]['website_id'], (int) $rows[1]['owner_id']], 'the owner\'s notice');

        // Off: nothing is reported.
        $state['config']['enabled'] = false;
        $this->agent->succeed('mail.rate_get', $state);
        self::assertSame([], $svc->alerts());
    }
}
