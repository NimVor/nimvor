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

    public function testTheWizardOpensInAWindowWithoutTheWholePage(): void
    {
        $res = $this->request('GET', '/websites/new/modal', [], ['x-requested-with' => 'fetch']);
        self::assertSame(200, $res->status());
        $html = $res->body();
        self::assertStringNotContainsString('<html', $html, 'only the window content, not a page');
        self::assertStringContainsString('class="modal-header"', $html);
        self::assertStringContainsString('data-nim-wizard', $html);
        self::assertStringContainsString('data-nim-site-modal-form', $html, 'sent in the background');
        self::assertStringContainsString('data-nim-modal-error', $html);
        foreach ([1, 2, 3, 4] as $n) {
            self::assertStringContainsString('data-nim-step="' . $n . '"', $html);
        }
        self::assertStringContainsString('name="domain"', $html);
        self::assertStringNotContainsString('data-nim-busy', $html, 'the window sends its own form');
    }

    public function testCreateWebsiteLinksOpenTheWindowAndStillWorkAsPageLinks(): void
    {
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('href="/websites/new" data-nim-site-modal data-nim-modal-href="/websites/new/modal"', $page);
        self::assertStringContainsString('id="nim-site-modal"', $page, 'the one shared window is on every page');
        self::assertSame(1, substr_count($page, 'id="nim-site-modal"'));
        // The websites list (it needs a discovery to draw) and the other themes use the same attributes on "Add site".
        $views = dirname(__DIR__, 2) . '/resources/views';
        foreach (['websites/index.php', 'themes/classic/pages/websites/index.php', 'themes/compact/pages/websites/index.php', 'themes/simple/pages/websites/index.php', 'themes/workspace/pages/websites/index.php'] as $file) {
            $src = (string) file_get_contents($views . '/' . $file);
            self::assertStringNotContainsString("href=\"<?= e(\$this->url('sites.create')) ?>\">", $src, $file . ' has a create link that still leaves the page');
            self::assertStringContainsString("data-nim-modal-href=\"<?= e(\$this->url('sites.create.modal')) ?>\"", $src, $file);
        }
    }

    public function testASubmitFromTheWindowIsAnsweredAsJson(): void
    {
        $json = ['accept' => 'application/json', 'x-requested-with' => 'fetch'];
        $bad = $this->request('POST', '/websites/new', ['domain' => 'not a domain', 'kind' => 'static', '_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()], $json);
        self::assertSame(422, $bad->status());
        $body = (array) json_decode($bad->body(), true);
        self::assertFalse($body['success']);
        self::assertArrayHasKey('domain', (array) $body['error']['errors'], 'the field is marked in the window');

        $ok = $this->request('POST', '/websites/new', ['domain' => 'inwin.test', 'kind' => 'static', '_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()], $json);
        self::assertSame(200, $ok->status(), $ok->body());
        $body = (array) json_decode($ok->body(), true);
        $id = (int) $this->db()->scalar("SELECT id FROM websites WHERE domain = 'inwin.test'");
        self::assertTrue($body['success']);
        self::assertSame('/sites/' . $id, $body['redirect']);
    }

    private function db(): \NimVor\Database\Connection
    {
        return $this->app->container->get(\NimVor\Database\Connection::class);
    }
}
