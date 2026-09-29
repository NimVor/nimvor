<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Tests\Support\HttpTestCase;

/** Step-up confirmation for sensitive changes, and signing out other sessions. */
final class StepUpTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
    }

    private function expireConfirmation(): void
    {
        $this->session->set('_auth_confirmed_at', time() - 3600);
    }

    public function testSensitiveChangesNeedARecentPassword(): void
    {
        $this->login('admin');
        $db = $this->app->container->get(Connection::class);
        $user = ['username' => 'newop', 'email' => 'newop@example.test', 'display_name' => 'New', 'password' => 'Another-long-Passw0rd!', 'password_confirmation' => 'Another-long-Passw0rd!', 'role_id' => (string) $db->scalar("SELECT id FROM roles WHERE name = 'operator'"), 'is_active' => '1'];
        // Signing in counts as a confirmation.
        $this->post('/users', $user);
        self::assertNotNull($db->scalar("SELECT id FROM users WHERE username = 'newop'"));

        $this->expireConfirmation();
        $res = $this->request('POST', '/users', ['_token' => $this->app->container->get(\WhaCP\Security\Csrf::class)->token()] + ['username' => 'second', 'email' => 'second@example.test'] + $user, ['referer' => 'https://panel.test/users/create']);
        self::assertSame('/confirm-password?return=' . rawurlencode('/users/create'), $res->header('Location'));
        self::assertNull($db->scalar("SELECT id FROM users WHERE username = 'second'"));
        self::assertStringContainsString('need your password', $this->request('GET', '/confirm-password', [], [], '', ['return' => '/users/create'])->body());

        // A wrong password is refused and counted; the right one confirms.
        $this->post('/confirm-password', ['password' => 'wrong-password-1', 'return' => '/users/create']);
        self::assertFalse($this->app->container->get(\WhaCP\Security\AuthService::class)->recentlyConfirmed());
        $res = $this->post('/confirm-password', ['password' => 'a-long-test-password', 'return' => 'https://evil.test/x']);
        self::assertSame('/x', $res->header('Location'), 'only local paths');
        $this->post('/users', ['username' => 'second', 'email' => 'second@example.test'] + $user);
        self::assertNotNull($db->scalar("SELECT id FROM users WHERE username = 'second'"));

        // Other routes are unaffected.
        $this->expireConfirmation();
        self::assertSame(200, $this->request('GET', '/users')->status());
        // API tokens too.
        $this->post('/profile/tokens', ['name' => 'ci']);
        self::assertSame(0, (int) $db->scalar("SELECT COUNT(*) FROM api_tokens"));
    }

    public function testSignOutOtherSessions(): void
    {
        $this->login('admin');
        $first = $this->session;
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login('admin');
        $this->post('/profile/sessions/revoke');
        self::assertSame(200, $this->request('GET', '/profile')->status(), 'this session stays');
        $this->session = $first;
        $this->app = $this->makeApp();
        self::assertSame('/login', $this->request('GET', '/profile')->header('Location'), 'the other one ended');
    }
}
