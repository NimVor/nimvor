<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ApiTokenService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: the mail server's administration. */
final class MailAdminApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'reseller');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('mail.queue', ['total' => 2, 'counts' => ['deferred' => 1, 'hold' => 1], 'messages' => [['queue' => 'deferred', 'id' => '7506CE0062', 'sender' => 'shop@example.test', 'recipients' => ['x@nowhere.invalid']]]]);
        $this->agent->succeed('mail.queue_action', ['ok' => true]);
        $this->agent->succeed('mail.queue_message', ['id' => '7506CE0062', 'headers' => ['subject' => 'Order']]);
        $this->agent->succeed('mail.server_status', [
            'services' => [['unit' => 'postfix.service', 'name' => 'Postfix', 'active' => true]], 'checks' => ['postfix' => '', 'dovecot' => ''],
            'values' => ['postfix' => ['message_size_limit' => '52428800', 'relayhost' => ''], 'dovecot' => ['login_greeting' => '']],
        ]);
        $this->agent->succeed('mail.settings_set', ['ok' => true]);
        $this->agent->succeed('mail.log_tail', ['source' => 'journal', 'lines' => []]);
        $this->agent->succeed('mail.trace', ['address' => 'sales@shop.test', 'steps' => [['depth' => 0, 'result' => 'mailbox']]]);
        $this->agent->succeed('mail.repair_permissions', ['dirs' => 1, 'files' => 2, 'fixed' => 0, 'skipped' => []]);
        $this->agent->succeed('mail.report', ['source' => 'journal', 'report' => ['totals' => ['sent' => 7]]]);
        $state = [
            'configured' => true, 'providers' => [['id' => 'google', 'name' => 'Google'], ['id' => 'microsoft', 'name' => 'Microsoft']],
            'config' => ['enabled' => true, 'deferral' => 5, 'resend' => 240, 'expire' => 4320, 'spf_bypass' => true, 'auto_trust_new' => true,
                'trusted' => [['range' => '192.0.2.0/24', 'comment' => 'office'], ['range' => '198.51.100.7', 'comment' => '']],
                'providers' => ['google' => ['trust' => true, 'auto_update' => true], 'microsoft' => ['trust' => false, 'auto_update' => true]]],
        ];
        foreach (['mail.greylist_get', 'mail.greylist_set', 'mail.greylist_update_providers'] as $a) {
            $this->agent->succeed($a, $state);
        }
        $this->agent->succeed('mail.greylist_cleanup', ['removed' => 2]);
        $this->agent->succeed('mail.greylist_report', ['running' => true, 'total' => 1, 'rows' => [['ip' => '203.0.113.9', 'status' => 'deferring']]]);
        $rate = ['configured' => true, 'accounts' => ['blog', 'shop'], 'config' => ['enabled' => false, 'per_hour' => 200, 'accounts' => ['shop' => 50]], 'usage' => ['accounts' => [], 'events' => []]];
        foreach (['mail.rate_get', 'mail.rate_set', 'mail.rate_reset'] as $a) {
            $this->agent->succeed($a, $rate);
        }
    }

    /** @param array<string, mixed> $json */
    private function api(string $method, string $path, ?string $token, array $json = [], array $query = []): Response
    {
        $h = ['content-type' => 'application/json'] + ($token === null ? [] : ['authorization' => 'Bearer ' . $token]);
        return $this->app->handle(new Request($method, $path, $query, [], ['REMOTE_ADDR' => '192.0.2.50'], $h, $json === [] ? '' : (string) json_encode($json)));
    }

    /** @return array<string, mixed> */
    private static function json(Response $r): array
    {
        return (array) json_decode($r->body(), true);
    }

    private function token(int $userId, string $ability = 'write'): string
    {
        return $this->app->container->get(ApiTokenService::class)->create($userId, ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => $ability, 'all_scopes' => '1', 'expires_days' => 30]);
    }

    private function audited(string $action): bool
    {
        return $this->db->scalar('SELECT id FROM audit_logs WHERE action = ?', [$action]) !== null;
    }

    public function testEverythingIsForAdministrators(): void
    {
        $cust = $this->token($this->cust);
        foreach ([
            ['GET', '/mail/admin/queue'], ['GET', '/mail/admin/server'], ['GET', '/mail/admin/greylisting'], ['GET', '/mail/admin/sending-limits'],
            ['POST', '/mail/admin/queue/actions'], ['PATCH', '/mail/admin/server'], ['POST', '/mail/admin/repair'], ['PUT', '/mail/admin/greylisting/providers'], ['PATCH', '/mail/admin/sending-limits'],
        ] as [$m, $p]) {
            self::assertSame(403, $this->api($m, '/api/v1' . $p, $cust, $m === 'GET' ? [] : ['x' => 1])->status(), "$m $p");
        }
        self::assertSame(401, $this->api('GET', '/api/v1/mail/admin/queue', null)->status());
        self::assertSame(403, $this->api('POST', '/api/v1/mail/admin/queue/actions', $this->token($this->admin, 'read'), ['action' => 'flush'])->status());
        self::assertSame(0, $this->calls('mail.queue_action'));
    }

    public function testQueue(): void
    {
        $adm = $this->token($this->admin);
        self::assertSame([2, '7506CE0062'], (function () use ($adm) {
            $d = self::json($this->api('GET', '/api/v1/mail/admin/queue', $adm))['data'];
            return [$d['total'], $d['messages'][0]['id']];
        })());
        self::assertSame('Order', self::json($this->api('GET', '/api/v1/mail/admin/queue/7506CE0062', $adm))['data']['headers']['subject']);
        self::assertSame(404, $this->api('GET', '/api/v1/mail/admin/queue/x', $adm)->status());

        $res = $this->api('POST', '/api/v1/mail/admin/queue/actions', $adm, ['action' => 'hold', 'ids' => ['7506CE0062', 'bad id!']]);
        self::assertSame([200, 1], [$res->status(), self::json($res)['data']['count']]);
        self::assertSame(['action' => 'hold', 'ids' => ['7506CE0062']], $this->lastCall('mail.queue_action'));
        self::assertTrue($this->audited('mail.queue_hold'));
        self::assertSame(200, $this->api('POST', '/api/v1/mail/admin/queue/actions', $adm, ['action' => 'flush'])->status());

        $calls = $this->calls('mail.queue_action');
        self::assertSame(422, $this->api('POST', '/api/v1/mail/admin/queue/actions', $adm, ['action' => 'explode', 'ids' => ['7506CE0062']])->status());
        self::assertSame(409, $this->api('POST', '/api/v1/mail/admin/queue/actions', $adm, ['action' => 'delete', 'ids' => []])->status());
        self::assertSame(422, $this->api('POST', '/api/v1/mail/admin/queue/actions', $adm, ['action' => 'delete', 'ids' => 'all'])->status());
        self::assertSame($calls, $this->calls('mail.queue_action'));
    }

    public function testServerSettingsTraceRepairAndReport(): void
    {
        $adm = $this->token($this->admin);
        self::assertSame('52428800', self::json($this->api('GET', '/api/v1/mail/admin/server', $adm))['data']['values']['postfix']['message_size_limit']);

        $res = $this->api('PATCH', '/api/v1/mail/admin/server', $adm, ['postfix' => ['message_size_limit' => 100]]);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame(['postfix' => ['message_size_limit' => '100'], 'dovecot' => []], json_decode(json_encode($this->lastCall('mail.settings_set')), true));
        self::assertTrue($this->audited('mail.settings'));
        $calls = $this->calls('mail.settings_set');
        foreach ([['postfix' => ['smtpd_banner' => 'x']], ['postfix' => [['a']]], ['postfix' => ['relayhost' => ['x']]], ['dovecot' => 'x'], []] as $bad) {
            self::assertSame(422, $this->api('PATCH', '/api/v1/mail/admin/server', $adm, $bad)->status(), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('mail.settings_set'));

        self::assertSame(200, $this->api('GET', '/api/v1/mail/admin/log', $adm, [], ['lines' => '900'])->status());
        self::assertSame(500, $this->lastCall('mail.log_tail')['lines']);

        self::assertSame(422, $this->api('GET', '/api/v1/mail/admin/trace', $adm)->status());
        self::assertSame('mailbox', self::json($this->api('GET', '/api/v1/mail/admin/trace', $adm, [], ['address' => ' sales@shop.test ']))['data']['steps'][0]['result']);
        self::assertSame('sales@shop.test', $this->lastCall('mail.trace')['address']);

        $res = $this->api('POST', '/api/v1/mail/admin/repair', $adm, ['domain' => 'Shop.Test']);
        self::assertSame([200, 'shop.test'], [$res->status(), $this->lastCall('mail.repair_permissions')['domain']]);
        self::assertTrue($this->audited('mail.repair_permissions'));

        $d = self::json($this->api('GET', '/api/v1/mail/admin/report', $adm, [], ['range' => '7d', 'address' => 'a@b.test']))['data'];
        self::assertSame(['7d', 'journal', 7], [$d['range'], $d['source'], $d['report']['totals']['sent']]);
        self::assertSame(422, $this->api('GET', '/api/v1/mail/admin/report', $adm, [], ['range' => 'forever'])->status());
    }

    public function testGreylisting(): void
    {
        $adm = $this->token($this->admin);
        $base = '/api/v1/mail/admin/greylisting';
        self::assertSame(5, self::json($this->api('GET', $base, $adm))['data']['config']['deferral']);

        // Only what is sent changes; the rest of the configuration is sent back as it was.
        self::assertSame(200, $this->api('PATCH', $base, $adm, ['deferral' => 10, 'enabled' => false])->status());
        $sent = $this->lastCall('mail.greylist_set');
        self::assertSame([10, 240, 4320, false, 2], [$sent['deferral'], $sent['resend'], $sent['expire'], $sent['enabled'], count($sent['trusted'])]);
        self::assertTrue($this->audited('mail.greylist_settings'));
        $calls = $this->calls('mail.greylist_set');
        foreach ([['deferral' => 0], ['deferral' => '5'], ['expire' => 59], ['resend' => 10081], ['enabled' => 'yes'], []] as $bad) {
            self::assertSame(422, $this->api('PATCH', $base, $adm, $bad)->status(), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('mail.greylist_set'));

        $res = $this->api('POST', $base . '/trusted', $adm, ['range' => '203.0.113.0/24', 'comment' => 'partner']);
        self::assertSame(201, $res->status(), $res->body());
        self::assertSame(['203.0.113.0/24', 3], [$this->lastCall('mail.greylist_set')['trusted'][2]['range'], count($this->lastCall('mail.greylist_set')['trusted'])]);
        self::assertSame(422, $this->api('POST', $base . '/trusted', $adm, ['range' => ' '])->status());

        self::assertSame(200, $this->api('PUT', $base . '/trusted/1', $adm, ['range' => '198.51.100.8'])->status());
        self::assertSame('198.51.100.8', $this->lastCall('mail.greylist_set')['trusted'][1]['range']);
        self::assertSame(200, $this->api('DELETE', $base . '/trusted/0', $adm)->status());
        self::assertSame(['198.51.100.7'], array_column($this->lastCall('mail.greylist_set')['trusted'], 'range'));
        self::assertSame(404, $this->api('DELETE', $base . '/trusted/9', $adm)->status());
        self::assertSame(404, $this->api('PUT', $base . '/trusted/9', $adm, ['range' => '1.2.3.4'])->status());
        self::assertTrue($this->audited('mail.greylist_trusted_delete'));

        self::assertSame(200, $this->api('PUT', $base . '/providers', $adm, ['trust' => ['microsoft'], 'auto_update' => []])->status());
        $p = json_decode(json_encode($this->lastCall('mail.greylist_set')['providers']), true);
        self::assertSame([['trust' => false, 'auto_update' => false], ['trust' => true, 'auto_update' => false]], [$p['google'], $p['microsoft']]);
        self::assertSame(422, $this->api('PUT', $base . '/providers', $adm, ['trust' => ['yahoo']])->status());
        self::assertSame(200, $this->api('POST', $base . '/providers/update', $adm)->status());
        self::assertTrue($this->audited('mail.greylist_update_providers'));

        $d = self::json($this->api('GET', $base . '/report', $adm, [], ['status' => 'bogus', 'per' => '7', 'sort' => 'ip', 'dir' => 'asc']))['data'];
        self::assertSame(['', 50, 'ip', 1], [$d['query']['status'], $d['query']['per'], $d['query']['sort'], $d['total']]);
        self::assertSame(false, $this->lastCall('mail.greylist_report')['desc']);

        self::assertSame(2, self::json($this->api('POST', $base . '/cleanup', $adm, ['op' => 'purge']))['data']['removed']);
        $res = $this->api('POST', $base . '/cleanup', $adm, ['op' => 'delete', 'triplets' => [['ip' => '203.0.113.9', 'from' => 'a@b.test', 'to' => 'c@d.test']]]);
        self::assertSame([200, 1], [$res->status(), count($this->lastCall('mail.greylist_cleanup')['triplets'])]);
        $calls = $this->calls('mail.greylist_cleanup');
        foreach ([['op' => 'delete'], ['op' => 'delete', 'triplets' => [['ip' => '1.1.1.1']]], ['op' => 'drop']] as $bad) {
            self::assertSame(422, $this->api('POST', $base . '/cleanup', $adm, $bad)->status(), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('mail.greylist_cleanup'));
    }

    public function testSendingLimits(): void
    {
        $adm = $this->token($this->admin);
        $base = '/api/v1/mail/admin/sending-limits';
        self::assertSame(200, self::json($this->api('GET', $base, $adm))['data']['config']['per_hour']);

        // Only what is sent changes: the account limit of "shop" stays, "blog" gets one.
        $res = $this->api('PATCH', $base, $adm, ['per_hour' => 300, 'accounts' => ['blog' => 20]]);
        self::assertSame(200, $res->status(), $res->body());
        $sent = json_decode(json_encode($this->lastCall('mail.rate_set')), true);
        self::assertSame([false, 300, ['blog' => 20, 'shop' => 50]], [$sent['enabled'], $sent['per_hour'], $sent['accounts']]);
        self::assertTrue($this->audited('mail.rate_settings'));

        self::assertSame(200, $this->api('PATCH', $base, $adm, ['accounts' => ['shop' => null]])->status());
        self::assertSame([], json_decode(json_encode($this->lastCall('mail.rate_set')['accounts']), true));

        $calls = $this->calls('mail.rate_set');
        // A wrong type is 422; a number the server's rules refuse (out of range, an unknown website account) is 409.
        foreach ([[['per_hour' => '5'], 422], [['enabled' => 'on'], 422], [['accounts' => ['shop' => 'x']], 422], [['accounts' => [5]], 422], [['per_hour' => -1], 409], [['per_hour' => 2000000], 409], [['accounts' => ['nobody' => 5]], 409]] as [$bad, $want]) {
            self::assertSame($want, $this->api('PATCH', $base, $adm, $bad)->status(), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('mail.rate_set'));

        self::assertSame('shop', self::json($this->api('POST', $base . '/reset', $adm, ['account' => 'shop']))['data']['reset']);
        self::assertSame('shop', $this->lastCall('mail.rate_reset')['account']);
        self::assertSame('all', self::json($this->api('POST', $base . '/reset', $adm))['data']['reset']);
        self::assertSame(404, $this->api('POST', $base . '/reset', $adm, ['account' => 'ghost'])->status());
        self::assertTrue($this->audited('mail.rate_reset'));
    }
}
