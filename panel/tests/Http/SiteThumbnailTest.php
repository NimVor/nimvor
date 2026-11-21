<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** The picture of a website's home page on its card: shown, refreshed over AJAX, access-checked, and the browser it needs. */
final class SiteThumbnailTest extends HttpTestCase
{
    private int $siteId;
    private int $otherId;
    private const JPEG = "\xFF\xD8\xFF\xE0fake-jpeg";

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($cust));
        $this->otherId = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($admin));
        $this->agent->succeed('discovery.inventory', ['websites' => ['nginx_version' => '1.24.0', 'catch_all_vhosts' => 0, 'warnings' => [], 'websites' => []]]);
        $this->agent->succeed('screenshot.status', ['installed' => true]);
        $this->agent->succeed('screenshot.capture', ['taken_at' => '2026-11-20T12:00:00Z', 'bytes' => 100]);
        $this->agent->succeed('screenshot.get', ['exists' => true, 'taken_at' => '2026-11-20T12:00:00Z', 'jpeg' => base64_encode(self::JPEG)]);
    }

    /** @return array<string, string> */
    private function json(): array
    {
        return ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest'];
    }

    private function token(): array
    {
        return ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
    }

    public function testACardShowsTheLetterUntilAPictureExistsThenThePicture(): void
    {
        $this->login('admin');
        $before = $this->request('GET', '/websites', [], [], '', ['view' => 'grid'])->body();
        self::assertStringContainsString('nim-site-initial', $before);
        self::assertStringNotContainsString('nim-site-shot', $before);
        self::assertStringContainsString('data-nim-thumb-refresh="/sites/' . $this->siteId . '/thumbnail"', $before, 'the refresh icon');

        $res = $this->request('POST', '/sites/' . $this->siteId . '/thumbnail', $this->token(), $this->json());
        self::assertSame(200, $res->status(), $res->body());
        $data = json_decode($res->body(), true);
        self::assertTrue($data['success']);
        self::assertStringStartsWith('/sites/' . $this->siteId . '/thumbnail?v=', $data['url']);
        self::assertSame(['domain' => 'shop.test', 'user' => $this->lastCall('screenshot.capture')['user'], 'root' => $this->lastCall('screenshot.capture')['root']], $this->lastCall('screenshot.capture'));

        $after = $this->request('GET', '/websites', [], [], '', ['view' => 'grid'])->body();
        self::assertStringContainsString('nim-site-shot', $after);
        self::assertStringContainsString('has-shot', $after);

        $img = $this->request('GET', '/sites/' . $this->siteId . '/thumbnail');
        self::assertSame(200, $img->status());
        self::assertSame(self::JPEG, $img->body());
        self::assertSame('image/jpeg', $img->headers()['Content-Type'] ?? null);
    }

    public function testAnotherPictureIsRefusedForAMoment(): void
    {
        $this->login('admin');
        self::assertSame(200, $this->request('POST', '/sites/' . $this->siteId . '/thumbnail', $this->token(), $this->json())->status());
        $again = $this->request('POST', '/sites/' . $this->siteId . '/thumbnail', $this->token(), $this->json());
        self::assertSame(429, $again->status());
        self::assertStringContainsString('half a minute', $again->body());
        self::assertSame(1, $this->calls('screenshot.capture'));
    }

    public function testACustomerReachesOnlyTheirOwnWebsite(): void
    {
        $this->login('cust');
        self::assertSame(200, $this->request('POST', '/sites/' . $this->siteId . '/thumbnail', $this->token(), $this->json())->status());
        self::assertSame(404, $this->request('POST', '/sites/' . $this->otherId . '/thumbnail', $this->token(), $this->json())->status());
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherId . '/thumbnail')->status());
    }

    public function testAnAgentErrorIsAnswerableNotAFatalPage(): void
    {
        $this->agent->fail('screenshot.capture', 409, 'conflict', 'No browser is installed to take the picture.');
        $this->login('admin');
        $res = $this->request('POST', '/sites/' . $this->siteId . '/thumbnail', $this->token(), $this->json());
        self::assertSame(502, $res->status());
        self::assertStringContainsString('No browser is installed', $res->body());
    }

    public function testAdministratorsAreOfferedTheBrowserWhenItIsMissing(): void
    {
        $this->agent->succeed('screenshot.status', ['installed' => false]);
        $this->agent->succeed('screenshot.install', ['job_id' => 'abcdef0123456789']);
        $this->login('admin');
        // The table (an administrator's default), the cards and the website's own page all carry the button.
        foreach (['list', 'grid'] as $view) {
            $page = $this->request('GET', '/websites', [], [], '', ['view' => $view])->body();
            self::assertStringContainsString('Install the browser', $page, $view);
        }
        self::assertStringContainsString('Install the browser', $this->request('GET', '/sites/' . $this->otherId)->body());
        $this->post('/server/screenshots/install');
        self::assertSame('abcdef0123456789', $this->session->pullFlash('job'));
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM agent_jobs WHERE job_id = 'abcdef0123456789'"));
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'screenshot.install'"));
    }
}
