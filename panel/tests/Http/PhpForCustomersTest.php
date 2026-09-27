<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

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
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testCustomersChangeOnlyTheirOwnPhpSettings(): void
    {
        self::assertSame([7, true], [$this->settings()['max_children'], $this->settings()['harden']]);
        $this->login('cust');
        $form = $this->request('GET', '/sites/' . $this->siteId . '/settings')->body();
        self::assertStringContainsString('name="memory_limit_mb"', $form);
        foreach (['name="max_children"', 'name="harden"', 'Advanced PHP-FPM', 'name="pm"'] as $hidden) {
            self::assertStringNotContainsString($hidden, $form, $hidden);
        }
        $new = $this->request('GET', '/websites/new')->body();
        self::assertStringContainsString('name="php_version"', $new, 'the version is chosen');
        self::assertStringNotContainsString('name="memory_limit_mb"', $new, 'no PHP settings card when creating');

        // Sent anyway: ignored; their own settings are applied.
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->post('/sites/' . $this->siteId . '/settings', ['kind' => 'php', 'php_version' => '8.3', 'memory_limit_mb' => '512', 'max_children' => '150', 'harden' => '0', 'pm' => 'static', 'max_requests' => '1']);
        $s = $this->settings();
        self::assertSame(512, $s['memory_limit_mb']);
        self::assertSame([7, true, 'ondemand', 500], [$s['max_children'], $s['harden'], $s['pm'], $s['max_requests']]);
        $spec = $this->lastCall('sites.apply');
        self::assertSame([7, true], [$spec['php']['max_children'], $spec['php']['harden']]);

        // Administrators still set them.
        $this->switchTo('admin');
        self::assertStringContainsString('name="max_children"', $this->request('GET', '/sites/' . $this->siteId . '/settings')->body());
        $this->post('/sites/' . $this->siteId . '/settings', ['kind' => 'php', 'php_version' => '8.3', 'max_children' => '12', 'harden' => '0']);
        self::assertSame([12, false], [$this->settings()['max_children'], $this->settings()['harden']]);
    }
}
