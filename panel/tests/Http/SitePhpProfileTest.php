<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Repositories\UserRepository;
use WhaCP\Services\SettingsService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** PHP security profiles for websites (items 33–35). */
final class SitePhpProfileTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->adminId = $this->seedUser('admin', 'admin');
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.3', 'fpm_binary' => '/x']]]);
        $this->agent->succeed('sites.apply', ['domain' => '', 'changed' => [], 'user' => ['name' => 'x', 'uid' => 1, 'gid' => 1]]);
    }

    private int $adminId;

    private function create(array $input): array
    {
        $users = $this->app->container->get(UserRepository::class);
        $admin = (array) $users->find($this->adminId);
        $this->app->container->get(WebsiteService::class)->create($input, $admin);
        return $this->lastCall('sites.apply');
    }

    public function testNewPhpSiteDefaultsToRecommended(): void
    {
        $spec = $this->create(['domain' => 'rec.test', 'kind' => 'php', 'php_version' => '8.3']);
        self::assertSame('recommended', $spec['php']['profile'] ?? null);
    }

    public function testDefaultProfileSettingIsHonoured(): void
    {
        $this->app->container->get(SettingsService::class)->update(['default_php_security_profile' => 'balanced'], null);
        $spec = $this->create(['domain' => 'bal.test', 'kind' => 'php', 'php_version' => '8.3']);
        self::assertSame('balanced', $spec['php']['profile'] ?? null);
    }

    public function testCustomProfileListIsCleaned(): void
    {
        $spec = $this->create(['domain' => 'cust.test', 'kind' => 'php', 'php_version' => '8.3', 'php_profile' => 'custom', 'disable_functions' => 'System, exec, exec, $(id), 123bad, a-b, shell_exec']);
        self::assertSame('custom', $spec['php']['profile'] ?? null);
        // Lowercased, de-duplicated, junk dropped (order preserved by insertion).
        self::assertSame('system,exec,shell_exec', $spec['php']['disable_functions'] ?? null);
    }

    public function testInvalidProfileIsRejected(): void
    {
        $this->expectException(\WhaCP\Support\ValidationException::class);
        $this->create(['domain' => 'bad.test', 'kind' => 'php', 'php_version' => '8.3', 'php_profile' => 'wide-open']);
    }
}
