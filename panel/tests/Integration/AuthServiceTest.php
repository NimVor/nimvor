<?php

declare(strict_types=1);

namespace NimVor\Tests\Integration;

use NimVor\Http\Session\ArraySession;
use NimVor\Repositories\LoginAttemptRepository;
use NimVor\Security\AuthService;
use NimVor\Security\LoginThrottle;
use NimVor\Tests\Support\DatabaseTestCase;

final class AuthServiceTest extends DatabaseTestCase
{
    private ArraySession $session;
    private int $now = 1_800_000_000;

    private function auth(int $idle = 1800): AuthService
    {
        $clock = fn (): int => $this->now;
        return new AuthService(
            $this->session,
            $this->users,
            $this->hasher,
            new LoginThrottle(new LoginAttemptRepository($this->db), 10, 3, 900, $clock),
            $this->audit,
            $idle,
            43200,
            $clock,
        );
    }

    protected function setUp(): void
    {
        parent::setUp();
        $this->session = new ArraySession();
        $this->createUser('alice', 'admin', 'alice-password-123');
    }

    public function testSuccessfulLoginRegeneratesSessionAndAudits(): void
    {
        $auth = $this->auth();
        self::assertNull($auth->attempt('alice', 'alice-password-123', '192.0.2.1'));
        self::assertSame(1, $this->session->regenerations);
        self::assertSame('alice', $auth->user()['username'] ?? null);
        $rows = $this->auditRows();
        self::assertSame('auth.login', end($rows)['action']);
        self::assertSame('success', end($rows)['result']);
        self::assertSame('192.0.2.1', $this->users->find(1)['last_login_ip']);
    }

    public function testLoginWithEmailWorks(): void
    {
        self::assertNull($this->auth()->attempt('alice@example.test', 'alice-password-123', '192.0.2.1'));
    }

    public function testWrongPasswordAndUnknownUserGiveSameMessage(): void
    {
        $auth = $this->auth();
        $a = $auth->attempt('alice', 'nope', '192.0.2.1');
        $b = $auth->attempt('mallory', 'nope', '192.0.2.1');
        self::assertSame($a, $b);
        self::assertNull($auth->user());
    }

    public function testInactiveUserCannotLogIn(): void
    {
        $this->createUser('bob', 'viewer', 'bob-password-123', false);
        self::assertNotNull($this->auth()->attempt('bob', 'bob-password-123', '192.0.2.1'));
    }

    public function testThrottleLocksOutAfterRepeatedFailures(): void
    {
        $auth = $this->auth();
        for ($i = 0; $i < 3; $i++) {
            $auth->attempt('alice', 'bad', '192.0.2.1');
        }
        $msg = $auth->attempt('alice', 'alice-password-123', '192.0.2.1');
        self::assertNotNull($msg);
        self::assertStringContainsString('Too many', (string) $msg);

        $this->now += 901;
        self::assertNull($this->auth()->attempt('alice', 'alice-password-123', '192.0.2.1'));
    }

    public function testIdleTimeoutEndsSession(): void
    {
        $this->auth(600)->attempt('alice', 'alice-password-123', '192.0.2.1');
        $this->now += 300;
        self::assertNotNull($this->auth(600)->user());
        $this->now += 601;
        self::assertNull($this->auth(600)->user());
    }

    public function testPasswordChangeInvalidatesOtherSessions(): void
    {
        $this->auth()->attempt('alice', 'alice-password-123', '192.0.2.1');
        self::assertNotNull($this->auth()->user());
        $this->users->updatePassword(1, $this->hasher->hash('a-brand-new-password'));
        self::assertNull($this->auth()->user());
    }

    public function testDisablingUserEndsSession(): void
    {
        $this->auth()->attempt('alice', 'alice-password-123', '192.0.2.1');
        $this->db->run('UPDATE users SET is_active = 0 WHERE id = 1');
        self::assertNull($this->auth()->user());
    }

    public function testLogoutClearsSession(): void
    {
        $auth = $this->auth();
        $auth->attempt('alice', 'alice-password-123', '192.0.2.1');
        $auth->logout();
        self::assertNull($this->auth()->user());
    }
}
