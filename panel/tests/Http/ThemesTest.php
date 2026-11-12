<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;
use NimVor\View\SiteMenu;
use NimVor\View\Themes;

/** The customer panel's themes: choosing one, the administrator's controls, and that every theme keeps every tool. */
final class ThemesTest extends HttpTestCase
{
    private int $custId;
    private int $siteId;
    private int $secondId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->custId = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $sites = $this->app->container->get(WebsiteService::class);
        $admin = ['role_scope' => 'all', 'id' => 1];
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3', 'owner_id' => (string) $this->custId], $admin);
        $this->secondId = $sites->create(['domain' => 'blog.test', 'kind' => 'php', 'php_version' => '8.3', 'owner_id' => (string) $this->custId], $admin);
        $sites->create(['domain' => 'else.test', 'kind' => 'php', 'php_version' => '8.3', 'owner_id' => (string) $other], $admin);
        $this->db = $this->app->container->get(Connection::class);
    }

    private function switchTo(string $user): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    /** A new request: the panel is built again on the same database and session, as every real request is. */
    private function page(string $path): string
    {
        $this->app = $this->makeApp();
        return $this->request('GET', $path)->body();
    }

    /** @return array<string, string> */
    private function token(): array
    {
        return ['_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()];
    }

    /** @return array<string, list<string>> */
    public static function themes(): array
    {
        $out = [];
        foreach (array_keys(Themes::all()) as $key) {
            if ($key !== Themes::DEFAULT) {
                $out[$key] = [$key];
            }
        }
        return $out;
    }

    private function choose(string $theme): void
    {
        $res = $this->post('/appearance', $this->token() + ['theme' => $theme]);
        self::assertSame('/appearance', $res->header('location'), 'saved');
    }

    public function testNewCustomersGetNimVorModern(): void
    {
        $this->switchTo('cust');
        $page = $this->page('/websites');
        self::assertStringContainsString('data-nim-theme="modern"', $page);
        self::assertStringContainsString('nim-brandbar', $page, 'the existing customer layout is untouched');
        self::assertStringNotContainsString('nt-body', $page);
        self::assertStringNotContainsString('assets/css/themes/', $page, 'no theme stylesheet is loaded for the default theme');
        self::assertStringContainsString('href="/appearance"', $page, 'the account menu leads to Appearance');
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('themes')]
    public function testEachThemeRendersTheHomeAndEveryToolOfTheWebsite(string $theme): void
    {
        $this->switchTo('cust');
        $this->choose($theme);
        $home = $this->page('/websites');
        self::assertStringContainsString('data-nim-theme="' . $theme . '"', $home);
        self::assertStringContainsString('nt-' . $theme, $home);
        self::assertStringContainsString('assets/css/themes/' . $theme . '.css', $home);
        self::assertStringContainsString('assets/css/themes/base.css', $home);
        self::assertStringNotContainsString('assets/css/themes/' . ($theme === 'classic' ? 'compact' : 'classic') . '.css', $home, 'only the chosen theme is loaded');
        self::assertStringContainsString('shop.test', $home);
        self::assertStringContainsString('blog.test', $home);
        self::assertStringNotContainsString('else.test', $home, 'another customer\'s website is never shown');

        // A website's own page: all of its tools are reachable from the theme's navigation.
        $page = $this->page('/sites/' . $this->siteId);
        self::assertStringContainsString('nt-' . $theme, $page);
        $view = $this->app->container->get(\NimVor\View\View::class);
        self::assertStringContainsString('/sites/' . $this->siteId . '/files', $page);
        $marker = ['classic' => 'ncl-group', 'compact' => 'nco-facts'][$theme] ?? null;
        if ($marker !== null) {
            self::assertStringContainsString($marker, $page, 'the website page has the theme\'s own arrangement, not the shared one');
            self::assertStringNotContainsString('nim-site-hero', $page);
        }
        foreach (['files', 'databases', 'dns', 'ssl', 'backups', 'git', 'cron', 'logs', 'php', 'terminal', 'redirects'] as $tool) {
            self::assertTrue(
                str_contains($page, 'href="/sites/' . $this->siteId . '/' . $tool . '"'),
                $theme . ' hides the ' . $tool . ' tool'
            );
        }
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('themes')]
    public function testEveryToolOpensInEveryTheme(string $theme): void
    {
        $this->switchTo('cust');
        $this->choose($theme);
        $this->app = $this->makeApp();
        $view = $this->app->container->get(\NimVor\View\View::class);
        $page = $this->request('GET', '/sites/' . $this->siteId);
        self::assertSame(200, $page->status());
        // Every link of the website's menu, as the theme shows it.
        preg_match_all('#href="(/sites/' . $this->siteId . '/[a-z0-9_/-]+)"#', $page->body(), $m);
        $urls = array_values(array_unique($m[1]));
        self::assertGreaterThan(15, count($urls), 'the website\'s tools are listed');
        foreach ($urls as $url) {
            $res = $this->request('GET', $url);
            self::assertContains($res->status(), [200, 302], $theme . ' ' . $url . ' answered ' . $res->status());
            if ($res->status() === 200) {
                self::assertStringContainsString('data-nim-theme="' . $theme . '"', $res->body(), $theme . ' ' . $url);
            }
        }
    }

    public function testTheChoiceSurvivesSigningOutAndIn(): void
    {
        $this->switchTo('cust');
        $this->choose('classic');
        $this->switchTo('cust');
        self::assertStringContainsString('data-nim-theme="classic"', $this->page('/websites'));
        self::assertSame('classic', $this->db->scalar('SELECT ui_theme FROM users WHERE id = ?', [$this->custId]));
    }

    public function testSwitchingThemesChangesNoWebsiteData(): void
    {
        $this->switchTo('cust');
        $before = $this->db->fetchAll('SELECT * FROM websites ORDER BY id');
        $calls = array_sum(array_map(fn (string $a): int => $this->calls($a), ['sites.apply', 'nginx.apply', 'website.create']));
        foreach (array_keys(self::themes()) as $theme) {
            $this->choose($theme);
            $this->request('GET', '/websites');
        }
        $this->choose('modern');
        self::assertSame($before, $this->db->fetchAll('SELECT * FROM websites ORDER BY id'));
        self::assertSame($calls, array_sum(array_map(fn (string $a): int => $this->calls($a), ['sites.apply', 'nginx.apply', 'website.create'])), 'the agent was not asked to change anything');
    }

    public function testAnUnknownOrDisabledThemeIsRefusedAndFallsBackToTheDefault(): void
    {
        $this->switchTo('cust');
        $res = $this->post('/appearance', $this->token() + ['theme' => 'nonsense']);
        self::assertSame('/appearance', $res->header('location'));
        self::assertSame('', $this->db->scalar('SELECT ui_theme FROM users WHERE id = ?', [$this->custId]));

        $this->choose('classic');
        $this->app->container->get(SettingsService::class)->update(['theme_enabled_classic' => false], null);
        self::assertStringContainsString('data-nim-theme="modern"', $this->page('/websites'), 'a theme that was switched off is no longer shown');
        $this->post('/appearance', $this->token() + ['theme' => 'classic']);
        self::assertStringContainsString('data-nim-theme="modern"', $this->page('/websites'));
    }

    public function testTheAdministratorSetsTheDefaultAndMayLockTheChoice(): void
    {
        $this->switchTo('admin');
        $res = $this->post('/settings/themes', $this->token() + ['theme_default' => 'classic', 'theme_customers_choose' => '0']);
        self::assertSame('/settings/themes', $res->header('location'));
        $this->switchTo('cust');
        self::assertStringContainsString('data-nim-theme="classic"', $this->page('/websites'), 'the default applies to customers who never chose');
        $this->post('/appearance', $this->token() + ['theme' => 'modern']);
        self::assertStringContainsString('data-nim-theme="classic"', $this->page('/websites'), 'with choice off the default always applies');
        $res = $this->post('/appearance/preview', $this->token() + ['theme' => 'modern']);
        self::assertSame('/appearance', $res->header('location'), 'no previews either');
    }

    public function testTheDefaultMustBeAnEnabledTheme(): void
    {
        $this->switchTo('admin');
        $res = $this->post('/settings/themes', $this->token() + ['theme_default' => 'classic', 'theme_enabled_classic' => '0']);
        self::assertSame('/settings/themes', $res->header('location'));
        self::assertSame('modern', $this->app->container->get(SettingsService::class)->string('theme_default'));
    }

    public function testOnlyAdministratorsOpenTheThemeSettings(): void
    {
        $this->switchTo('cust');
        self::assertSame(403, $this->request('GET', '/settings/themes')->status());
        self::assertSame(403, $this->post('/settings/themes', $this->token() + ['theme_default' => 'classic'])->status());
        $this->switchTo('admin');
        $page = $this->request('GET', '/settings/themes');
        self::assertSame(200, $page->status());
        foreach (Themes::all() as $t) {
            self::assertStringContainsString($t['label'], $page->body());
        }
        self::assertStringContainsString('/settings/themes', $this->request('GET', '/settings')->body(), 'linked from the settings page');
    }

    public function testAPreviewChangesNothingUntilItIsKept(): void
    {
        $this->switchTo('cust');
        $res = $this->post('/appearance/preview', $this->token() + ['theme' => 'classic']);
        self::assertSame('/websites', $res->header('location'));
        $page = $this->page('/websites');
        self::assertStringContainsString('data-nim-theme="classic"', $page);
        self::assertStringContainsString('Previewing the', $page);
        self::assertSame('', $this->db->scalar('SELECT ui_theme FROM users WHERE id = ?', [$this->custId]), 'nothing saved');
        $this->post('/appearance/preview/cancel', $this->token());
        self::assertStringContainsString('data-nim-theme="modern"', $this->page('/websites'));
        $this->post('/appearance/preview', $this->token() + ['theme' => 'classic']);
        $this->post('/appearance', $this->token() + ['theme' => 'classic']);
        self::assertSame('classic', $this->db->scalar('SELECT ui_theme FROM users WHERE id = ?', [$this->custId]));
        self::assertStringNotContainsString('Previewing the', $this->page('/websites'));
    }

    public function testLightDarkAndAccentArePerUserAndRestorable(): void
    {
        $this->switchTo('cust');
        $this->post('/appearance', $this->token() + ['mode' => 'dark', 'accent' => 'violet']);
        $page = $this->page('/websites');
        self::assertStringContainsString('data-nim-mode="dark"', $page);
        self::assertStringContainsString('data-nim-accent="violet"', $page);
        self::assertStringContainsString('assets/css/themes/accents.css', $page);
        $bad = $this->post('/appearance', $this->token() + ['accent' => '"><script>', 'mode' => 'purple']);
        self::assertSame('/appearance', $bad->header('location'));
        self::assertSame('violet', $this->db->scalar('SELECT ui_accent FROM users WHERE id = ?', [$this->custId]), 'a refused change leaves the choice as it was');
        $this->post('/appearance/reset', $this->token());
        $page = $this->page('/websites');
        self::assertStringNotContainsString('data-nim-accent', $page);
        self::assertStringContainsString('data-nim-mode="auto"', $page);
    }

    public function testAnAdministratorMayTurnAnAccentOff(): void
    {
        $this->app->container->get(SettingsService::class)->update(['theme_accent_rose' => false], null);
        $this->switchTo('cust');
        $this->post('/appearance', $this->token() + ['accent' => 'rose']);
        self::assertSame('', $this->db->scalar('SELECT ui_accent FROM users WHERE id = ?', [$this->custId]));
    }

    public function testAdministratorsKeepTheirOwnLayoutWhateverTheDefaultIs(): void
    {
        $this->app->container->get(SettingsService::class)->update(['theme_default' => 'classic'], null);
        $this->switchTo('admin');
        $page = $this->page('/dashboard');
        self::assertStringContainsString('nim-sidebar', $page);
        self::assertStringNotContainsString('nt-body', $page);
        self::assertSame(200, $this->request('GET', '/appearance')->status());
    }

    public function testThemeAssetsAndTemplatesAllExist(): void
    {
        $root = dirname(__DIR__, 2);
        foreach (array_keys(Themes::all()) as $key) {
            if ($key === Themes::DEFAULT) {
                continue;
            }
            foreach (['resources/views/themes/' . $key . '/layout.php', 'public/assets/css/themes/' . $key . '.css'] as $file) {
                self::assertFileExists($root . '/' . $file, $key);
            }
        }
        self::assertFileExists($root . '/public/assets/css/themes/base.css');
        self::assertFileExists($root . '/public/assets/css/themes/accents.css');
        self::assertFileExists($root . '/public/assets/js/themes/common.js');
    }
}
