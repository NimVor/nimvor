<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Services\SettingsService;
use NimVor\Tests\Support\HttpTestCase;

/** PHP Manager: versions and extensions, review, apply, saved profile. */
final class PhpManagerTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('oper', 'operator');
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.3', 'fpm_binary' => '/usr/sbin/php-fpm8.3'], ['version' => '8.1', 'cli_binary' => '/usr/bin/php8.1']], 'pools' => []]);
        $this->agent->succeed('php.available', ['8.2', '8.3', '8.4']);
        $this->agent->succeed('php.catalog', ['extensions' => [['name' => 'curl', 'description' => 'HTTP client'], ['name' => 'redis', 'description' => 'Redis client'], ['name' => 'xdebug', 'description' => 'Debugger']], 'defaults' => ['curl']]);
        $this->agent->succeed('php.extensions', [['name' => 'curl', 'installed' => true, 'enabled' => true], ['name' => 'redis', 'installed' => false, 'enabled' => false], ['name' => 'xdebug', 'installed' => true, 'enabled' => false]]);
        $this->agent->succeed('php.profile_plan', ['changes' => true, 'removals' => true, 'items' => [
            ['action' => 'install', 'version' => '8.4', 'packages' => ['php8.4-fpm', 'php8.4-cli', 'php8.4-curl'], 'detail' => 'PHP 8.4 with PHP-FPM and 1 extension(s)'],
            ['action' => 'unchanged', 'version' => '8.3', 'detail' => 'Installed'],
            ['action' => 'remove', 'version' => '8.3', 'extension' => 'xdebug', 'packages' => ['php8.3-xdebug']],
            ['action' => 'blocked', 'version' => '8.0', 'detail' => 'Not offered <b>here</b>'],
        ]]);
        $this->agent->succeed('php.profile_apply', ['job_id' => '9999888877776666']);
    }

    public function testSelectionDefaultsToWhatTheServerHas(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/php/manager')->body();
        // Only versions with PHP-FPM count as installed; 8.2/8.4 can be installed.
        self::assertMatchesRegularExpression('#id="pv-8.3" aria-label="PHP 8.3" checked#', $page);
        self::assertDoesNotMatchRegularExpression('#id="pv-8.4"[^>]*checked#', $page);
        self::assertMatchesRegularExpression('#id="px-curl" aria-label="curl" checked#', $page);
        self::assertDoesNotMatchRegularExpression('#id="px-xdebug"[^>]*checked#', $page, 'installed but switched off');
        self::assertStringContainsString('Until 2027-12-31', $page);
    }

    public function testReviewThenApplyKeepsTheProfile(): void
    {
        $this->login('admin');
        $page = $this->request('POST', '/php/manager/review', ['versions' => ['8.3', '8.4', '8.3; rm'], 'extensions' => ['curl', 'Evil!'], '_token' => $this->csrf()])->body();
        self::assertSame(['versions' => ['8.3', '8.4'], 'extensions' => ['curl']], $this->lastCall('php.profile_plan'));
        foreach (['To install (1)', 'To remove (1)', 'Kept or not possible (1)', 'php8.4-fpm php8.4-cli php8.4-curl', 'name="confirm_removals"', 'Not offered &lt;b&gt;here&lt;/b&gt;'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post('/php/manager/apply', ['versions' => ['8.3', '8.4'], 'extensions' => ['curl'], 'confirm_removals' => '1']);
        self::assertSame(['versions' => ['8.3', '8.4'], 'extensions' => ['curl'], 'confirm_removals' => true], $this->lastCall('php.profile_apply'));
        self::assertSame('9999888877776666', $this->flash('job'));
        $saved = json_decode($this->app->container->get(SettingsService::class)->string('php_profile'), true);
        self::assertSame(['8.3', '8.4'], $saved['versions']);
        self::assertSame('9999888877776666', $saved['job_id']);
        // The saved profile is now the selection, and can be exported.
        self::assertMatchesRegularExpression('#id="pv-8.4" aria-label="PHP 8.4" checked#', $this->request('GET', '/php/manager')->body());
        $export = $this->request('GET', '/php/manager/export');
        self::assertSame(['format' => 'nimvor-php-profile', 'version' => 1, 'versions' => ['8.3', '8.4'], 'extensions' => ['curl']], json_decode($export->body(), true));
        self::assertStringContainsString('attachment', (string) $export->header('Content-Disposition'));
    }

    public function testNoVersionIsRefused(): void
    {
        $this->login('admin');
        $this->post('/php/manager/review', ['versions' => [], 'extensions' => ['curl']]);
        self::assertStringContainsString('at least one PHP version', (string) json_encode($this->flash('error')));
        self::assertSame(0, $this->calls('php.profile_plan'));
    }

    public function testOperatorsCannotUseIt(): void
    {
        $this->login('oper');
        self::assertContains($this->request('GET', '/php/manager')->status(), [302, 403]);
        $this->post('/php/manager/apply', ['versions' => ['8.3'], 'extensions' => [], 'confirm_removals' => '1']);
        self::assertSame(0, $this->calls('php.profile_apply'));
    }

    private function csrf(): string
    {
        return $this->app->container->get(\NimVor\Security\Csrf::class)->token();
    }
}
