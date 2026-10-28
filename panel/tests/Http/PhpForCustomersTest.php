<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/**
 * PHP settings of hosting customers and resellers: the process limits,
 * hardening and PHP-FPM tuning stay the administrator's, in the form and
 * in what the server accepts.
 */
final class PhpForCustomersTest extends HttpTestCase
{
    private int $siteId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3', 'max_children' => '7', 'harden' => '1'], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($cust));
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id = ?', [$cust, $this->siteId]);
    }

    /** @return array<string, mixed> */
    private function settings(): array
    {
        return (array) json_decode((string) $this->db->scalar('SELECT php_settings FROM websites WHERE id = ?', [$this->siteId]), true);
    }

    private function switchTo(string $user): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testCustomersChangeOnlyTheirOwnPhpSettings(): void
    {
        self::assertSame([7, true], [$this->settings()['max_children'], $this->settings()['harden']]);
        $this->login('cust');
        $form = $this->request('GET', '/sites/' . $this->siteId . '/php')->body();
        self::assertStringContainsString('name="memory_limit_mb"', $form);
        foreach (['name="max_children"', 'name="harden"', 'Advanced PHP-FPM', 'name="pm"'] as $hidden) {
            self::assertStringNotContainsString($hidden, $form, $hidden);
        }
        $new = $this->request('GET', '/websites/new')->body();
        self::assertStringContainsString('name="php_version"', $new, 'the version is chosen');
        self::assertStringNotContainsString('name="memory_limit_mb"', $new, 'no PHP settings card when creating');

        // Sent anyway: ignored; their own settings are applied.
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->post('/sites/' . $this->siteId . '/php', ['kind' => 'php', 'php_version' => '8.3', 'memory_limit_mb' => '512', 'max_children' => '150', 'harden' => '0', 'pm' => 'static', 'max_requests' => '1']);
        $s = $this->settings();
        self::assertSame(512, $s['memory_limit_mb']);
        self::assertSame([7, true, 'ondemand', 500], [$s['max_children'], $s['harden'], $s['pm'], $s['max_requests']]);
        $spec = $this->lastCall('sites.apply');
        self::assertSame([7, true], [$spec['php']['max_children'], $spec['php']['harden']]);

        // Administrators still set them.
        $this->switchTo('admin');
        self::assertStringContainsString('name="max_children"', $this->request('GET', '/sites/' . $this->siteId . '/php')->body());
        $this->post('/sites/' . $this->siteId . '/php', ['kind' => 'php', 'php_version' => '8.3', 'max_children' => '12', 'harden' => '0']);
        self::assertSame([12, false], [$this->settings()['max_children'], $this->settings()['harden']]);
    }

    public function testNewWebsitesFollowTheHardeningSetting(): void
    {
        $sites = $this->app->container->get(WebsiteService::class);
        $admin = ['role_scope' => 'all', 'id' => 1];
        $harden = fn (int $id): bool => (bool) json_decode((string) $this->db->scalar('SELECT php_settings FROM websites WHERE id = ?', [$id]), true)['harden'];
        self::assertFalse($harden($sites->create(['domain' => 'off.test', 'kind' => 'php', 'php_version' => '8.3'], $admin)));

        $this->app->container->get(\NimVor\Services\SettingsService::class)->update(['php_harden_new_sites' => '1'], 1);
        $this->login('admin');
        // The new-website form offers the PHP security profile, defaulting to
        // Recommended (the hardening control is now the profile selector).
        self::assertMatchesRegularExpression('/name="php_profile"[\s\S]*?<option value="recommended" selected/', $this->request('GET', '/websites/new')->body());
        self::assertTrue($harden($sites->create(['domain' => 'on.test', 'kind' => 'php', 'php_version' => '8.3'], $admin)));
        // An administrator can still create one without it.
        self::assertFalse($harden($sites->create(['domain' => 'free.test', 'kind' => 'php', 'php_version' => '8.3', 'harden' => '0'], $admin)));
        // A hosting customer cannot opt out.
        $cust = (array) $this->app->container->get(UserRepository::class)->find((int) $this->db->scalar("SELECT id FROM users WHERE username = 'cust'"));
        self::assertTrue($harden($sites->create(['domain' => 'cust.test', 'kind' => 'php', 'php_version' => '8.3', 'harden' => '0'], $cust)));
    }

    public function testCustomersSetTheirWebsitesPhpDirectives(): void
    {
        $this->login('cust');
        $root = (string) $this->db->scalar('SELECT root FROM websites WHERE id = ?', [$this->siteId]);
        $form = $this->request('GET', '/sites/' . $this->siteId . '/php')->body();
        self::assertStringContainsString('name="ini[date.timezone]"', $form);
        self::assertStringNotContainsString('ini[open_basedir]', $form);
        self::assertStringNotContainsString('ini[allow_url_include]', $form);

        $this->agent->succeed('sites.apply', ['changed' => []]);
        $base = ['kind' => 'php', 'php_version' => '8.3'];
        $this->post('/sites/' . $this->siteId . '/php', $base + ['ini' => ['date.timezone' => 'Asia/Karachi', 'display_errors' => 'On', 'post_max_size' => '128m', 'session.save_path' => $root . '/tmp/sessions', 'error_reporting' => '']]);
        self::assertSame(['date.timezone' => 'Asia/Karachi', 'display_errors' => 'On', 'post_max_size' => '128M', 'session.save_path' => $root . '/tmp/sessions'], $this->lastCall('sites.apply')['php']['ini']);

        // Refused, and nothing is changed.
        $calls = $this->calls('sites.apply');
        foreach ([['open_basedir' => '/'], ['allow_url_include' => 'On'], ['session.save_path' => '/home/nimvor/other.test/tmp'], ['error_log' => $root . '/../x'], ['date.timezone' => "UTC\nphp_admin_value[open_basedir]=/"], ['display_errors' => 'maybe']] as $bad) {
            $this->post('/sites/' . $this->siteId . '/php', $base + ['ini' => $bad]);
        }
        self::assertSame($calls, $this->calls('sites.apply'));
        self::assertSame('Asia/Karachi', $this->settings()['ini']['date.timezone']);
        $this->post('/sites/' . $this->siteId . '/php', $base + ['ini' => ['display_errors' => 'maybe']]);
        self::assertStringContainsString('Use On or Off.', $this->request('GET', '/sites/' . $this->siteId . '/php')->body(), 'the error is shown by the field');

        // Emptied: back to the server default.
        $this->post('/sites/' . $this->siteId . '/php', $base + ['ini' => ['date.timezone' => '', 'display_errors' => '', 'post_max_size' => '', 'session.save_path' => '']]);
        self::assertSame([], $this->settings()['ini']);
        self::assertArrayNotHasKey('ini', $this->lastCall('sites.apply')['php']);
    }

    public function testThePanelAndTheAgentOfferTheSameDirectives(): void
    {
        preg_match_all('/^\t"([a-z_.]+)":\s+\{kind/m', (string) file_get_contents(__DIR__ . '/../../../agent/internal/sites/phpini.go'), $m);
        $agent = $m[1];
        sort($agent);
        $panel = array_keys(\NimVor\Services\PhpDirectives::ALL);
        sort($panel);
        self::assertNotEmpty($agent);
        self::assertSame($agent, $panel);
    }
}
