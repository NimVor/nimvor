<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Security\Csrf;
use WhaCP\Services\SettingsService;
use WhaCP\Tests\Support\HttpTestCase;
use WhaCP\Tests\Support\SoftAuthenticator;

require_once __DIR__ . '/../Support/SoftAuthenticator.php';

final class SecurityKeyTest extends HttpTestCase
{
    private const ORIGIN = 'https://panel.example.com';

    protected function setUp(): void
    {
        parent::setUp();
        $this->app->container->get(SettingsService::class)->update(['panel_url' => self::ORIGIN], null);
    }

    /** @param array<string, mixed> $body @return array<string, mixed> */
    private function json(string $path, array $body = []): array
    {
        $token = $this->app->container->get(Csrf::class)->token();
        $res = $this->request('POST', $path, $body, ['content-type' => 'application/json', 'accept' => 'application/json', 'x-csrf-token' => $token], (string) json_encode($body));
        return ['status' => $res->status()] + (array) json_decode($res->body(), true);
    }

    private function addKey(SoftAuthenticator $key, string $name): void
    {
        $opts = $this->json('/profile/security-keys/options')['data'];
        self::assertSame('panel.example.com', $opts['rp']['id']);
        $r = $key->create($opts, self::ORIGIN);
        $res = $this->json('/profile/security-keys', ['name' => $name, 'client_data' => $r['client_data'], 'attestation' => $r['attestation']]);
        self::assertTrue($res['success'], json_encode($res));
    }

    public function testSignInWithASecurityKey(): void
    {
        $this->seedUser('alice', 'admin');
        $this->login('alice');
        $key = new SoftAuthenticator();
        $this->addKey($key, 'YubiKey');
        self::assertStringContainsString('YubiKey', $this->request('GET', '/profile')->body());
        $this->post('/logout');

        // The password alone does not sign in; the second-factor page offers the key only.
        $this->post('/login', ['username' => 'alice', 'password' => 'a-long-test-password']);
        self::assertSame('/login', $this->request('GET', '/')->header('Location'));
        $page = $this->request('GET', '/login/two-factor')->body();
        self::assertStringContainsString('Use security key', $page);
        self::assertStringNotContainsString('name="code"', $page, 'no code field without an authenticator app');

        // A signature from another key fails; the right key signs in.
        $opts = $this->json('/login/two-factor/key/options')['data'];
        $bad = (new SoftAuthenticator())->get($opts, self::ORIGIN);
        $res = $this->json('/login/two-factor/key', $bad);
        self::assertSame(400, $res['status']);
        $opts = $this->json('/login/two-factor/key/options')['data'];
        $res = $this->json('/login/two-factor/key', $key->get($opts, self::ORIGIN));
        self::assertTrue($res['success'], json_encode($res));
        self::assertSame(200, $this->request('GET', '/')->status());
        $audit = (string) $this->app->container->get(Connection::class)->scalar("SELECT details FROM audit_logs WHERE action = 'auth.login' AND result = 'success' ORDER BY id DESC LIMIT 1");
        self::assertStringContainsString('security_key', $audit);

        // Removing a key needs the password.
        $id = (int) $this->app->container->get(Connection::class)->scalar('SELECT id FROM webauthn_credentials');
        $this->post('/profile/security-keys/' . $id . '/delete', ['current_password' => 'wrong-one-123']);
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar('SELECT COUNT(*) FROM webauthn_credentials'));
        $this->post('/profile/security-keys/' . $id . '/delete', ['current_password' => 'a-long-test-password']);
        self::assertSame(0, (int) $this->app->container->get(Connection::class)->scalar('SELECT COUNT(*) FROM webauthn_credentials'));
    }

    public function testKeySatisfiesRoleRequirementAndAdminResetRemovesIt(): void
    {
        $db = $this->app->container->get(Connection::class);
        $db->run("UPDATE roles SET require_two_factor = 1 WHERE name = 'operator'");
        $this->seedUser('admin', 'admin');
        $this->seedUser('bob', 'operator');
        $this->login('bob');
        self::assertSame('/profile#two-factor', $this->request('GET', '/websites')->header('Location'));
        $this->addKey(new SoftAuthenticator('EdDSA'), 'Laptop passkey');
        self::assertSame(200, $this->request('GET', '/websites')->status());

        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('admin');
        $bob = (int) $db->scalar("SELECT id FROM users WHERE username = 'bob'");
        $this->post('/users/' . $bob . '/two-factor/reset');
        self::assertSame(0, (int) $db->scalar('SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?', [$bob]));
    }

    public function testKeyRegisteredForAnotherAddressIsRefused(): void
    {
        $this->seedUser('alice', 'admin');
        $this->login('alice');
        $opts = $this->json('/profile/security-keys/options')['data'];
        $r = (new SoftAuthenticator())->create($opts, 'https://phish.example');
        $res = $this->json('/profile/security-keys', ['name' => 'x', 'client_data' => $r['client_data'], 'attestation' => $r['attestation']]);
        self::assertSame(400, $res['status']);
        self::assertStringContainsString('different address', $res['error']['message']);
    }
}
