<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class CaptchaProviderTest extends SiteTestCase
{
    protected function setUp(): void
    {
        $this->captchaProvider = 'turnstile';
        parent::setUp();
    }

    /** @return array<string, string> */
    private function contact(array $over = []): array
    {
        return $over + ['name' => 'Grace', 'email' => 'grace@example.test', 'subject' => 'A question', 'message' => 'Hello, I have a question about NimVor.', 'category' => 'general'];
    }

    public function testTheWidgetAndThePolicyAllowOnlyTheChosenProvider(): void
    {
        $res = $this->get('/support/contact');
        self::assertStringContainsString('class="cf-turnstile" data-sitekey="site-key"', $res->body);
        self::assertStringContainsString('https://challenges.cloudflare.com/turnstile/v0/api.js', $res->body);
        self::assertStringContainsString('script-src \'self\' https://challenges.cloudflare.com', $res->headers['Content-Security-Policy']);
        self::assertStringNotContainsString('hcaptcha', $res->headers['Content-Security-Policy']);
    }

    public function testTheProviderIsAskedAndTheAnswerHonoured(): void
    {
        $this->http->on('https://challenges.cloudflare.com/turnstile/v0/siteverify', 200, ['success' => true]);
        self::assertSame(303, $this->post('/support/contact', $this->contact(['cf-turnstile-response' => 'token-123']))->status);
        $sent = $this->http->calls[0]['body'];
        self::assertStringContainsString('secret=secret-key', $sent);
        self::assertStringContainsString('response=token-123', $sent);
    }

    public function testARefusalAMissingResponseAndAnOutageAllRefuseTheForm(): void
    {
        $this->http->on('https://challenges.cloudflare.com/turnstile/v0/siteverify', 200, ['success' => false]);
        self::assertSame(422, $this->post('/support/contact', $this->contact(['cf-turnstile-response' => 'bad']))->status);
        self::assertSame(422, $this->post('/support/contact', $this->contact())->status);
        $this->http->down = true;
        self::assertSame(422, $this->post('/support/contact', $this->contact(['cf-turnstile-response' => 'token']))->status, 'a form that cannot be checked is not accepted');
        self::assertSame(0, $this->rows('tickets'));
    }
}
