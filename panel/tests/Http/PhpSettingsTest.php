<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Tests\Support\HttpTestCase;

/** PHP → version → Settings: extensions, php.ini and PHP-FPM. */
final class PhpSettingsTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->agent->succeed('php.extensions', [
            ['name' => 'redis', 'description' => 'Redis client', 'package' => 'php8.3-redis', 'installed' => false, 'enabled' => false],
            ['name' => 'intl', 'description' => 'Internationalisation', 'package' => 'php8.3-intl', 'installed' => true, 'version' => '8.3.6', 'enabled' => true],
            ['name' => 'mbstring', 'description' => 'Multibyte strings', 'package' => 'php8.3-mbstring', 'installed' => true, 'enabled' => true, 'protected' => 'NimVor\'s panel runs on PHP 8.3 and needs mbstring.'],
        ]);
        $this->agent->succeed('php.ini_get', [
            'file' => '/etc/php/8.3/fpm/conf.d/99-nimvor.ini', 'values' => ['memory_limit' => '512M', 'display_errors' => 'Off'],
            'effective' => ['memory_limit' => '512M', 'max_input_vars' => '1000'],
            'directives' => [
                ['key' => 'memory_limit', 'kind' => 'size', 'description' => 'Memory', 'per_website' => true],
                ['key' => 'max_input_vars', 'kind' => 'int', 'description' => 'Input variables', 'min' => 100, 'max' => 1000000],
                ['key' => 'display_errors', 'kind' => 'bool', 'description' => 'Show errors'],
                // The agent omits a zero min (and other empty fields).
                ['key' => 'max_execution_time', 'kind' => 'int', 'description' => 'Seconds', 'max' => 86400],
            ],
        ]);
        $this->agent->succeed('php.fpm_get', ['file' => '/etc/php/8.3/fpm/pool.d/nimvor-global.conf', 'settings' => ['process_max' => 64, 'log_level' => 'warning'], 'log_levels' => ['alert', 'error', 'warning', 'notice', 'debug']]);
    }

    public function testTabsShowTheAgentsState(): void
    {
        $page = $this->request('GET', '/php/8.3')->body();
        self::assertStringContainsString('php8.3-redis', $page);
        self::assertStringContainsString('/php/8.3/extensions/install', $page);
        self::assertStringContainsString('Switch off', $page, 'intl can be switched off');
        self::assertStringNotContainsString('action="/php/8.3/extensions/remove" data-nim-busy data-nim-confirm="Remove mbstring', $page, 'protected: no Remove');

        $ini = $this->request('GET', '/php/8.3', [], [], '', ['tab' => 'ini'])->body();
        self::assertStringContainsString('name="ini[memory_limit]" value="512M"', $ini);
        self::assertStringContainsString('per website', $ini);
        self::assertStringContainsString('placeholder="0–86400"', $ini);
        self::assertStringContainsString("memory_limit = 512M\ndisplay_errors = Off\n", $ini, 'text editor holds the saved settings');

        $fpm = $this->request('GET', '/php/8.3', [], [], '', ['tab' => 'fpm'])->body();
        self::assertStringContainsString('name="process_max" value="64"', $fpm);
        self::assertStringContainsString('<option value="warning" selected>', $fpm);
    }

    public function testPhpVersionUpdateAndIndexRefresh(): void
    {
        $this->agent->succeed('php.update', ['job_id' => '5555555555555555', 'packages' => ['php8.3-fpm']]);
        $this->post('/php/8.3/update', []);
        self::assertSame(['version' => '8.3'], $this->lastCall('php.update'));
        self::assertSame('5555555555555555', $this->flash('job'));
        $this->post('/php/8.3x/update', []);
        self::assertSame(1, $this->calls('php.update'), 'a bad version reached the agent');

        $this->agent->succeed('packages.refresh', ['job_id' => '6666666666666666']);
        $this->post('/php/refresh', []);
        self::assertSame(1, $this->calls('packages.refresh'));
    }

    public function testIonCubeLoaderUsesItsOwnAgentActions(): void
    {
        $this->agent->succeed('php.ioncube_install', ['job_id' => '3333333333333333', 'package' => 'ionCube Loader']);
        $this->post('/php/8.3/extensions/install', ['name' => 'ioncube']);
        self::assertSame(['version' => '8.3', 'name' => 'ioncube'], $this->lastCall('php.ioncube_install'));
        self::assertSame('3333333333333333', $this->flash('job'));
        self::assertSame(0, $this->calls('php.extension_install'), 'not a package install');

        $this->agent->succeed('php.ioncube_remove', ['job_id' => '4444444444444444']);
        $this->post('/php/8.3/extensions/remove', ['name' => 'ioncube']);
        self::assertSame(['version' => '8.3', 'name' => 'ioncube'], $this->lastCall('php.ioncube_remove'));

        // Switching it on or off is the ordinary extension switch.
        $this->agent->succeed('php.extension_set', ['name' => 'ioncube', 'enabled' => false]);
        $this->post('/php/8.3/extensions/toggle', ['name' => 'ioncube', 'enabled' => '0']);
        self::assertSame(['version' => '8.3', 'name' => 'ioncube', 'enabled' => false], $this->lastCall('php.extension_set'));
    }

    public function testIonCubeShowsInstallAndRemoveButtons(): void
    {
        $row = ['name' => 'ioncube', 'description' => 'ionCube Loader (runs ionCube-encoded PHP files)', 'package' => '', 'vendor' => true, 'installed' => false, 'enabled' => false];
        $this->agent->succeed('php.extensions', [$row]);
        $this->agent->succeed('php.ini_get', ['file' => '', 'directives' => []]);
        $this->agent->succeed('php.fpm_get', []);
        $page = $this->request('GET', '/php/8.3')->body();
        self::assertStringContainsString('downloaded from ionCube', $page);
        self::assertStringContainsString('/php/8.3/extensions/install', $page);

        $this->agent->succeed('php.extensions', [array_merge($row, ['installed' => true, 'enabled' => true])]);
        $page = $this->request('GET', '/php/8.3')->body();
        self::assertStringContainsString('/php/8.3/extensions/remove', $page);
        self::assertStringContainsString('Switch off', $page);
    }

    public function testExtensionActions(): void
    {
        $this->agent->succeed('php.extension_install', ['job_id' => '1111111111111111', 'package' => 'php8.3-redis']);
        $this->post('/php/8.3/extensions/install', ['name' => 'redis']);
        self::assertSame(['version' => '8.3', 'name' => 'redis'], $this->lastCall('php.extension_install'));
        self::assertSame('1111111111111111', $this->flash('job'));

        $this->agent->succeed('php.extension_set', ['name' => 'intl', 'enabled' => false]);
        $this->post('/php/8.3/extensions/toggle', ['name' => 'intl', 'enabled' => '0']);
        self::assertSame(['version' => '8.3', 'name' => 'intl', 'enabled' => false], $this->lastCall('php.extension_set'));

        $this->agent->succeed('php.extension_remove', ['job_id' => '2222222222222222']);
        $this->post('/php/8.3/extensions/remove', ['name' => 'intl']);
        self::assertSame(['version' => '8.3', 'name' => 'intl'], $this->lastCall('php.extension_remove'));

        // Unknown operations, names and versions never reach the agent.
        $before = $this->calls('php.extension_install') + $this->calls('php.extension_remove') + $this->calls('php.extension_set');
        foreach (['/php/8.3/extensions/purge' => 'redis', '/php/8.3/extensions/install' => 'red is', '/php/8.3x/extensions/install' => 'redis'] as $path => $name) {
            $this->post($path, ['name' => $name]);
        }
        self::assertSame($before, $this->calls('php.extension_install') + $this->calls('php.extension_remove') + $this->calls('php.extension_set'));
    }

    public function testIniFormAndEditor(): void
    {
        $this->agent->succeed('php.ini_set', ['values' => []]);
        $this->post('/php/8.3/ini', ['mode' => 'form', 'ini' => ['memory_limit' => ' 1G ', 'max_input_vars' => '', 'display_errors' => 'Off', 'bad key' => 'x']]);
        self::assertSame(['version' => '8.3', 'content' => "memory_limit = 1G\ndisplay_errors = Off"], $this->lastCall('php.ini_set'));

        $this->post('/php/8.3/ini', ['mode' => 'text', 'content' => "; mine\nmax_input_vars = 5000\n"]);
        self::assertSame("; mine\nmax_input_vars = 5000\n", $this->lastCall('php.ini_set')['content']);

        // The agent's refusal is shown, nothing else happens.
        $this->agent->fail('php.ini_set', 400, 'invalid_params', 'line 1: extension cannot be set here');
        $res = $this->post('/php/8.3/ini', ['mode' => 'text', 'content' => 'extension=/tmp/x.so']);
        self::assertSame('/php/8.3?tab=ini', $res->header('Location'));
    }

    public function testFpmSettings(): void
    {
        $this->agent->succeed('php.fpm_set', ['settings' => []]);
        $this->post('/php/8.3/fpm', ['emergency_restart_threshold' => '10', 'emergency_restart_interval_seconds' => '60', 'process_control_timeout_seconds' => '', 'process_max' => '128', 'log_level' => 'warning']);
        self::assertSame(['version' => '8.3', 'log_level' => 'warning', 'emergency_restart_threshold' => 10, 'emergency_restart_interval_seconds' => 60, 'process_control_timeout_seconds' => 0, 'process_max' => 128], $this->lastCall('php.fpm_set'));
    }

    public function testCustomersCannotChangePhp(): void
    {
        $this->seedUser('cust', 'customer');
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/php/8.3')->status());
        foreach (['/php/8.3/extensions/install', '/php/8.3/ini', '/php/8.3/fpm'] as $p) {
            self::assertSame(403, $this->post($p, ['name' => 'redis'])->status(), $p);
        }
        self::assertSame(0, $this->calls('php.extension_install') + $this->calls('php.ini_set') + $this->calls('php.fpm_set'));
    }
}
