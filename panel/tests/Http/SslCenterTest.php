<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

final class SslCenterTest extends HttpTestCase
{
    private int $siteId;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($admin));
        $cert = ['file' => '/etc/letsencrypt/live/shop.test/fullchain.pem', 'common_name' => 'shop.test', 'dns_names' => ['shop.test'], 'issuer' => 'CN=R11,O=Let\'s Encrypt,C=US', 'not_after' => '2026-12-01T00:00:00Z', 'days_remaining' => 60, 'expired' => false, 'self_signed' => false];
        $this->agent->succeed('ssl.store', [
            'certificates' => [
                ['source' => 'letsencrypt', 'name' => 'shop.test', 'certificate' => $cert, 'used_by' => ['whacp-shop.test.conf'], 'deletable' => false],
                ['source' => 'whacp', 'name' => 'old.test', 'certificate' => ['common_name' => 'old.test', 'dns_names' => [], 'days_remaining' => -3, 'expired' => true], 'used_by' => [], 'deletable' => true],
            ],
            'hosts' => [
                ['name' => 'shop.test', 'file' => 'whacp-shop.test.conf', 'enabled' => true, 'https' => true, 'redirects_to_https' => true, 'whacp' => true, 'covered' => true, 'certificate' => $cert],
                ['name' => 'www.shop.test', 'file' => 'whacp-shop.test.conf', 'enabled' => true, 'https' => true, 'redirects_to_https' => true, 'whacp' => true, 'covered' => false, 'certificate' => $cert],
                ['name' => 'legacy.test', 'file' => 'legacy.conf', 'enabled' => true, 'https' => false, 'whacp' => false, 'covered' => false],
            ],
        ]);
        $this->agent->succeed('ssl.renewal_status', ['certbot_installed' => true, 'timer_active' => true, 'method' => 'certbot.timer']);
    }

    public function testAdministratorPages(): void
    {
        $this->login('admin');
        self::assertSame('/ssl/hosts', $this->request('GET', '/ssl')->header('Location'));
        $hosts = $this->request('GET', '/ssl/hosts')->body();
        foreach (['www.shop.test', 'Does not cover this name', 'legacy.test', '1 secure', '1 need attention', 'certbot.timer', '/sites/' . $this->siteId . '/ssl'] as $want) {
            self::assertStringContainsString($want, $hosts, $want);
        }
        $certs = $this->request('GET', '/ssl/certificates')->body();
        self::assertStringContainsString('Expired', $certs);
        self::assertStringContainsString('in use', $certs);

        $this->agent->succeed('ssl.custom_delete', ['deleted' => 'old.test']);
        $this->post('/ssl/certificates/delete', ['source' => 'whacp', 'name' => 'old.test']);
        self::assertSame('old.test', $this->lastCall('ssl.custom_delete')['name']);
        $this->post('/ssl/certificates/delete', ['source' => 'other', 'name' => '/etc/ssl/x.pem']);
        self::assertNotEmpty($this->flash('error'));
    }

    public function testKeysCsrAndInstall(): void
    {
        $this->login('admin');
        $this->agent->succeed('ssl.csr_create', ['id' => 'shop.test-20260901120000', 'names' => ['shop.test', 'www.shop.test'], 'csr' => "-----BEGIN CERTIFICATE REQUEST-----\nX\n-----END CERTIFICATE REQUEST-----\n"]);
        $res = $this->post('/ssl/keys', ['names' => 'Shop.test, www.shop.test', 'organization' => 'Shop', 'country' => 'de', 'key_type' => 'ecdsa256', 'self_signed' => '1', 'self_signed_days' => '9999']);
        $call = $this->lastCall('ssl.csr_create');
        self::assertSame([['shop.test', 'www.shop.test'], 'DE', 825], [$call['names'], $call['country'], $call['self_signed_days']]);
        self::assertStringContainsString('/ssl/keys?id=shop.test-20260901120000', (string) $res->header('Location'));

        $key = ['id' => 'shop.test-20260901120000', 'names' => ['shop.test'], 'subject' => 'CN=shop.test', 'key_type' => 'ecdsa256', 'created' => '2026-09-01T12:00:00Z', 'public_key_sha256' => 'AB', 'has_csr' => true, 'self_signed' => true, 'csr' => "-----BEGIN CERTIFICATE REQUEST-----\nX\n-----END CERTIFICATE REQUEST-----\n"];
        $this->agent->succeed('ssl.keys', ['keys' => [$key], 'key_types' => ['ecdsa256', 'rsa2048']]);
        $this->agent->succeed('ssl.key', $key);
        $page = $this->request('GET', '/ssl/keys', query: ['id' => $key['id']])->body();
        self::assertStringContainsString('BEGIN CERTIFICATE REQUEST', $page);
        self::assertStringNotContainsString('PRIVATE KEY', $page);
        $dl = $this->request('GET', '/ssl/keys/csr', query: ['id' => $key['id']]);
        self::assertStringContainsString('attachment', (string) $dl->header('Content-Disposition'));

        $this->agent->succeed('ssl.custom', ['cert_file' => '/var/lib/whacp-agent/certs/shop.test/fullchain.pem', 'key_file' => '/var/lib/whacp-agent/certs/shop.test/privkey.pem', 'certificate' => []]);
        $this->agent->succeed('sites.apply', ['domain' => 'shop.test', 'changed' => [], 'user' => ['name' => 'shop', 'uid' => 1500, 'gid' => 1500]]);
        $this->post('/ssl/keys/install', ['key_id' => $key['id'], 'website_id' => (string) $this->siteId, 'self_signed' => '1']);
        $call = $this->lastCall('ssl.custom');
        self::assertSame([$key['id'], true, 'shop.test', ''], [$call['key_id'], $call['use_self_signed'], $call['domain'], $call['certificate']]);
        $db = $this->app->container->get(Connection::class);
        self::assertSame(1, (int) $db->scalar('SELECT https FROM websites WHERE id = ?', [$this->siteId]));
        self::assertSame('success', $db->scalar("SELECT result FROM audit_logs WHERE action = 'ssl.install_key'"));

        $this->agent->succeed('ssl.key_delete', ['deleted' => $key['id']]);
        $this->post('/ssl/keys/delete', ['id' => $key['id']]);
        self::assertSame($key['id'], $this->lastCall('ssl.key_delete')['id']);
    }

    public function testTlsProfile(): void
    {
        $this->login('admin');
        $this->agent->succeed('ssl.tls_get', ['profile' => ['protocols' => 'tls12', 'ciphers' => 'default', 'session_tickets' => true], 'snippet' => "ssl_protocols TLSv1.2 TLSv1.3;\n", 'path' => '/etc/nginx/whacp-tls.conf', 'vhosts' => 3, 'legacy_vhosts' => 2]);
        $page = $this->request('GET', '/ssl/tls')->body();
        self::assertStringContainsString('2 file(s)', $page);
        $this->agent->succeed('ssl.tls_set', ['migrated_vhosts' => 2, 'changed' => true]);
        $this->post('/ssl/tls', ['protocols' => 'tls13', 'ciphers' => 'intermediate']);
        self::assertSame(['protocols' => 'tls13', 'ciphers' => 'intermediate', 'session_tickets' => false], $this->lastCall('ssl.tls_set'));
        self::assertStringContainsString('2 website configuration(s)', (string) $this->flash('success'));
    }

    public function testLetsEncryptAccountCardAndRegistration(): void
    {
        $this->login('admin');
        $this->agent->succeed('ssl.services', ['hostname' => 'server.test', 'certbot_installed' => true, 'lets_encrypt' => [], 'mail_tls' => []]);
        $this->agent->succeed('ssl.account', [
            'providers' => [['id' => 'letsencrypt', 'label' => "Let's Encrypt", 'requires_eab' => false, 'terms_url' => 'https://letsencrypt.org/repository/'], ['id' => 'zerossl', 'label' => 'ZeroSSL', 'requires_eab' => true, 'terms_url' => 'https://zerossl.com/terms/']],
            'provider' => 'letsencrypt', 'label' => "Let's Encrypt", 'requires_eab' => false,
            'certbot_installed' => true, 'registered' => false, 'email' => '', 'account_uri' => '', 'terms_url' => 'https://letsencrypt.org/repository/',
        ]);

        $page = $this->request('GET', '/ssl/services')->body();
        self::assertStringContainsString('Certificate authority', $page);
        self::assertStringContainsString('has no saved', $page);
        self::assertStringContainsString('action="/ssl/services/account"', $page);
        self::assertStringContainsString('<option value="zerossl"', $page, 'provider selector lists ZeroSSL');
        self::assertStringContainsString('Register account', $page);

        // Registering passes provider, email and the accepted ToS to the agent,
        // and makes the provider active.
        $this->agent->succeed('ssl.account_register', ['provider' => 'letsencrypt', 'registered' => true, 'updated' => false, 'email' => 'ops@real.test', 'account_uri' => 'https://acme-v02.api.letsencrypt.org/acme/acct/9']);
        $this->post('/ssl/services/account', ['provider' => 'letsencrypt', 'email' => 'ops@real.test', 'agree_tos' => '1']);
        self::assertSame(['provider' => 'letsencrypt', 'email' => 'ops@real.test', 'agree_tos' => true, 'staging' => false, 'eab_kid' => '', 'eab_hmac_key' => ''], $this->lastCall('ssl.account_register'));
        self::assertStringContainsString('active certificate authority', (string) $this->flash('success'));
    }

    public function testZeroSSLProviderShowsEabFieldsAndRegisters(): void
    {
        $this->login('admin');
        $this->agent->succeed('ssl.services', ['hostname' => 'server.test', 'certbot_installed' => true, 'lets_encrypt' => [], 'mail_tls' => []]);
        $this->agent->succeed('ssl.account', [
            'providers' => [['id' => 'letsencrypt', 'label' => "Let's Encrypt", 'requires_eab' => false, 'terms_url' => 'x'], ['id' => 'zerossl', 'label' => 'ZeroSSL', 'requires_eab' => true, 'terms_url' => 'https://zerossl.com/terms/']],
            'provider' => 'zerossl', 'label' => 'ZeroSSL', 'requires_eab' => true,
            'certbot_installed' => true, 'registered' => false, 'email' => '', 'account_uri' => '', 'terms_url' => 'https://zerossl.com/terms/',
        ]);

        $page = $this->request('GET', '/ssl/services', [], [], '', ['provider' => 'zerossl'])->body();
        self::assertStringContainsString('EAB key ID', $page);
        self::assertStringContainsString('EAB HMAC key', $page);

        $this->agent->succeed('ssl.account_register', ['provider' => 'zerossl', 'registered' => true, 'updated' => false, 'email' => 'ops@real.test', 'account_uri' => 'https://acme.zerossl.com/acct/5']);
        $this->post('/ssl/services/account', ['provider' => 'zerossl', 'email' => 'ops@real.test', 'agree_tos' => '1', 'eab_kid' => 'KID1', 'eab_hmac_key' => 'SECRET']);
        self::assertSame(['provider' => 'zerossl', 'email' => 'ops@real.test', 'agree_tos' => true, 'staging' => false, 'eab_kid' => 'KID1', 'eab_hmac_key' => 'SECRET'], $this->lastCall('ssl.account_register'));
        // The EAB HMAC secret must not be written to the audit log.
        $row = $this->app->container->get(Connection::class)->fetch("SELECT details FROM audit_logs WHERE action = 'ssl.account_register' ORDER BY id DESC LIMIT 1");
        self::assertStringNotContainsString('SECRET', (string) ($row['details'] ?? ''), 'EAB HMAC key must never be logged');
    }

    public function testCustomersCannotRegisterLetsEncryptAccount(): void
    {
        $this->login('cust');
        $this->post('/ssl/services/account', ['email' => 'x@real.test', 'agree_tos' => '1']);
        self::assertSame(0, $this->calls('ssl.account_register'));
    }

    public function testCustomersKeepTheirOwnPage(): void
    {
        $this->login('cust');
        self::assertNull($this->request('GET', '/ssl')->header('Location'));
        foreach (['/ssl/hosts', '/ssl/certificates', '/ssl/keys', '/ssl/tls'] as $u) {
            self::assertNotSame(200, $this->request('GET', $u)->status(), $u);
        }
        $this->post('/ssl/keys', ['names' => 'shop.test']);
        $this->post('/ssl/keys/install', ['key_id' => 'x-20260901120000', 'website_id' => (string) $this->siteId, 'self_signed' => '1']);
        $this->post('/ssl/tls', ['protocols' => 'tls13', 'ciphers' => 'default']);
        foreach (['ssl.csr_create', 'ssl.custom', 'ssl.tls_set'] as $a) {
            self::assertSame(0, $this->calls($a), $a);
        }
    }

    public function testSecurityHeadersPerWebsite(): void
    {
        $this->agent->succeed('sites.apply', ['domain' => 'shop.test', 'changed' => [], 'user' => ['name' => 'shop', 'uid' => 1500, 'gid' => 1500]]);
        $this->login('admin');
        self::assertStringContainsString('Send recommended security headers', $this->request('GET', '/sites/' . $this->siteId . '/ssl')->body());
        $this->post('/sites/' . $this->siteId . '/security-headers', ['security_headers' => '1']);
        self::assertTrue($this->lastCall('sites.apply')['security_headers']);
        $this->post('/sites/' . $this->siteId . '/security-headers', ['security_headers' => '0']);
        self::assertFalse($this->lastCall('sites.apply')['security_headers']);
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        $n = $this->calls('sites.apply');
        $this->post('/sites/' . $this->siteId . '/security-headers', ['security_headers' => '1']);
        self::assertSame($n, $this->calls('sites.apply'), 'not the customer\'s website');
    }
}
