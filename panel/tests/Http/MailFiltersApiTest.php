<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\UserRepository;
use NimVor\Services\ApiTokenService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: mailbox filters, auto-responder and mail routing. */
final class MailFiltersApiTest extends HttpTestCase
{
    private const STORED = [
        ['name' => 'Newsletters', 'enabled' => true, 'any' => true, 'rules' => [['part' => 'list-id', 'match' => 'contains', 'value' => 'news']], 'actions' => [['type' => 'fileinto', 'value' => 'News']]],
        ['name' => 'Boss', 'enabled' => true, 'any' => false, 'rules' => [['part' => 'from', 'match' => 'is', 'value' => 'boss@x.test']], 'actions' => [['type' => 'stop', 'value' => '']]],
    ];

    private Connection $db;
    private int $admin;
    private int $cust;
    private int $custSite;
    private int $adminSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->custSite = $sites->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($this->cust));
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($this->admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('mail.status', ['configured' => true, 'hostname' => 'mail.host.test', 'domains' => [], 'mailboxes' => [], 'aliases' => []]);
        $this->agent->succeed('mail.domain_add', ['domain' => 'shop.test', 'dkim_selector' => 'nimvor202609', 'dkim_txt' => 'v=DKIM1; k=rsa; p=MIIBIjAN']);
        $this->agent->succeed('mail.mailbox_save', ['created' => true]);
        $this->agent->succeed('mail.mailbox_delete', ['deleted' => true]);
        $this->agent->succeed('mail.alias_save', ['ok' => true]);
        $this->agent->succeed('mail.alias_delete', ['deleted' => true]);
        $this->agent->succeed('mail.domain_remove', ['removed' => true]);
        $this->agent->succeed('mail.filters_save', ['ok' => true]);
        $this->agent->succeed('mail.routing_set', ['ok' => true]);
        $this->agent->succeed('mail.filters_get', ['filters' => self::STORED, 'responder' => ['enabled' => false, 'subject' => '', 'body' => '', 'days' => 1, 'start' => '', 'end' => '']]);
        $this->agent->succeed('mail.auth_check', ['checks' => [['name' => 'MX', 'status' => 'ok', 'message' => 'mail.host.test']]]);
    }

    /** @param array<string, mixed> $json @param array<string, string> $query */
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
        return $this->app->container->get(ApiTokenService::class)->create($userId, ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => $ability, 'expires_days' => 30]);
    }

    private function domain(string $token): int
    {
        $res = $this->api('POST', '/api/v1/mail/domains', $token, ['website_id' => $this->custSite]);
        self::assertSame(201, $res->status(), $res->body());
        return self::json($res)['data']['id'];
    }

    /** @return array{0: int, 1: int} domain id and mailbox id of a customer's mailbox */
    private function mailbox(string $token): array
    {
        $d = $this->domain($token);
        $res = $this->api('POST', "/api/v1/mail/domains/$d/mailboxes", $token, ['local' => 'info', 'generate_password' => true, 'quota_mb' => 100]);
        self::assertSame(201, $res->status(), $res->body());
        return [$d, self::json($res)['data']['mailbox']['id']];
    }

    public function testReadAddReplaceToggleAndDeleteFilters(): void
    {
        $cust = $this->token($this->cust);
        [$d, $m] = $this->mailbox($cust);
        $base = "/api/v1/mail/domains/$d/mailboxes/$m/filters";

        $data = self::json($this->api('GET', $base, $cust))['data'];
        self::assertSame(['Newsletters', 'Boss'], array_column($data['filters'], 'name'));
        self::assertSame([0, 'any', 1, 'all'], [$data['filters'][0]['index'], $data['filters'][0]['match'], $data['filters'][1]['index'], $data['filters'][1]['match']]);
        self::assertFalse($data['responder']['enabled']);
        self::assertSame('info@shop.test', $this->lastCall('mail.filters_get')['address']);

        $new = ['name' => ' Spam ', 'match' => 'any', 'rules' => [['part' => 'subject', 'match' => 'contains', 'value' => 'viagra'], ['part' => 'from', 'match' => 'ends', 'value' => '@spam.test']], 'actions' => [['type' => 'discard']]];
        $res = $this->api('POST', $base, $cust, $new);
        self::assertSame(201, $res->status(), $res->body());
        self::assertSame([2, 'Spam'], [self::json($res)['data']['index'], self::json($res)['data']['name']]);
        $sent = $this->lastCall('mail.filters_save');
        self::assertSame(['Newsletters', 'Boss', 'Spam'], array_column($sent['filters'], 'name'));
        self::assertTrue($sent['filters'][2]['any']);
        self::assertSame('info@shop.test', $sent['address']);
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'mail.filters'"));

        $res = $this->api('PUT', "$base/1", $cust, ['name' => 'Boss VIP', 'rules' => [['part' => 'from', 'value' => 'boss@x.test']], 'actions' => [['type' => 'redirect', 'value' => 'me@elsewhere.test']]]);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('mail.filters_save');
        self::assertSame(['Newsletters', 'Boss VIP'], array_column($sent['filters'], 'name'));
        self::assertSame([['type' => 'redirect', 'value' => 'me@elsewhere.test']], $sent['filters'][1]['actions']);

        $res = $this->api('PATCH', "$base/0", $cust, ['enabled' => false]);
        self::assertSame([200, false], [$res->status(), self::json($res)['data']['enabled']]);
        $sent = $this->lastCall('mail.filters_save');
        self::assertSame([false, true], [$sent['filters'][0]['enabled'], $sent['filters'][1]['enabled']], 'only the named filter changed');
        self::assertSame(self::STORED[0]['rules'], $sent['filters'][0]['rules']);

        $res = $this->api('DELETE', "$base/0", $cust);
        self::assertSame([200, 0], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame(['Boss'], array_column($this->lastCall('mail.filters_save')['filters'], 'name'));
        self::assertSame(404, $this->api('DELETE', "$base/7", $cust)->status());
        self::assertSame(404, $this->api('PUT', "$base/7", $cust, $new)->status());
        self::assertSame(404, $this->api('PATCH', "$base/7", $cust, ['enabled' => true])->status());
    }

    public function testFiltersAreCheckedBeforeTheMailServer(): void
    {
        $cust = $this->token($this->cust);
        [$d, $m] = $this->mailbox($cust);
        $base = "/api/v1/mail/domains/$d/mailboxes/$m/filters";
        $ok = ['name' => 'F', 'rules' => [['part' => 'from', 'value' => 'a@b.test']], 'actions' => [['type' => 'discard']]];
        $calls = $this->calls('mail.filters_save');
        foreach ([
            [['name' => ''] + $ok, 'name'],
            [['rules' => []] + $ok, 'rules'],
            [['rules' => [['part' => 'nonsense', 'value' => 'x']]] + $ok, 'rules'],
            [['rules' => [['part' => 'from', 'match' => 'sounds-like', 'value' => 'x']]] + $ok, 'rules'],
            [['rules' => [['part' => 'from', 'value' => '']]] + $ok, 'rules'],
            [['actions' => []] + $ok, 'actions'],
            [['actions' => [['type' => 'run-shell', 'value' => 'rm -rf /']]] + $ok, 'actions'],
            [['actions' => [['type' => 'redirect', 'value' => 'not-an-address']]] + $ok, 'actions'],
            [['actions' => [['type' => 'fileinto']]] + $ok, 'actions'],
            [['match' => 'most'] + $ok, 'match'],
        ] as [$body, $field]) {
            $res = $this->api('POST', $base, $cust, $body);
            self::assertSame(422, $res->status(), json_encode($body) . $res->body());
            self::assertArrayHasKey($field, self::json($res)['error']['fields'], json_encode($body));
        }
        self::assertSame(422, $this->api('PATCH', "$base/0", $cust, ['enabled' => 'maybe'])->status());
        self::assertSame(422, $this->api('PATCH', "$base/0", $cust, ['name' => 'x'])->status());
        self::assertSame($calls, $this->calls('mail.filters_save'));
    }

    public function testTheResponderKeepsTheFilters(): void
    {
        $cust = $this->token($this->cust);
        [$d, $m] = $this->mailbox($cust);
        $url = "/api/v1/mail/domains/$d/mailboxes/$m/responder";
        $res = $this->api('PUT', $url, $cust, ['enabled' => true, 'subject' => 'Away', 'body' => "Back soon.\nBye", 'days' => 3, 'start' => '2026-11-27', 'end' => '2026-12-05']);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame([true, 'Away', 3, '2026-12-05'], [self::json($res)['data']['enabled'], self::json($res)['data']['subject'], self::json($res)['data']['days'], self::json($res)['data']['end']]);
        $sent = $this->lastCall('mail.filters_save');
        self::assertSame(['Newsletters', 'Boss'], array_column($sent['filters'], 'name'), 'the filters stay');
        self::assertSame(['Away', true], [$sent['responder']['subject'], $sent['responder']['enabled']]);
        // Off needs no text.
        self::assertSame(200, $this->api('PUT', $url, $cust, ['enabled' => false])->status());

        $calls = $this->calls('mail.filters_save');
        foreach ([['enabled' => true], ['enabled' => true, 'subject' => 'x'], ['enabled' => true, 'subject' => "a\nb", 'body' => 'x'], ['subject' => 'x', 'body' => 'y', 'days' => 0], ['subject' => 'x', 'body' => 'y', 'days' => 31], ['subject' => 'x', 'body' => 'y', 'start' => '27/11/2026'], ['subject' => 'x', 'body' => 'y', 'start' => '2026-12-05', 'end' => '2026-11-27'], ['subject' => 'x', 'body' => 'y', 'end' => '2026-02-31']] as $bad) {
            self::assertSame(422, $this->api('PUT', $url, $cust, $bad)->status(), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('mail.filters_save'));
    }

    public function testRouting(): void
    {
        $cust = $this->token($this->cust);
        $d = $this->domain($cust);
        $res = $this->api('PUT', "/api/v1/mail/domains/$d/routing", $cust, ['routing' => 'backup']);
        self::assertSame([200, 'backup'], [$res->status(), self::json($res)['data']['routing']], $res->body());
        self::assertSame(['shop.test', 'backup'], [$this->lastCall('mail.routing_set')['domain'], $this->lastCall('mail.routing_set')['routing']]);
        self::assertSame('backup', self::json($this->api('GET', "/api/v1/mail/domains/$d", $cust))['data']['routing']);
        $calls = $this->calls('mail.routing_set');
        foreach ([[], ['routing' => 'sideways'], ['routing' => '']] as $bad) {
            self::assertSame(422, $this->api('PUT', "/api/v1/mail/domains/$d/routing", $cust, $bad)->status());
        }
        self::assertSame($calls, $this->calls('mail.routing_set'));
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $admin = $this->token($this->admin);
        [$d, $m] = $this->mailbox($cust);
        // The administrator's own domain and mailbox.
        $od = self::json($this->api('POST', '/api/v1/mail/domains', $admin, ['website_id' => $this->adminSite]))['data']['id'];
        $om = self::json($this->api('POST', "/api/v1/mail/domains/$od/mailboxes", $admin, ['local' => 'boss', 'generate_password' => true]))['data']['mailbox']['id'];

        $writes = $this->calls('mail.filters_save') + $this->calls('mail.routing_set');
        $ok = ['name' => 'F', 'rules' => [['part' => 'from', 'value' => 'a@b.test']], 'actions' => [['type' => 'discard']]];
        foreach ([['GET', "/mail/domains/$od/mailboxes/$om/filters", []], ['POST', "/mail/domains/$od/mailboxes/$om/filters", $ok], ['DELETE', "/mail/domains/$od/mailboxes/$om/filters/0", []], ['PUT', "/mail/domains/$od/mailboxes/$om/responder", ['enabled' => false]], ['PUT', "/mail/domains/$od/routing", ['routing' => 'remote']], ['GET', "/mail/domains/$d/mailboxes/$om/filters", []], ['PUT', "/mail/domains/$d/mailboxes/$om/responder", ['enabled' => false]]] as [$method, $path, $body]) {
            self::assertSame(404, $this->api($method, '/api/v1' . $path, $cust, $body)->status(), "$method $path");
        }
        self::assertSame($writes, $this->calls('mail.filters_save') + $this->calls('mail.routing_set'), 'another account\'s mailbox is never touched');

        self::assertSame(200, $this->api('GET', "/api/v1/mail/domains/$d/mailboxes/$m/filters", $ro)->status());
        foreach ([['POST', "/filters", $ok], ['PUT', "/filters/0", $ok], ['PATCH', "/filters/0", ['enabled' => false]], ['DELETE', "/filters/0", []], ['PUT', "/responder", ['enabled' => false]]] as [$method, $p, $body]) {
            self::assertSame(403, $this->api($method, "/api/v1/mail/domains/$d/mailboxes/$m$p", $ro, $body)->status(), "$method $p");
        }
        self::assertSame(403, $this->api('PUT', "/api/v1/mail/domains/$d/routing", $ro, ['routing' => 'local'])->status());
        self::assertSame(401, $this->api('GET', "/api/v1/mail/domains/$d/mailboxes/$m/filters", null)->status());
        self::assertSame(200, $this->api('GET', "/api/v1/mail/domains/$od/mailboxes/$om/filters", $admin)->status());
    }
}
