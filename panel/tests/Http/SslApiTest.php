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

/** REST API v1: a website's HTTPS. */
final class SslApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    private int $site;
    private int $adminSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->site = $sites->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($this->cust));
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($this->admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('ssl.issue', ['cert_file' => '/etc/letsencrypt/live/shop.test/fullchain.pem', 'key_file' => '/etc/letsencrypt/live/shop.test/privkey.pem']);
        $this->agent->succeed('ssl.custom', ['cert_file' => '/var/lib/nimvor/certs/shop.test/fullchain.pem', 'key_file' => '/var/lib/nimvor/certs/shop.test/privkey.pem']);
        $this->agent->succeed('ssl.renew', ['renewed' => true]);
        $this->agent->succeed('ssl.delete', ['deleted' => true]);
        $this->agent->succeed('ssl.renewal_status', ['certbot_installed' => true, 'timer_active' => true, 'method' => 'systemd']);
        $this->agent->succeed('ssl.inspect', ['common_name' => 'shop.test', 'dns_names' => ['shop.test', 'www.shop.test'], 'issuer' => "Let's Encrypt R11", 'not_before' => '2026-11-01T00:00:00Z', 'not_after' => '2027-01-30T00:00:00Z', 'days_remaining' => 67, 'expired' => false, 'self_signed' => false, 'fingerprint_sha256' => 'AB:CD']);
    }

    /** @param array<string, mixed> $json */
    private function api(string $method, string $path, ?string $token, array $json = []): Response
    {
        $h = ['content-type' => 'application/json'] + ($token === null ? [] : ['authorization' => 'Bearer ' . $token]);
        return $this->app->handle(new Request($method, $path, [], [], ['REMOTE_ADDR' => '192.0.2.50'], $h, $json === [] ? '' : (string) json_encode($json)));
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

    public function testStateBeforeAndAfterIssuingACertificate(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/ssl';
        $d = self::json($this->api('GET', $base, $cust))['data'];
        self::assertSame([false, 'none', null, null], [$d['https'], $d['mode'], $d['certificate'], $d['state']]);

        $res = $this->api('POST', $base, $cust, ['email' => 'owner@shop.test']);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($this->api('GET', $base, $cust))['data'];
        self::assertSame([true, 'letsencrypt', true, 'valid', true], [$d['https'], $d['mode'], $d['force_https'], $d['state'], $d['auto_renewal']]);
        self::assertSame(['shop.test', ['shop.test', 'www.shop.test'], 67, false], [$d['certificate']['common_name'], $d['certificate']['names'], $d['certificate']['days_remaining'], $d['certificate']['self_signed']]);
        self::assertArrayNotHasKey('key_file', $d['certificate']);

        $res = $this->api('POST', $base . '/renew', $cust);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame('shop.test', $this->lastCall('ssl.renew')['cert_name']);
    }

    public function testOptionsChangeOnlyWhatIsSent(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/ssl';
        // Nothing to set before HTTPS is on.
        $res = $this->api('PATCH', $base . '/options', $cust, ['force_https' => true]);
        self::assertSame([409, 'rejected'], [$res->status(), self::json($res)['error']['code']]);

        $this->api('POST', $base, $cust, []);
        $d = self::json($this->api('PATCH', $base . '/options', $cust, ['hsts' => true]))['data'];
        self::assertSame([true, true], [$d['force_https'], $d['hsts']], 'the redirect stays on');
        $d = self::json($this->api('PATCH', $base . '/options', $cust, ['force_https' => false]))['data'];
        self::assertSame([false, false], [$d['force_https'], $d['hsts']], 'no HSTS without the redirect');
        $d = self::json($this->api('PATCH', $base . '/options', $cust, ['security_headers' => true]))['data'];
        self::assertTrue($d['security_headers']);
        self::assertSame(422, $this->api('PATCH', $base . '/options', $cust, ['hsts' => 'maybe'])->status());
    }

    public function testInstallOwnCertificateNeverEchoesTheKey(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/ssl';
        $calls = $this->calls('ssl.custom');
        $res = $this->api('PUT', $base . '/custom', $cust, ['certificate' => '', 'private_key' => 'KEY']);
        self::assertContains($res->status(), [409, 422]);
        self::assertSame($calls, $this->calls('ssl.custom'), 'an incomplete pair never reaches the server');

        $res = $this->api('PUT', $base . '/custom', $cust, ['certificate' => 'CERT-PEM', 'private_key' => 'SECRET-KEY-PEM', 'chain' => 'CHAIN-PEM']);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('ssl.custom');
        self::assertSame(['shop.test', 'CERT-PEM', 'SECRET-KEY-PEM', 'CHAIN-PEM'], [$sent['name'], $sent['certificate'], $sent['private_key'], $sent['chain']]);
        self::assertStringNotContainsString('SECRET-KEY-PEM', $res->body());
        $d = self::json($res)['data'];
        self::assertSame([true, 'custom', null], [$d['https'], $d['mode'], $d['auto_renewal']]);
        self::assertStringNotContainsString('SECRET-KEY-PEM', (string) $this->db->scalar('SELECT group_concat(details) FROM audit_logs'));

        // An uploaded certificate is not renewed by the server.
        $res = $this->api('POST', $base . '/renew', $cust);
        self::assertSame(409, $res->status());
    }

    public function testTheServerRefusingTheCertificateChangesNothing(): void
    {
        $cust = $this->token($this->cust);
        $this->agent->fail('ssl.custom', 422, 'invalid_params', 'The private key does not match the certificate.');
        $res = $this->api('PUT', '/api/v1/websites/' . $this->site . '/ssl/custom', $cust, ['certificate' => 'C', 'private_key' => 'K']);
        self::assertSame(502, $res->status());
        self::assertSame(0, (int) $this->db->scalar('SELECT https FROM websites WHERE id = ' . $this->site));
    }

    public function testDisable(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/ssl';
        $this->api('POST', $base, $cust, []);
        $d = self::json($this->api('DELETE', $base, $cust))['data'];
        self::assertSame([false, 'none', null], [$d['https'], $d['mode'], $d['certificate']]);
        self::assertSame(0, $this->calls('ssl.delete'), 'the certificate is kept unless asked');
        $this->api('POST', $base, $cust, []);
        $req = new Request('DELETE', $base, ['delete_certificate' => '1'], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $cust]);
        self::assertSame(200, $this->app->handle($req)->status());
        self::assertSame('shop.test', $this->lastCall('ssl.delete')['cert_name']);
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $base = '/api/v1/websites/' . $this->site . '/ssl';
        self::assertSame(200, $this->api('GET', $base, $ro)->status());
        foreach ([['PUT', '/custom', ['certificate' => 'C', 'private_key' => 'K']], ['POST', '/renew', []], ['PATCH', '/options', ['hsts' => true]], ['DELETE', '', []]] as [$m, $p, $b]) {
            self::assertSame(403, $this->api($m, $base . $p, $ro, $b)->status(), "$m $p");
        }
        self::assertSame(401, $this->api('GET', $base, null)->status());
        // Another customer's website is not visible.
        $other = '/api/v1/websites/' . $this->adminSite . '/ssl';
        self::assertSame(404, $this->api('GET', $other, $cust)->status());
        self::assertSame(404, $this->api('PUT', $other . '/custom', $cust, ['certificate' => 'C', 'private_key' => 'K'])->status());
        self::assertSame(200, $this->api('GET', $other, $this->token($this->admin))->status());
    }
}
