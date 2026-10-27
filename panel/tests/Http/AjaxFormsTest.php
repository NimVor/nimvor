<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Security\Csrf;
use NimVor\Tests\Support\HttpTestCase;

/** Forms submitted in the background get their redirect as JSON; nothing else changes. */
final class AjaxFormsTest extends HttpTestCase
{
    private function ajaxPost(string $path, array $body, bool $withToken = true): \NimVor\Http\Response
    {
        if ($withToken) {
            $body['_token'] = $this->app->container->get(Csrf::class)->token();
        }
        return $this->request('POST', $path, $body, ['x-nimvor-ajax' => '1', 'accept' => 'text/html']);
    }

    public function testRedirectBecomesJsonWithTheSameEffect(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $res = $this->ajaxPost('/settings', ['panel_name' => 'Ajax Panel']);
        self::assertSame(200, $res->status());
        self::assertStringStartsWith('application/json', (string) $res->header('Content-Type'));
        $body = json_decode($res->body(), true);
        self::assertTrue($body['success']);
        self::assertStringStartsWith('/settings', $body['redirect']);
        self::assertNull($res->header('Location'));
        // The action ran and its message waits for the next page, as for a normal submit.
        self::assertStringContainsString('Ajax Panel', $this->request('GET', '/settings')->body());
    }

    public function testSecurityChecksAreUnchanged(): void
    {
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        // No CSRF token: refused exactly as without the header.
        $this->login('admin');
        $res = $this->ajaxPost('/settings', ['panel_name' => 'Nope'], false);
        self::assertGreaterThanOrEqual(400, $res->status());
        self::assertStringNotContainsString('Nope', $this->request('GET', '/settings')->body());
        // Not permitted: the same refusal.
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        $res = $this->ajaxPost('/settings', ['panel_name' => 'Nope']);
        self::assertNotSame(200, $res->status());
        // Signed out: the redirect to the sign-in page is reported, and the
        // page loads it in full.
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $res = $this->ajaxPost('/settings', ['panel_name' => 'Nope']);
        self::assertSame('/login', json_decode($res->body(), true)['redirect'] ?? null);
        $this->login('admin');
        self::assertStringNotContainsString('Nope', $this->request('GET', '/settings')->body());
    }

    public function testNormalSubmitsStillRedirect(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $res = $this->post('/settings', ['panel_name' => 'Plain']);
        self::assertSame(303, $res->status());
        self::assertStringStartsWith('/settings', (string) $res->header('Location'));
    }
}
