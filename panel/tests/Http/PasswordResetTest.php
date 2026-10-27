<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Mail\Mailer;
use NimVor\Tests\Support\ArrayMailer;
use NimVor\Tests\Support\HttpTestCase;

final class PasswordResetTest extends HttpTestCase
{
    private ArrayMailer $mailer;

    protected function setUp(): void
    {
        parent::setUp();
        $this->mailer = new ArrayMailer();
        $this->app->container->instance(Mailer::class, $this->mailer);
        $this->seedUser('alice', 'admin');
        $this->login('alice');
        $this->post('/settings', ['panel_url' => 'https://panel.example.com']);
        $this->post('/logout');
    }

    private function link(): string
    {
        self::assertNotEmpty($this->mailer->sent);
        self::assertSame(1, preg_match('#https://panel\.example\.com/password/reset/([a-f0-9]{64})#', end($this->mailer->sent)['text'], $m));
        return $m[1];
    }

    public function testResetFlow(): void
    {
        self::assertStringContainsString('Forgot your password?', $this->request('GET', '/login')->body());
        $res = $this->post('/password/forgot', ['identifier' => 'alice']);
        self::assertSame('/login', $res->header('Location'));
        $generic = (string) $this->flash('success');
        self::assertSame(['alice@example.test'], $this->mailer->sent[0]['to']);
        $token = $this->link();
        self::assertStringNotContainsString($token, (string) json_encode($this->app->container->get(Connection::class)->fetchAll('SELECT * FROM password_resets')), 'only the hash is stored');

        // Unknown accounts get the same answer and no email.
        $this->post('/password/forgot', ['identifier' => 'nobody']);
        self::assertSame($generic, (string) $this->flash('success'));
        self::assertCount(1, $this->mailer->sent);

        self::assertSame(200, $this->request('GET', '/password/reset/' . $token)->status());
        $this->post('/password/reset/' . $token, ['password' => 'short', 'password_confirmation' => 'short']);
        self::assertArrayHasKey('password', (array) $this->flash('errors'));
        $this->post('/password/reset/' . $token, ['password' => 'a-brand-new-password-9', 'password_confirmation' => 'a-brand-new-password-9']);
        self::assertStringContainsString('changed', (string) $this->flash('success'));

        self::assertSame(404, $this->request('GET', '/password/reset/' . $token)->status(), 'single use');
        $res = $this->post('/login', ['username' => 'alice', 'password' => 'a-brand-new-password-9']);
        self::assertSame('/', $res->header('Location'));
    }

    public function testExpiryThrottlingAndAvailability(): void
    {
        $this->post('/password/forgot', ['identifier' => 'alice@example.test']);
        $token = $this->link();
        $this->app->container->get(Connection::class)->run('UPDATE password_resets SET expires_at = 1');
        self::assertSame(404, $this->request('GET', '/password/reset/' . $token)->status());

        $this->post('/password/forgot', ['identifier' => 'alice']);
        $this->post('/password/forgot', ['identifier' => 'alice']);
        $this->post('/password/forgot', ['identifier' => 'alice']);
        self::assertCount(3, $this->mailer->sent, 'at most three emails per account per hour');

        $this->mailer->ready = false;
        self::assertStringContainsString('not set up', $this->request('GET', '/password/forgot')->body());
        $this->post('/password/forgot', ['identifier' => 'alice']);
        self::assertCount(3, $this->mailer->sent);
    }
}
