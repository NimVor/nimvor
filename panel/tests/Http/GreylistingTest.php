<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** Greylisting: settings, trusted hosts, providers, report and cleanup. */
final class GreylistingTest extends HttpTestCase
{
    private const STATE = [
        'configured' => true,
        'service' => ['active' => true, 'socket' => true, 'postfix' => true],
        'providers' => [['id' => 'google', 'name' => 'Google (Gmail, Workspace)', 'spf' => ['_spf.google.com']], ['id' => 'microsoft', 'name' => 'Microsoft', 'spf' => ['spf.protection.outlook.com']]],
        'config' => [
            'enabled' => true, 'deferral' => 5, 'resend' => 240, 'expire' => 4320, 'spf_bypass' => true, 'auto_trust_new' => true,
            'trusted' => [['range' => '192.0.2.0/24', 'comment' => 'office <b>'], ['range' => '198.51.100.7', 'comment' => '']],
            'providers' => ['google' => ['trust' => true, 'auto_update' => true, 'ranges' => ['209.85.128.0/17'], 'updated' => 1790000000], 'microsoft' => ['trust' => false, 'auto_update' => true]],
        ],
    ];

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('resell', 'reseller');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('mail.greylist_get', self::STATE);
        $this->agent->succeed('mail.greylist_set', self::STATE);
        $this->agent->succeed('mail.greylist_update_providers', self::STATE);
        $this->agent->succeed('mail.greylist_cleanup', ['removed' => 2]);
        $this->agent->succeed('mail.greylist_report', ['running' => true, 'total' => 120, 'counts' => ['deferring' => 100, 'waiting' => 10, 'passed' => 10, 'expired' => 0], 'rows' => [
            ['ip' => '203.0.113.9', 'from' => 'alice@sender.test', 'to' => 'bob@<here>.test', 'deferred' => 2, 'accepted' => 0, 'created' => 1790000000, 'block_expire' => 1790000300, 'must_retry' => 1790014700, 'record_expire' => 1790014700, 'status' => 'deferring'],
        ]]);
    }

    public function testSettingsPage(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/mail/admin/greylisting')->body();
        foreach (['Initial deferral period (minutes)', 'Resend acceptance period (minutes)', 'Record expiration time (minutes)', 'Bypass greylisting for hosts with valid SPF records', 'Automatically trust newly added mail providers', '192.0.2.0/24', 'office &lt;b&gt;', 'Google (Gmail, Workspace)', 'name="trust[]" value="google"', 'id="trusted-new"', 'id="trusted-1"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        // Settings, trusted hosts and providers are tabs of one page (no reload).
        foreach (['greylist-settings', 'trusted', 'providers'] as $pane) {
            self::assertStringContainsString('data-bs-target="#' . $pane . '"', $page, $pane);
            self::assertStringContainsString('id="' . $pane . '" role="tabpanel"', $page, $pane);
        }
    }

    public function testSettingsSendTheWholeConfiguration(): void
    {
        $this->login('admin');
        $this->post('/mail/admin/greylisting', ['enabled' => '1', 'deferral' => '10', 'resend' => '300', 'expire' => '10000', 'spf_bypass' => '0', 'auto_trust_new' => '1']);
        $call = $this->lastCall('mail.greylist_set');
        self::assertSame([true, 10, 300, 10000, false, true], [$call['enabled'], $call['deferral'], $call['resend'], $call['expire'], $call['spf_bypass'], $call['auto_trust_new']]);
        self::assertCount(2, $call['trusted'], 'trusted hosts kept');
        self::assertEquals(['trust' => true, 'auto_update' => true], ((array) $call['providers'])['google']);
        self::assertStringContainsString('greylisting is on', (string) $this->flash('success'));
    }

    public function testTrustedHostsAndProviders(): void
    {
        $this->login('admin');
        $this->post('/mail/admin/greylisting/trusted', ['range' => '2001:db8::/48', 'comment' => 'v6']);
        self::assertSame(['range' => '2001:db8::/48', 'comment' => 'v6'], $this->lastCall('mail.greylist_set')['trusted'][2]);
        $this->post('/mail/admin/greylisting/trusted', ['index' => '1', 'range' => '198.51.100.8', 'comment' => 'moved']);
        $t = $this->lastCall('mail.greylist_set')['trusted'];
        self::assertCount(2, $t);
        self::assertSame('198.51.100.8', $t[1]['range']);
        $this->post('/mail/admin/greylisting/trusted/0/delete');
        self::assertSame([['range' => '198.51.100.7', 'comment' => '']], $this->lastCall('mail.greylist_set')['trusted']);
        $before = $this->calls('mail.greylist_set');
        $this->post('/mail/admin/greylisting/trusted/x/delete');
        self::assertSame($before, $this->calls('mail.greylist_set'), 'a non-numeric index deletes nothing');
        $this->post('/mail/admin/greylisting/providers', ['trust' => ['microsoft'], 'auto_update' => ['google', 'evil']]);
        self::assertEquals(['google' => ['trust' => false, 'auto_update' => true], 'microsoft' => ['trust' => true, 'auto_update' => false]], (array) $this->lastCall('mail.greylist_set')['providers']);
        $this->post('/mail/admin/greylisting/providers/update');
        self::assertTrue($this->lastCall('mail.greylist_update_providers')['all']);
    }

    public function testReportAndCleanup(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/mail/admin/greylisting/report', [], [], '', ['search' => 'alice', 'status' => 'deferring', 'sort' => 'ip', 'dir' => 'asc', 'page' => '2', 'per' => '25', 'x' => 'y'])->body();
        $call = $this->lastCall('mail.greylist_report');
        self::assertSame(['alice', 'deferring', 'ip', false, 2, 25], [$call['search'], $call['status'], $call['sort'], $call['desc'], $call['page'], $call['per']]);
        foreach (['bob@&lt;here&gt;.test', 'Page 2 of 5', 'Block expire time', 'Must retry time', 'Record expire time', 'aria-sort="ascending"', 'Remove expired'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->request('GET', '/mail/admin/greylisting/report', [], [], '', ['sort' => 'evil', 'status' => 'nope', 'per' => '99999']);
        $call = $this->lastCall('mail.greylist_report');
        self::assertSame(['created', '', true, 50], [$call['sort'], $call['status'], $call['desc'], $call['per']]);

        $this->post('/mail/admin/greylisting/report', ['op' => 'delete', 'sel' => [json_encode(['203.0.113.9', 'alice@sender.test', 'bob@here.test']), 'garbage', json_encode(['a' => 1])]]);
        self::assertSame(['op' => 'delete', 'triplets' => [['ip' => '203.0.113.9', 'from' => 'alice@sender.test', 'to' => 'bob@here.test']]], $this->lastCall('mail.greylist_cleanup'));
        $this->post('/mail/admin/greylisting/report', ['op' => 'purge']);
        self::assertSame('purge', $this->lastCall('mail.greylist_cleanup')['op']);
        $n = $this->calls('mail.greylist_cleanup');
        $this->post('/mail/admin/greylisting/report', ['op' => 'delete']);
        $this->post('/mail/admin/greylisting/report', ['op' => 'rm -rf']);
        self::assertSame($n, $this->calls('mail.greylist_cleanup'));
    }

    public function testOnlyServerAdministrators(): void
    {
        foreach (['resell', 'cust'] as $who) {
            $this->session = new ArraySession();
            $this->app = $this->makeApp();
            $this->login($who);
            self::assertNotSame(200, $this->request('GET', '/mail/admin/greylisting')->status(), $who);
            self::assertNotSame(200, $this->request('GET', '/mail/admin/greylisting/report')->status(), $who);
            $this->post('/mail/admin/greylisting', ['enabled' => '0', 'deferral' => '5', 'resend' => '5', 'expire' => '100']);
            $this->post('/mail/admin/greylisting/trusted', ['range' => '0.0.0.0/0']);
            $this->post('/mail/admin/greylisting/report', ['op' => 'delete_all']);
        }
        self::assertSame(0, $this->calls('mail.greylist_get'));
        self::assertSame(0, $this->calls('mail.greylist_set'));
        self::assertSame(0, $this->calls('mail.greylist_cleanup'));
    }
}
