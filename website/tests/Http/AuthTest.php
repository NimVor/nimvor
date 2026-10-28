<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class AuthTest extends SiteTestCase
{
    /** @return array<string, string> */
    private function form(array $over = []): array
    {
        return $over + ['name' => 'Ada Lovelace', 'email' => 'ada@example.test', 'password' => 'a-long-test-password', 'password_confirm' => 'a-long-test-password'];
    }

    public function testRegistrationCreatesAClientAndSignsThemIn(): void
    {
        $res = $this->post('/register', $this->form());
        self::assertSame([303, '/account'], [$res->status, $res->headers['Location']]);
        $u = $this->row('SELECT * FROM users WHERE email = ?', ['ada@example.test']);
        self::assertSame('client', $u['role']);
        self::assertStringStartsWith('$argon2id$', $u['password_hash']);
        self::assertStringContainsString('Hello, Ada', $this->get('/account')->body);
    }

    public function testRegistrationIsValidated(): void
    {
        foreach ([
            ['name' => '', 'name_err' => 'Enter your name'],
            ['email' => 'not-an-email', 'name_err' => 'valid email'],
            ['password' => 'short', 'password_confirm' => 'short', 'name_err' => 'at least 12'],
            ['password_confirm' => 'something-else-entirely', 'name_err' => 'differ'],
            ['password' => 'password1234', 'password_confirm' => 'password1234', 'name_err' => 'too easy'],
        ] as $case) {
            $err = $case['name_err'];
            unset($case['name_err']);
            $res = $this->post('/register', $this->form($case));
            self::assertSame(422, $res->status);
            self::assertStringContainsString($err, $res->body);
        }
        self::assertSame(0, $this->rows('users'));
    }

    public function testAPasswordIsNeverEchoedBack(): void
    {
        $res = $this->post('/register', $this->form(['password' => 'short', 'password_confirm' => 'short']));
        self::assertStringNotContainsString('value="short"', $res->body);
        self::assertStringContainsString('value="ada@example.test"', $res->body, 'the rest of the form is kept');
    }

    public function testDuplicateEmailIsRefused(): void
    {
        $this->createUser('ada@example.test');
        self::assertSame(422, $this->post('/register', $this->form())->status);
        self::assertSame(1, $this->rows('users'));
    }

    public function testLoginSucceedsAndLogoutEndsTheSession(): void
    {
        $this->createUser('ada@example.test');
        $res = $this->login('ada@example.test');
        self::assertSame('/account', $res->headers['Location']);
        self::assertSame(200, $this->get('/account')->status);
        $this->post('/logout');
        self::assertSame(303, $this->get('/account')->status);
    }

    public function testWrongPasswordAndUnknownEmailLookTheSame(): void
    {
        $this->createUser('ada@example.test');
        $a = $this->post('/login', ['email' => 'ada@example.test', 'password' => 'wrong-password-here']);
        $b = $this->post('/login', ['email' => 'nobody@example.test', 'password' => 'wrong-password-here']);
        self::assertSame([422, 422], [$a->status, $b->status]);
        self::assertStringContainsString('The email or password is not right.', $a->body);
        self::assertStringContainsString('The email or password is not right.', $b->body);
    }

    public function testRepeatedFailuresAreThrottledEvenWithTheRightPassword(): void
    {
        $this->createUser('ada@example.test');
        for ($i = 0; $i < 6; $i++) {
            $this->post('/login', ['email' => 'ada@example.test', 'password' => 'wrong-password-' . $i]);
        }
        $res = $this->post('/login', ['email' => 'ada@example.test', 'password' => 'a-long-test-password']);
        self::assertSame(429, $res->status);
        self::assertStringContainsString('Too many failed attempts', $res->body);
    }

    public function testTheSessionIdChangesAtLogin(): void
    {
        $this->createUser('ada@example.test');
        $this->get('/login');
        $before = $this->cookies['nimvor_sid'];
        $this->login('ada@example.test');
        self::assertNotSame($before, $this->cookies['nimvor_sid'], 'a fixed session id is worthless');
        // The old id no longer works.
        $this->cookies['nimvor_sid'] = $before;
        self::assertSame(303, $this->get('/account')->status);
    }

    public function testNextOnlyFollowsLocalPaths(): void
    {
        $this->createUser('ada@example.test');
        $res = $this->post('/login', ['email' => 'ada@example.test', 'password' => 'a-long-test-password', 'next' => '/account/profile']);
        self::assertSame('/account/profile', $res->headers['Location']);
        $this->post('/logout');
        foreach (['https://evil.test/', '//evil.test/', '/\\evil.test', "/ok\r\nSet-Cookie: x=1"] as $next) {
            $res = $this->post('/login', ['email' => 'ada@example.test', 'password' => 'a-long-test-password', 'next' => $next]);
            self::assertSame('/account', $res->headers['Location'], $next);
            $this->post('/logout');
        }
    }

    public function testADeactivatedUserCannotLogInOrKeepASession(): void
    {
        $id = $this->createUser('ada@example.test');
        $this->login('ada@example.test');
        $this->app->users->setActive($id, false);
        self::assertSame(303, $this->get('/account')->status, 'the open session stops working');
        self::assertSame(422, $this->post('/login', ['email' => 'ada@example.test', 'password' => 'a-long-test-password'])->status);
    }

    public function testRegistrationIsRateLimitedPerAddress(): void
    {
        for ($i = 0; $i < 5; $i++) {
            $this->post('/register', $this->form(['email' => "u{$i}@example.test"]));
            $this->post('/logout');
        }
        self::assertSame(429, $this->post('/register', $this->form(['email' => 'u9@example.test']))->status);
    }
}
