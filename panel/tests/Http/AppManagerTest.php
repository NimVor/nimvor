<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\AppCatalogService;
use WhaCP\Services\WebsiteService;
use WhaCP\Support\UserFacingException;
use WhaCP\Tests\Support\HttpTestCase;

/** Administration → Application Manager, and catalog enforcement on install. */
final class AppManagerTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
    }

    public function testManagerTogglesCategoriesAppsAndInstaller(): void
    {
        $this->login('admin');
        $db = $this->app->container->get(Connection::class);
        $page = $this->request('GET', '/apps-manager')->body();
        foreach (['Application Manager', 'Blogs &amp; CMS', 'WordPress', 'Visible to customers'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }

        // Hide WordPress from customers, disable the forums category, turn the
        // whole installer off and on.
        $this->post('/apps-manager/app', ['slug' => 'wordpress', 'field' => 'customer_visible', 'on' => '0']);
        self::assertSame(0, (int) $db->scalar("SELECT customer_visible FROM app_catalog WHERE slug = 'wordpress'"));
        $this->post('/apps-manager/category', ['slug' => 'forums', 'on' => '0']);
        self::assertSame(0, (int) $db->scalar("SELECT enabled FROM app_categories WHERE slug = 'forums'"));
        $this->post('/apps-manager/installer', ['on' => '0']);

        $catalog = $this->app->container->get(AppCatalogService::class);
        self::assertFalse($catalog->installerEnabled());
        self::assertSame([], $catalog->forCustomer(), 'a disabled installer shows nothing');
    }

    public function testCustomersCannotReachManager(): void
    {
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/apps-manager')->status());
        self::assertSame(403, $this->post('/apps-manager/installer', ['on' => '0'])->status());
    }

    public function testEnforcementBlocksHiddenAndDisabledApps(): void
    {
        $admin = (array) $this->app->container->get(UserRepository::class)->find($this->seedUser('owner', 'customer'));
        $this->login('owner');
        $site = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], $admin);
        $siteRow = (array) $this->app->container->get(Connection::class)->fetch('SELECT * FROM websites WHERE id = ?', [$site]);
        $apps = $this->app->container->get(\WhaCP\Services\AppInstallerService::class);
        $catalog = $this->app->container->get(AppCatalogService::class);

        // Hidden-from-customers app: a customer cannot install it.
        $catalog->setAppFlag('joomla', 'customer_visible', false);
        try {
            $apps->install($siteRow, 'joomla', [], $admin);
            self::fail('a hidden app must not be installable by a customer');
        } catch (UserFacingException) {
        }

        // Disabled app: nobody can install it.
        $catalog->setAppFlag('drupal', 'enabled', false);
        self::assertFalse($catalog->installableBy('drupal', $admin));
    }
}
