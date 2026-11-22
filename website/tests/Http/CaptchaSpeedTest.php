<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class CaptchaSpeedTest extends SiteTestCase
{
    protected function setUp(): void
    {
        $this->captchaProvider = 'builtin';
        $this->captchaMinAge = 3;
        parent::setUp();
    }

    public function testAnAnswerSentTheMomentTheFormWasShownIsRefused(): void
    {
        $html = $this->get('/support/contact')->body;
        preg_match('/name="captcha_token" value="([^"]+)"/', $html, $t);
        foreach (range(1, 5) as $answer) {
            $res = $this->post('/support/contact', ['name' => 'Grace', 'email' => 'g@example.test', 'subject' => 'A question', 'message' => 'Hello, I have a question about NimVor.', 'captcha_token' => $t[1], 'captcha_answer' => (string) $answer]);
            self::assertSame(422, $res->status);
        }
        self::assertSame(0, $this->rows('tickets'));
    }
}
