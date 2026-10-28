<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Tests\Support\SiteTestCase;

final class FormsTest extends SiteTestCase
{
    /** @return array<string, string> */
    private function contact(array $over = []): array
    {
        return $over + ['name' => 'Grace', 'email' => 'grace@example.test', 'subject' => 'A question', 'message' => 'Hello, I have a question about NimVor.'];
    }

    public function testAContactMessageIsStored(): void
    {
        $res = $this->post('/support/contact', $this->contact());
        self::assertSame([303, '/support/contact'], [$res->status, $res->headers['Location']]);
        $m = $this->row('SELECT * FROM contact_messages');
        self::assertSame(['Grace', 'grace@example.test', 'new', null], [$m['name'], $m['email'], $m['status'], $m['user_id']]);
        self::assertStringContainsString('Thank you', $this->get('/support/contact')->body, 'the confirmation is shown once');
        self::assertStringNotContainsString('Thank you', $this->get('/support/contact')->body);
    }

    public function testAContactMessageIsValidatedAndKept(): void
    {
        $res = $this->post('/support/contact', $this->contact(['email' => 'bad', 'message' => 'short']));
        self::assertSame(422, $res->status);
        self::assertStringContainsString('valid email', $res->body);
        self::assertStringContainsString('at least 10 characters', $res->body);
        self::assertStringContainsString('value="Grace"', $res->body, 'what was typed is kept');
        self::assertSame(0, $this->rows('contact_messages'));
    }

    public function testTheHiddenFieldCatchesBots(): void
    {
        $res = $this->post('/support/contact', $this->contact(['website' => 'http://spam.test']));
        self::assertSame(303, $res->status, 'the bot is told it worked');
        self::assertSame(0, $this->rows('contact_messages'));
    }

    public function testFormsAreRateLimitedPerAddress(): void
    {
        for ($i = 0; $i < 6; $i++) {
            self::assertSame(303, $this->post('/support/contact', $this->contact())->status);
        }
        self::assertSame(429, $this->post('/support/contact', $this->contact())->status);
        self::assertSame(6, $this->rows('contact_messages'));
    }

    public function testASignedInUserIsPrefilledAndLinked(): void
    {
        $id = $this->createUser('ada@example.test', 'Ada');
        $this->login('ada@example.test');
        self::assertStringContainsString('value="ada@example.test"', $this->get('/support/contact')->body);
        $this->post('/support/contact', $this->contact(['email' => 'ada@example.test']));
        self::assertSame($id, (int) $this->row('SELECT user_id FROM contact_messages')['user_id']);
    }

    public function testABuildRequestIsStoredAndOnlyKnownServicesAreAccepted(): void
    {
        $data = ['name' => 'Linus', 'email' => 'linus@example.test', 'service' => 'migration', 'details' => 'Move three sites from another panel, please.'];
        self::assertSame(303, $this->post('/resources/build-services', $data)->status);
        $r = $this->row('SELECT * FROM build_requests');
        self::assertSame(['migration', 'new', null], [$r['service'], $r['status'], $r['user_id']]);
        self::assertSame(422, $this->post('/resources/build-services', ['service' => 'free-money'] + $data)->status);
        self::assertSame(1, $this->rows('build_requests'));
    }

    public function testASignedInBuildRequestAppearsOnTheDashboard(): void
    {
        $this->createUser('ada@example.test', 'Ada');
        $this->login('ada@example.test');
        $res = $this->post('/resources/build-services', ['name' => 'Ada', 'email' => 'ada@example.test', 'service' => 'install', 'details' => 'Install and harden a fresh Rocky Linux server.']);
        self::assertSame('/account/requests', $res->headers['Location']);
        self::assertStringContainsString('Install and harden a NimVor server', $this->get('/account')->body);
    }

    public function testProfileAndPasswordChange(): void
    {
        $this->createUser('ada@example.test', 'Ada');
        $this->login('ada@example.test');
        $bad = $this->post('/account/profile', ['name' => 'Ada', 'current_password' => 'wrong', 'new_password' => 'another-long-password', 'new_password_confirm' => 'another-long-password']);
        self::assertSame(422, $bad->status);
        self::assertStringContainsString('current password is not right', $bad->body);
        $ok = $this->post('/account/profile', ['name' => 'Ada L', 'current_password' => 'a-long-test-password', 'new_password' => 'another-long-password', 'new_password_confirm' => 'another-long-password']);
        self::assertSame(303, $ok->status);
        self::assertSame('Ada L', $this->row('SELECT name FROM users')['name']);
        $this->post('/logout');
        $this->login('ada@example.test', 'another-long-password');
    }
}
