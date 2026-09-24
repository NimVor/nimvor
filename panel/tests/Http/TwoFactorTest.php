<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Security\TwoFactor;
use WhaCP\Tests\Support\HttpTestCase;

final class TwoFactorTest extends HttpTestCase
{
    private const T = 1_900_000_005;

    protected function setUp(): void
    {
        parent::setUp();
        // A fixed clock, so codes never cross a 30-second boundary mid-test.
        $c = $this->app->container;
        $c->instance(TwoFactor::class, new TwoFactor($c->get(Connection::class), $c->get(\WhaCP\Security\Crypto::class), static fn (): int => self::T));
    }

    private function now(string $secret, int $offset = 0): string
    {
        return TwoFactor::code($secret, intdiv(self::T, TwoFactor::PERIOD) + $offset);
    }

    /** Turn two-factor on for the signed-in user; returns [secret, recovery codes]. */
    private function enable(): array
    {
        $this->post('/profile/two-factor/setup');
        $body = $this->request('GET', '/profile')->body();
        self::assertSame(1, preg_match('/secret=([A-Z2-7]{32})/', $body, $m));
        $secret = $m[1];

        $this->post('/profile/two-factor/enable', ['code' => $this->now($secret), 'current_password' => 'wrong-password-123']);
        self::assertStringContainsString('password', (string) $this->flash('error'));
        $this->post('/profile/two-factor/enable', ['code' => '000000', 'current_password' => 'a-long-test-password']);
        self::assertStringContainsString('not valid', (string) $this->flash('error'));

        // The previous step is accepted too (clock drift) — and consumed.
        $this->post('/profile/two-factor/enable', ['code' => $this->now($secret, -1), 'current_password' => 'a-long-test-password']);
        $this->flash('success');
        $body = $this->request('GET', '/profile')->body();
        self::assertSame(1, preg_match('#data-wcp-recovery>([^<]*)</pre>#', $body, $pre));
        self::assertSame(10, preg_match_all('/^[a-z2-7]{5}-[a-z2-7]{5}$/m', $pre[1], $codes));
        self::assertStringNotContainsString('data-wcp-recovery', $this->request('GET', '/profile')->body(), 'shown once');
        self::assertStringNotContainsString($secret, (string) json_encode($this->app->container->get(Connection::class)->fetchAll('SELECT * FROM user_two_factor')), 'the secret is stored encrypted');
        return [$secret, $codes[0]];
    }

    public function testSignInNeedsTheSecondFactor(): void
    {
        $this->seedUser('alice', 'admin');
        $this->login('alice');
        [$secret, $codes] = $this->enable();
        $this->post('/logout');

        $res = $this->post('/login', ['username' => 'alice', 'password' => 'a-long-test-password']);
        self::assertSame('/login/two-factor', $res->header('Location'));
        self::assertStringStartsWith('/login', (string) $this->request('GET', '/')->header('Location'), 'not signed in before the code');
        self::assertStringContainsString('Two-factor sign-in', $this->request('GET', '/login/two-factor')->body());

        $this->post('/login/two-factor', ['code' => '123456']);
        self::assertStringContainsString('not valid', (string) $this->flash('error'));
        $res = $this->post('/login/two-factor', ['code' => $this->now($secret)]);
        self::assertSame('/', $res->header('Location'));
        self::assertSame(200, $this->request('GET', '/')->status());
        $this->post('/logout');

        // A code cannot be used twice.
        $this->post('/login', ['username' => 'alice', 'password' => 'a-long-test-password']);
        $this->post('/login/two-factor', ['code' => $this->now($secret)]);
        self::assertStringContainsString('not valid', (string) $this->flash('error'));

        // A recovery code works once.
        $res = $this->post('/login/two-factor', ['code' => strtoupper($codes[0])]);
        self::assertSame('/', $res->header('Location'));
        $this->post('/logout');
        $this->post('/login', ['username' => 'alice', 'password' => 'a-long-test-password']);
        $this->post('/login/two-factor', ['code' => $codes[0]]);
        self::assertStringContainsString('not valid', (string) $this->flash('error'));
        self::assertStringContainsString('9 recovery codes left', $this->loginAndProfile($secret));
    }

    private function loginAndProfile(string $secret): string
    {
        // The next step has not been used yet.
        $this->post('/login/two-factor', ['code' => $this->now($secret, 1)]);
        return $this->request('GET', '/profile')->body();
    }

    public function testTooManyWrongCodesRestartTheSignIn(): void
    {
        $this->seedUser('alice', 'admin');
        $this->login('alice');
        $this->enable();
        $this->post('/logout');
        $this->post('/login', ['username' => 'alice', 'password' => 'a-long-test-password']);
        for ($i = 0; $i < 5; $i++) {
            $res = $this->post('/login/two-factor', ['code' => '000000']);
        }
        self::assertSame('/login', $res->header('Location'));
        self::assertSame('/login', $this->request('GET', '/login/two-factor')->header('Location'));
    }

    public function testDisableAndAdminReset(): void
    {
        $bob = $this->seedUser('bob', 'customer');
        $this->seedUser('alice', 'admin');
        $this->login('bob');
        $this->enable();
        $this->post('/profile/two-factor/disable', ['current_password' => 'nope-nope-nope']);
        self::assertStringContainsString('password', (string) $this->flash('error'));
        $this->post('/logout');

        $this->login('alice');
        self::assertStringContainsString('Reset two-factor sign-in', $this->request('GET', '/users/' . $bob . '/edit')->body());
        $this->post('/users/' . $bob . '/two-factor/reset');
        self::assertFalse($this->app->container->get(TwoFactor::class)->enabled($bob));
        $this->post('/logout');
        $this->login('bob'); // no second factor any more

        $this->enable();
        $this->post('/profile/two-factor/disable', ['current_password' => 'a-long-test-password']);
        self::assertFalse($this->app->container->get(TwoFactor::class)->enabled($bob));
    }
}
