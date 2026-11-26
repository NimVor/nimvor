<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Tests\Support\HttpTestCase;

/** The new-website page is a four-step wizard over the same form. */
final class CreateWizardTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('sites.apply', ['applied' => true]);
        $this->login('admin');
    }

    public function testThePageHasFourSteps(): void
    {
        $page = $this->request('GET', '/websites/new')->body();
        self::assertStringContainsString('data-nim-wizard', $page);
        foreach ([1, 2, 3, 4] as $n) {
            self::assertStringContainsString('data-nim-step-tab="' . $n . '"', $page);
            self::assertStringContainsString('data-nim-step="' . $n . '"', $page);
        }
        self::assertStringContainsString('name="domain"', $page);
        self::assertStringContainsString('name="kind"', $page);
        self::assertStringContainsString('data-nim-review', $page);
    }

    public function testTheSettingsPageIsNotAWizard(): void
    {
        $this->post('/websites/new', ['domain' => 'wiz.test', 'kind' => 'static']);
        $id = (int) $this->db()->scalar("SELECT id FROM websites WHERE domain = 'wiz.test'");
        $page = $this->request('GET', '/sites/' . $id . '/settings')->body();
        self::assertStringNotContainsString('data-nim-wizard', $page);
        self::assertStringNotContainsString('data-nim-step=', $page);
    }

    public function testCanOpenTheAppInstallerAfterwards(): void
    {
        $res = $this->post('/websites/new', ['domain' => 'blog.test', 'kind' => 'static', 'after_apps' => '1']);
        $id = (int) $this->db()->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        self::assertStringEndsWith('/sites/' . $id . '/apps', $res->header('Location') ?? '');
    }

    private function db(): \NimVor\Database\Connection
    {
        return $this->app->container->get(\NimVor\Database\Connection::class);
    }
}
