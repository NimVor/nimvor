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

/** REST API v1: email domains, mailboxes and aliases. */
final class MailApiTest extends HttpTestCase
{
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

    public function testCustomerHostsMailForOwnDomainAndGetsTheDnsRecords(): void
    {
        $cust = $this->token($this->cust);
        $res = $this->api('POST', '/api/v1/mail/domains', $cust, ['website_id' => $this->custSite]);
        self::assertSame(201, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame(['shop.test', $this->custSite, $this->cust, 'nimvor202609'], [$d['domain'], $d['website_id'], $d['owner_id'], $d['dkim_selector']]);
        $records = array_map(static fn (array $x): string => $x['name'] . ' ' . $x['type'], $d['dns_records']);
        foreach (['@ MX', '@ TXT', 'nimvor202609._domainkey TXT', '_dmarc TXT'] as $want) {
            self::assertContains($want, $records, $want);
        }
        self::assertStringNotContainsString('PRIVATE KEY', $res->body());
        self::assertSame(['shop.test'], array_column(self::json($this->api('GET', '/api/v1/mail/domains', $cust))['data'], 'domain'));
        self::assertSame($d['id'], self::json($this->api('GET', '/api/v1/mail/domains/' . $d['id'], $cust))['data']['id']);

        // The same domain twice; another account's website; a domain that is not their website's.
        self::assertSame(422, $this->api('POST', '/api/v1/mail/domains', $cust, ['website_id' => $this->custSite])->status());
        self::assertArrayHasKey('website_id', self::json($this->api('POST', '/api/v1/mail/domains', $cust, ['website_id' => $this->adminSite]))['error']['fields']);
        self::assertArrayHasKey('website_id', self::json($this->api('POST', '/api/v1/mail/domains', $cust, ['domain' => 'elsewhere.test']))['error']['fields']);
        self::assertSame(1, $this->calls('mail.domain_add'), 'the mail server was only asked once');
    }

    public function testMailboxesAreValidatedLimitedAndPasswordsReturnedOnce(): void
    {
        $cust = $this->token($this->cust);
        $did = $this->domain($cust);
        $res = $this->api('POST', "/api/v1/mail/domains/$did/mailboxes", $cust, ['local' => 'info', 'generate_password' => true, 'quota_mb' => 500]);
        self::assertSame(201, $res->status(), $res->body());
        $data = self::json($res)['data'];
        self::assertSame(['info', 'info@shop.test', 500], [$data['mailbox']['local'], $data['mailbox']['address'], $data['mailbox']['quota_mb']]);
        self::assertGreaterThanOrEqual(15, strlen((string) $data['password']));
        $mid = $data['mailbox']['id'];
        self::assertStringNotContainsString('password', (string) json_encode(self::json($this->api('GET', "/api/v1/mail/domains/$did/mailboxes", $cust))['data']), 'listing never shows passwords');

        foreach ([
            [['local' => 'Bad Name', 'password' => 'Correct-Horse-9'], 'local'],
            [['local' => 'x', 'password' => 'short'], 'password'],
            [['local' => 'x', 'password' => 'has:colon-1234567'], 'password'],
            [['local' => 'x'], 'password'],
            [['local' => 'info', 'password' => 'Correct-Horse-Battery-9'], 'local'], // exists
        ] as [$body, $field]) {
            $res = $this->api('POST', "/api/v1/mail/domains/$did/mailboxes", $cust, $body);
            self::assertSame(422, $res->status(), json_encode($body));
            self::assertArrayHasKey($field, self::json($res)['error']['fields'], json_encode($body));
        }
        self::assertSame(1, $this->calls('mail.mailbox_save'), 'only the valid mailbox reached the mail server');

        // PATCH changes what is sent: quota alone keeps the mailbox, a password is passed on.
        $res = $this->api('PATCH', "/api/v1/mail/domains/$did/mailboxes/$mid", $cust, ['quota_mb' => 1000]);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame([1000, null], [self::json($res)['data']['mailbox']['quota_mb'], self::json($res)['data']['password']]);
        $res = $this->api('PATCH', "/api/v1/mail/domains/$did/mailboxes/$mid", $cust, ['password' => 'Another-Correct-Pw-1']);
        self::assertSame(1000, self::json($res)['data']['mailbox']['quota_mb'], 'the quota was kept');
        self::assertSame('Another-Correct-Pw-1', $this->lastCall('mail.mailbox_save')['password']);
        self::assertSame(404, $this->api('PATCH', "/api/v1/mail/domains/$did/mailboxes/99999", $cust, ['quota_mb' => 1])->status());

        // Deleting needs the address.
        $res = $this->api('DELETE', "/api/v1/mail/domains/$did/mailboxes/$mid", $cust, [], ['confirm' => 'wrong@shop.test']);
        self::assertSame(422, $res->status());
        self::assertSame(0, $this->calls('mail.mailbox_delete'));
        $res = $this->api('DELETE', "/api/v1/mail/domains/$did/mailboxes/$mid", $cust, [], ['confirm' => 'info@shop.test']);
        self::assertSame([200, 'info@shop.test'], [$res->status(), self::json($res)['data']['address']]);
        self::assertSame(1, $this->calls('mail.mailbox_delete'));
    }

    public function testAliasesIncludingTheCatchAll(): void
    {
        $cust = $this->token($this->cust);
        $did = $this->domain($cust);
        $res = $this->api('POST', "/api/v1/mail/domains/$did/aliases", $cust, ['local' => 'sales', 'destinations' => ['info@shop.test', 'Owner@Gmail.test']]);
        self::assertSame(201, $res->status(), $res->body());
        $a = self::json($res)['data'];
        self::assertSame(['sales@shop.test', false, ['info@shop.test', 'owner@gmail.test']], [$a['source'], $a['catch_all'], $a['destinations']]);
        $res = $this->api('POST', "/api/v1/mail/domains/$did/aliases", $cust, ['local' => '*', 'destinations' => ['info@shop.test']]);
        self::assertSame(201, $res->status(), $res->body());
        self::assertTrue(self::json($res)['data']['catch_all']);
        self::assertCount(2, self::json($this->api('GET', "/api/v1/mail/domains/$did/aliases", $cust))['data']);
        // Replacing an alias changes its destinations.
        $res = $this->api('POST', "/api/v1/mail/domains/$did/aliases", $cust, ['local' => 'sales', 'destinations' => ['only@shop.test']]);
        self::assertSame(['only@shop.test'], self::json($res)['data']['destinations']);
        self::assertCount(2, self::json($this->api('GET', "/api/v1/mail/domains/$did/aliases", $cust))['data']);

        foreach ([[['local' => 'x', 'destinations' => []], 'destinations'], [['local' => 'x', 'destinations' => ['not-an-address']], 'destinations'], [['local' => 'Bad Name', 'destinations' => ['a@b.test']], 'alias_local']] as [$body, $field]) {
            $res = $this->api('POST', "/api/v1/mail/domains/$did/aliases", $cust, $body);
            self::assertSame(422, $res->status(), json_encode($body));
            self::assertArrayHasKey($field, self::json($res)['error']['fields']);
        }
        // An address that is a mailbox cannot also be an alias.
        $this->api('POST', "/api/v1/mail/domains/$did/mailboxes", $cust, ['local' => 'info', 'generate_password' => true]);
        self::assertSame(422, $this->api('POST', "/api/v1/mail/domains/$did/aliases", $cust, ['local' => 'info', 'destinations' => ['a@b.test']])->status());

        $res = $this->api('DELETE', "/api/v1/mail/domains/$did/aliases/{$a['id']}", $cust);
        self::assertSame([200, 'sales@shop.test'], [$res->status(), self::json($res)['data']['source']]);
        self::assertSame(404, $this->api('DELETE', "/api/v1/mail/domains/$did/aliases/{$a['id']}", $cust)->status());
    }

    public function testScopeAndReadOnlyTokens(): void
    {
        $admin = $this->token($this->admin);
        $cust = $this->token($this->cust);
        $did = $this->domain($cust);
        // The administrator sees it; another customer's token does not.
        self::assertSame(200, $this->api('GET', "/api/v1/mail/domains/$did", $admin)->status());
        $other = $this->seedUser('other', 'customer');
        $otherToken = $this->token($other);
        foreach ([['GET', "/api/v1/mail/domains/$did"], ['GET', "/api/v1/mail/domains/$did/mailboxes"], ['POST', "/api/v1/mail/domains/$did/mailboxes"], ['GET', "/api/v1/mail/domains/$did/aliases"], ['DELETE', "/api/v1/mail/domains/$did"], ['POST', "/api/v1/mail/domains/$did/dns"], ['GET', "/api/v1/mail/domains/$did/dns-check"]] as [$m, $p]) {
            self::assertSame(404, $this->api($m, $p, $otherToken, ['local' => 'x', 'password' => 'Correct-Horse-9'])->status(), $m . ' ' . $p);
        }
        self::assertSame([], self::json($this->api('GET', '/api/v1/mail/domains', $otherToken))['data']);
        $read = $this->token($this->admin, 'read');
        self::assertSame(200, $this->api('GET', '/api/v1/mail/domains', $read)->status());
        self::assertSame(403, $this->api('POST', "/api/v1/mail/domains/$did/mailboxes", $read, ['local' => 'x'])->status());
        self::assertSame(401, $this->api('GET', '/api/v1/mail/domains', null)->status());
    }

    public function testPublishAndCheckDnsAndDeleteTheDomain(): void
    {
        $cust = $this->token($this->cust);
        $did = $this->domain($cust);
        // No DNS zone for the domain on this panel yet.
        $res = $this->api('POST', "/api/v1/mail/domains/$did/dns", $cust);
        self::assertSame([409, 'rejected'], [$res->status(), self::json($res)['error']['code']]);

        $this->app->container->get(\NimVor\Services\SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test, ns2.host.test.', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->agent->succeed('dns.status', ['installed' => true, 'configured' => true, 'running' => true, 'zones' => []]);
        self::assertSame(201, $this->api('POST', '/api/v1/dns/zones', $cust, ['website_id' => $this->custSite])->status());
        $res = $this->api('POST', "/api/v1/mail/domains/$did/dns", $cust);
        self::assertSame(200, $res->status(), $res->body());
        self::assertGreaterThanOrEqual(3, self::json($res)['data']['added']);
        self::assertSame(0, self::json($this->api('POST', "/api/v1/mail/domains/$did/dns", $cust))['data']['added'], 'nothing is added twice');

        $res = $this->api('GET', "/api/v1/mail/domains/$did/dns-check", $cust);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame('MX', self::json($res)['data']['checks'][0]['name']);

        $res = $this->api('DELETE', "/api/v1/mail/domains/$did", $cust, [], ['confirm' => 'nope.test']);
        self::assertSame(422, $res->status());
        self::assertSame(0, $this->calls('mail.domain_remove'));
        $res = $this->api('DELETE', "/api/v1/mail/domains/$did", $cust, [], ['confirm' => 'shop.test']);
        self::assertSame(200, $res->status(), $res->body());
        self::assertNull($this->db->scalar('SELECT id FROM mail_domains WHERE id = ?', [$did]));
        self::assertSame(1, $this->calls('mail.domain_remove'));
    }
}
