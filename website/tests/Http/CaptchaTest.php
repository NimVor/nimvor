<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class CaptchaTest extends SiteTestCase
{
    private const WORDS = ['one' => 1, 'two' => 2, 'three' => 3, 'four' => 4, 'five' => 5, 'six' => 6, 'seven' => 7, 'eight' => 8, 'nine' => 9, 'ten' => 10];

    protected function setUp(): void
    {
        $this->captchaProvider = 'builtin';
        parent::setUp();
    }

    /** @return array{captcha_token: string, captcha_answer: string} the question on a form page, answered */
    private function solve(string $path): array
    {
        $html = $this->get($path)->body;
        self::assertMatchesRegularExpression('/name="captcha_token" value="([^"]+)"/', $html);
        preg_match('/name="captcha_token" value="([^"]+)"/', $html, $t);
        preg_match('/What is ([a-z]+) (plus|minus) ([a-z]+)(?: plus ([a-z]+))?\?/', $html, $q);
        $sum = $q[2] === 'plus' ? self::WORDS[$q[1]] + self::WORDS[$q[3]] : self::WORDS[$q[1]] - self::WORDS[$q[3]];
        if (($q[4] ?? '') !== '') {
            $sum += self::WORDS[$q[4]];
        }
        return ['captcha_token' => $t[1], 'captcha_answer' => (string) $sum];
    }

    /** @return array<string, string> */
    private function contact(array $over = []): array
    {
        return $over + ['name' => 'Grace', 'email' => 'grace@example.test', 'subject' => 'A question', 'message' => 'Hello, I have a question about NimVor.', 'category' => 'general'];
    }

    /** @return list<array{string}> */
    public static function forms(): array
    {
        return [['/login'], ['/register'], ['/support/contact'], ['/resources/build-services']];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('forms')]
    public function testEveryPublicFormAsksAQuestion(string $path): void
    {
        $body = $this->get($path)->body;
        self::assertStringContainsString('name="captcha_answer"', $body);
        self::assertStringContainsString('Are you a person?', $body);
        self::assertStringNotContainsString('challenges.cloudflare.com', $body, 'nothing is loaded from a third party');
    }

    public function testAFormWithTheRightAnswerIsAccepted(): void
    {
        $res = $this->post('/support/contact', $this->contact($this->solve('/support/contact')));
        self::assertSame(303, $res->status);
        self::assertSame(1, $this->rows('tickets'));
    }

    public function testAMissingOrWrongAnswerIsRefusedAndTheFormKept(): void
    {
        $res = $this->post('/support/contact', $this->contact());
        self::assertSame(422, $res->status);
        self::assertStringContainsString('That answer was not right', $res->body);
        self::assertStringContainsString('value="Grace"', $res->body, 'what was typed is kept');
        self::assertStringContainsString('name="captcha_token"', $res->body, 'a new question is shown');
        $a = $this->solve('/support/contact');
        $a['captcha_answer'] = (string) ((int) $a['captcha_answer'] + 1);
        self::assertSame(422, $this->post('/support/contact', $this->contact($a))->status);
        self::assertSame(0, $this->rows('tickets'));
    }

    public function testAQuestionCanBeTriedOnlyOnce(): void
    {
        $a = $this->solve('/support/contact');
        $wrong = ['captcha_token' => $a['captcha_token'], 'captcha_answer' => (string) ((int) $a['captcha_answer'] + 1)];
        self::assertSame(422, $this->post('/support/contact', $this->contact($wrong))->status);
        self::assertSame(422, $this->post('/support/contact', $this->contact($a))->status, 'the same question never works twice');
        $b = $this->solve('/support/contact');
        self::assertSame(303, $this->post('/support/contact', $this->contact($b))->status);
        self::assertSame(422, $this->post('/support/contact', $this->contact($b))->status, 'a good answer cannot be replayed');
    }

    public function testATamperedTokenIsRefused(): void
    {
        $a = $this->solve('/support/contact');
        [$body, $mac] = explode('.', $a['captcha_token']);
        $forged = ['captcha_token' => $body . '.' . strrev($mac), 'captcha_answer' => $a['captcha_answer']];
        self::assertSame(422, $this->post('/support/contact', $this->contact($forged))->status);
        $payload = json_decode((string) base64_decode(strtr($body, '-_', '+/')), true);
        $payload['a'] = hash_hmac('sha256', 'x', 'y');
        $changed = rtrim(strtr(base64_encode((string) json_encode($payload)), '+/', '-_'), '=');
        self::assertSame(422, $this->post('/support/contact', $this->contact(['captcha_token' => $changed . '.' . $mac, 'captcha_answer' => $a['captcha_answer']]))->status);
        self::assertSame(422, $this->post('/support/contact', $this->contact(['captcha_token' => 'x', 'captcha_answer' => '5']))->status);
        self::assertSame(422, $this->post('/support/contact', $this->contact(['captcha_token' => $a['captcha_token'], 'captcha_answer' => 'abc']))->status);
    }

    public function testSigningInNeedsTheAnswerToo(): void
    {
        $this->createUser('ada@example.test', 'Ada');
        $res = $this->post('/login', ['email' => 'ada@example.test', 'password' => 'a-long-test-password']);
        self::assertSame(422, $res->status);
        self::assertStringContainsString('That answer was not right', $res->body);
        $ok = $this->post('/login', ['email' => 'ada@example.test', 'password' => 'a-long-test-password'] + $this->solve('/login'));
        self::assertSame(303, $ok->status);
    }

    public function testRegisteringNeedsTheAnswerToo(): void
    {
        $data = ['name' => 'Ada', 'email' => 'ada@example.test', 'password' => 'a-long-test-password', 'password_confirm' => 'a-long-test-password'];
        self::assertSame(422, $this->post('/register', $data)->status);
        self::assertSame(0, $this->rows('users'));
        self::assertSame(303, $this->post('/register', $data + $this->solve('/register'))->status);
        self::assertSame(1, $this->rows('users'));
    }

    public function testSignedInPeopleAreNotAsked(): void
    {
        $this->createUser('ada@example.test', 'Ada');
        $this->post('/login', ['email' => 'ada@example.test', 'password' => 'a-long-test-password'] + $this->solve('/login'));
        self::assertStringNotContainsString('captcha_answer', $this->get('/support/contact')->body);
        self::assertSame(303, $this->post('/support/contact', $this->contact())->status);
    }

    public function testTheBuildServicesFormIsChecked(): void
    {
        $data = ['name' => 'Linus', 'email' => 'linus@example.test', 'service' => 'migration', 'details' => 'Move three sites from another panel, please.'];
        self::assertSame(422, $this->post('/resources/build-services', $data)->status);
        self::assertSame(303, $this->post('/resources/build-services', $data + $this->solve('/resources/build-services'))->status);
    }
}
