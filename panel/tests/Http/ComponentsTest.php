<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Tests\Support\HttpTestCase;

/** Server → Components: steps per component and their fixes. */
final class ComponentsTest extends HttpTestCase
{
    private const STATUS = [
        ['id' => 'web', 'name' => 'Web server', 'description' => 'Nginx', 'installed' => true, 'ready' => false, 'packages' => ['nginx'], 'units' => ['nginx.service'], 'steps' => [
            ['id' => 'installed', 'status' => 'ok'], ['id' => 'setup', 'status' => 'na'], ['id' => 'running', 'status' => 'ok'], ['id' => 'boot', 'status' => 'ok'],
            ['id' => 'ports', 'status' => 'todo', 'items' => ['80/tcp', '443/tcp'], 'detail' => 'Closed in the firewall: 80/tcp, 443/tcp'],
        ]],
        ['id' => 'mail', 'name' => 'Email', 'description' => 'Postfix', 'installed' => true, 'ready' => false, 'packages' => ['postfix', 'opendkim'], 'units' => ['postfix.service'], 'steps' => [
            ['id' => 'installed', 'status' => 'todo', 'items' => ['opendkim'], 'detail' => 'Not installed: opendkim'],
            ['id' => 'setup', 'status' => 'todo', 'detail' => 'Set up email on the Email page (Set up and start).'],
            ['id' => 'running', 'status' => 'ok'], ['id' => 'boot', 'status' => 'todo', 'items' => ['postfix.service'], 'detail' => 'Not started at boot: postfix.service'],
            ['id' => 'ports', 'status' => 'na'],
        ]],
        ['id' => 'dns', 'name' => 'DNS server', 'description' => 'BIND', 'installed' => false, 'ready' => false, 'packages' => ['bind9'], 'units' => [], 'steps' => [
            ['id' => 'installed', 'status' => 'todo', 'items' => ['bind9']], ['id' => 'setup', 'status' => 'na'], ['id' => 'running', 'status' => 'na'], ['id' => 'boot', 'status' => 'na'], ['id' => 'ports', 'status' => 'na'],
        ]],
    ];

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('oper', 'operator');
        $this->agent->succeed('components.status', self::STATUS);
        $this->agent->succeed('packages.install', ['job_id' => '1111222233334444']);
        $this->agent->succeed('services.control', ['active' => true]);
        $this->agent->succeed('firewall.allow_ports', ['changed' => true, 'pending' => ['id' => 'abcabcabcabc']]);
    }

    public function testPageShowsStepsAndFixes(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/components')->body();
        // Until chosen, the installed components are the expected ones.
        self::assertStringContainsString('0 of 2 components ready', $page);
        self::assertStringContainsString('Not used on this server (1)', $page);
        self::assertStringContainsString('Closed in the firewall: 80/tcp, 443/tcp', $page);
        self::assertStringContainsString('action="/components/web/ports"', $page);
        self::assertStringContainsString('action="/components/mail/install"', $page);
        self::assertStringContainsString('action="/components/mail/boot"', $page);
        self::assertStringContainsString('href="/mail">Set up</a>', $page);
        self::assertStringNotContainsString('action="/components/web/install"', $page, 'nothing to install for the web server');
    }

    public function testFixesUseTheAgentsItemsNotTheForm(): void
    {
        $this->login('admin');
        $this->post('/components/mail/install', ['names' => ['openssh-server']]);
        self::assertSame(['names' => ['opendkim']], $this->lastCall('packages.install'));
        $this->post('/components/mail/boot');
        self::assertSame(['unit' => 'postfix.service', 'op' => 'enable'], $this->lastCall('services.control'));
        $this->post('/components/web/ports');
        self::assertSame(['ports' => [['port' => '80', 'proto' => 'tcp'], ['port' => '443', 'proto' => 'tcp']], 'comment' => 'Web server'], $this->lastCall('firewall.allow_ports'));
        self::assertStringContainsString('Keep change', (string) $this->flash('success'));
        // A step with nothing to do changes nothing.
        $this->post('/components/web/install');
        self::assertSame(1, $this->calls('packages.install'));
        // Unknown component or step.
        $this->post('/components/nope/install');
        $this->post('/components/web/reboot');
        self::assertSame(1, $this->calls('packages.install'));
    }

    public function testFixesNeedTheirPermissions(): void
    {
        // Operators manage services but not the firewall or packages.
        $this->login('oper');
        $this->post('/components/web/ports');
        $this->post('/components/mail/install');
        self::assertSame(0, $this->calls('firewall.allow_ports'));
        self::assertSame(0, $this->calls('packages.install'));
        $this->post('/components/mail/boot');
        self::assertSame(1, $this->calls('services.control'));
        $this->post('/components/profile', ['expected' => ['dns']]);
        self::assertSame('', $this->app->container->get(\WhaCP\Services\SettingsService::class)->string('components_expected'), 'operators cannot change the profile');
    }

    public function testProfile(): void
    {
        $this->login('admin');
        $this->post('/components/profile', ['expected' => ['web', 'dns', 'nope']]);
        self::assertSame('web,dns', $this->app->container->get(\WhaCP\Services\SettingsService::class)->string('components_expected'));
        $page = $this->request('GET', '/components')->body();
        self::assertStringContainsString('0 of 2 components ready', $page);
        self::assertMatchesRegularExpression('#id="cp-dns" checked#', $page);
        $this->post('/components/profile', []);
        self::assertStringContainsString('Not used on this server (3)', $this->request('GET', '/components')->body());
    }
}
