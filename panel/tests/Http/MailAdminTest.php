<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Http\Session\ArraySession;
use WhaCP\Tests\Support\HttpTestCase;

/** Email administration: queue, delivery reports, statistics. */
final class MailAdminTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('resell', 'reseller');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('mail.queue', ['total' => 2, 'counts' => ['deferred' => 1, 'hold' => 1], 'messages' => [
            ['queue' => 'deferred', 'id' => '7506CE0062', 'arrival' => time() - 7200, 'size' => 2048, 'sender' => 'shop@example.test', 'recipients' => ['x@nowhere.invalid'], 'reason' => 'Host or domain name not found'],
            ['queue' => 'hold', 'id' => 'ABCDEF1234', 'arrival' => time() - 60, 'size' => 10, 'sender' => '', 'recipients' => ['y@z.test']],
        ]]);
        $this->agent->succeed('mail.queue_action', ['ok' => true]);
        $this->agent->succeed('mail.queue_message', ['id' => '7506CE0062', 'headers' => ['from' => 'Shop <shop@example.test>', 'subject' => 'Order <script>']]);
        $this->agent->succeed('mail.report', ['source' => 'journal', 'report' => [
            'totals' => ['received' => 12, 'sent' => 7, 'bounced' => 2, 'auth_failed' => 5],
            'series' => [['start' => '2026-09-27T10:00:00Z', 'received' => 12, 'sent' => 7]],
            'deliveries' => [['time' => '2026-09-27T10:05:02Z', 'id' => '6F7A8B9C0D', 'from' => 'me@shop.test', 'to' => 'x@nowhere.invalid', 'relay' => 'none', 'status' => 'deferred', 'dsn' => '4.4.3', 'delay' => '1.9', 'reason' => 'Host not found']],
            'relayers' => [['kind' => 'local', 'name' => 'uid 1001 (shop@mx)', 'messages' => 40]],
            'senders' => [['kind' => 'domain', 'name' => 'shop.test', 'messages' => 7]], 'truncated' => false,
        ]]);
        $this->agent->succeed('mail.server_status', [
            'services' => [
                ['unit' => 'postfix.service', 'name' => 'Postfix', 'active' => true, 'enabled' => true, 'version' => '3.8.6'],
                ['unit' => 'dovecot.service', 'name' => 'Dovecot', 'active' => false, 'enabled' => true, 'version' => '2.3.21'],
            ],
            'checks' => ['postfix' => '', 'dovecot' => 'doveconf: Error: <b>broken</b>'],
            'values' => ['postfix' => ['message_size_limit' => '52428800', 'maximal_queue_lifetime' => '5d', 'relayhost' => ''], 'dovecot' => ['login_greeting' => '']],
            'labels' => ['postfix' => ['message_size_limit' => 'Largest message (MB)', 'maximal_queue_lifetime' => 'Keep retrying undelivered mail (days)', 'relayhost' => 'Smart host'], 'dovecot' => ['login_greeting' => 'Login greeting']],
        ]);
        $this->agent->succeed('mail.log_tail', ['source' => 'journal', 'lines' => [['time' => '2026-09-27T10:05:02Z', 'ident' => 'postfix/smtp', 'message' => 'to=<a@b.test>, status=sent <x>']]]);
        $this->agent->succeed('mail.settings_set', ['ok' => true]);
        $this->agent->succeed('services.control', ['ok' => true]);
        $this->agent->succeed('mail.trace', ['address' => 'sales@shop.test', 'steps' => [
            ['depth' => 0, 'address' => 'sales@shop.test', 'result' => 'forward', 'detail' => 'Forwarded to me@shop.test <x>.'],
            ['depth' => 1, 'address' => 'me@shop.test', 'result' => 'mailbox', 'detail' => 'Delivered to the mailbox me@shop.test.'],
        ]]);
        $this->agent->succeed('mail.repair_permissions', ['dirs' => 12, 'files' => 40, 'fixed' => 3, 'skipped' => ['shop.test/me/evil']]);
    }

    public function testToolsTraceAndRepair(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/mail/admin/tools', [], [], '', ['address' => ' Sales@shop.test '])->body();
        self::assertSame('Sales@shop.test', $this->lastCall('mail.trace')['address']);
        foreach (['Forwarded to me@shop.test &lt;x&gt;.', 'wcp-trace-1', 'Repair mailbox permissions', 'Maildir'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertSame(0, $this->calls('mail.repair_permissions'));
        $this->post('/mail/admin/tools/repair', ['domain' => 'Shop.test']);
        self::assertSame(['domain' => 'shop.test'], $this->lastCall('mail.repair_permissions'));
        self::assertSame('Checked 12 folder(s) and 40 file(s); fixed 3. Skipped 1 link(s) or special file(s): shop.test/me/evil.', $this->flash('success'));
    }

    public function testMailServerPageAndSettings(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/mail/admin/server')->body();
        foreach (['3.8.6', 'Stopped', '&lt;b&gt;broken&lt;/b&gt;', 'name="postfix[message_size_limit]"', 'value="50"', 'status=sent &lt;x&gt;', 'value="start"', 'value="/mail/admin/server"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post('/mail/admin/server', [
            'postfix' => ['message_size_limit' => '25', 'maximal_queue_lifetime' => '5', 'relayhost' => ''],
            'current' => ['message_size_limit' => '50', 'maximal_queue_lifetime' => '5', 'relayhost' => ''],
            'dovecot' => ['login_greeting' => 'Mail ready'],
        ]);
        $call = $this->lastCall('mail.settings_set');
        self::assertEquals(['message_size_limit' => '25'], (array) $call['postfix'], 'only changed Postfix values are sent');
        self::assertEquals(['login_greeting' => 'Mail ready'], (array) $call['dovecot']);
        self::assertStringContainsString('applied', (string) $this->flash('success'));
        $res = $this->post('/services/control', ['unit' => 'dovecot.service', 'op' => 'start', 'return_to' => '/mail/admin/server']);
        self::assertSame('/mail/admin/server', $res->header('location'));
        $res = $this->post('/services/control', ['unit' => 'dovecot.service', 'op' => 'start', 'return_to' => 'https://evil.test/']);
        self::assertSame('/services', $res->header('location'), 'return_to is an allow-list');
    }

    public function testTheQueueCanBeListedAndManaged(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/mail/admin/queue')->body();
        foreach (['2 message(s) in the queue', '7506CE0062', 'Host or domain name not found', '(bounce)', 'data-wcp-confirm-submit'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post('/mail/admin/queue', ['action' => 'delete', 'ids' => ['7506CE0062', 'bad id; rm']]);
        self::assertSame(['action' => 'delete', 'ids' => ['7506CE0062']], $this->lastCall('mail.queue_action'), 'invalid IDs are dropped');
        $this->post('/mail/admin/queue', ['action' => 'flush']);
        self::assertSame('flush', $this->lastCall('mail.queue_action')['action']);
        $this->post('/mail/admin/queue', ['action' => 'hold', 'ids' => []]);
        self::assertStringContainsString('Select messages first', (string) $this->flash('error'));
        $json = json_decode($this->request('GET', '/mail/admin/queue/7506CE0062', [], ['accept' => 'application/json'])->body(), true);
        self::assertSame('Order <script>', $json['headers']['subject'], 'plain data; the page renders it as text');
        self::assertSame('mail.queue_delete', $this->app->container->get(\WhaCP\Database\Connection::class)->scalar("SELECT action FROM audit_logs WHERE action = 'mail.queue_delete'"));
    }

    public function testReportsAndStatistics(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/mail/admin/reports', [], [], '', ['range' => '7d', 'address' => 'nowhere', 'status' => 'deferred'])->body();
        self::assertStringContainsString('x@nowhere.invalid', $page);
        $call = $this->lastCall('mail.report');
        self::assertSame(['nowhere', 'deferred', 500], [$call['address'], $call['status'], $call['limit']]);
        self::assertEqualsWithDelta(7 * 86400, strtotime($call['until']) - strtotime($call['since']), 5);
        $stats = $this->request('GET', '/mail/admin/reports', [], [], '', ['view' => 'stats'])->body();
        foreach (['Failed logins', 'uid 1001 (shop@mx)', 'server account', 'shop.test'] as $want) {
            self::assertStringContainsString($want, $stats, $want);
        }
    }

    public function testOnlyServerAdministrators(): void
    {
        foreach (['resell', 'cust'] as $who) {
            $this->session = new ArraySession();
            $this->app = $this->makeApp();
            $this->login($who);
            self::assertNotSame(200, $this->request('GET', '/mail/admin/queue')->status(), $who);
            self::assertNotSame(200, $this->request('GET', '/mail/admin/reports')->status(), $who);
            self::assertNotSame(200, $this->request('GET', '/mail/admin/server')->status(), $who);
            $this->post('/mail/admin/server', ['postfix' => ['relayhost' => 'evil.test']]);
            self::assertNotSame(200, $this->request('GET', '/mail/admin/tools', [], [], '', ['address' => 'a@b.test'])->status(), $who);
            $this->post('/mail/admin/tools/repair', ['domain' => '']);
            $this->post('/mail/admin/queue', ['action' => 'delete', 'ids' => ['7506CE0062']]);
        }
        self::assertSame(0, $this->calls('mail.queue_action'));
        self::assertSame(0, $this->calls('mail.report'));
        self::assertSame(0, $this->calls('mail.server_status'));
        self::assertSame(0, $this->calls('mail.settings_set'));
        self::assertSame(0, $this->calls('mail.trace'));
        self::assertSame(0, $this->calls('mail.repair_permissions'));
    }
}
