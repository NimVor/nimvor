<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Tests\Support\HttpTestCase;

/** Security → Compiler Access, SMTP Restrictions, Shell Fork Bomb Protection, Traceroute. */
final class HardeningTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $db = $this->app->container->get(Connection::class);
        $db->run("INSERT INTO websites (server_id, domain, kind, php_version, https, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, 'shop.test', 'php', '8.3', 1, '/home/whacp/shop.test', '/home/whacp/shop.test/public_html', 'shop', ?, 'active', 'x', 'x')", [$admin]);
        $this->agent->succeed('hardening.status', [
            'compilers' => ['restricted' => true, 'group' => 'whacp-compiler', 'members' => ['shop'], 'programs' => [['path' => '/usr/bin/x86_64-linux-gnu-gcc-13', 'mode' => '750', 'owner' => 'root:whacp-compiler']]],
            'traceroute' => ['enabled' => false, 'programs' => [['path' => '/usr/bin/traceroute.db', 'mode' => '700', 'owner' => 'root:root']]],
            'forkbomb' => ['enabled' => true, 'file' => '/etc/security/limits.d/90-whacp-forkbomb.conf', 'soft' => 200, 'hard' => 300],
            'smtp' => ['enabled' => true, 'active' => true, 'available' => true, 'allowed' => ['root', 'postfix'], 'ports' => [25, 465, 587]],
        ]);
        foreach (['hardening.compilers', 'hardening.smtp', 'hardening.forkbomb', 'hardening.traceroute'] as $a) {
            $this->agent->succeed($a, []);
        }
    }

    public function testPagesShowTheStateAndTheDescriptions(): void
    {
        $this->login('admin');
        foreach ([
            '/security/compilers' => ['Many common exploits require a working C compiler', 'x86_64-linux-gnu-gcc-13', 'root:whacp-compiler', 'value="shop" id="cc-u0" checked', 'Compiler Access'],
            '/security/smtp' => ['only the MTA, mailman, and root', 'root, postfix', '25, 465, 587', '>Disable<'],
            '/security/forkbomb' => ['Fork Bomb Protection denies users', 'We recommend that you enable', 'Disable Protection'],
            '/security/traceroute' => ['packet routing statistics', '/usr/bin/traceroute.db', '>700<', 'Traceroute Status:', '>Enable<'],
        ] as $path => $wants) {
            $res = $this->request('GET', $path);
            self::assertSame(200, $res->status(), $path);
            foreach ($wants as $want) {
                self::assertStringContainsString($want, $res->body(), $path . ': ' . $want);
            }
            self::assertStringContainsString('data-wcp-section-menu="security"', $res->body());
        }
    }

    public function testAdministratorsChangeThemAndChangesAreAudited(): void
    {
        $this->login('admin');
        $this->post('/security/compilers', ['restricted' => '1', 'users' => ['shop']]);
        self::assertSame(['restricted' => true, 'users' => ['shop']], $this->lastCall('hardening.compilers'));
        $this->post('/security/compilers', ['restricted' => '1', 'users' => ['root']]);
        self::assertSame(1, $this->calls('hardening.compilers'), 'only website accounts can be allowed');
        $this->post('/security/compilers', ['restricted' => '0', 'users' => ['shop']]);
        self::assertSame(['restricted' => false, 'users' => []], $this->lastCall('hardening.compilers'));

        $this->post('/security/smtp', ['enabled' => '1']);
        self::assertSame(['enabled' => true], $this->lastCall('hardening.smtp'));
        $this->post('/security/forkbomb', ['enabled' => '0']);
        self::assertSame(['enabled' => false], $this->lastCall('hardening.forkbomb'));
        $this->post('/security/traceroute', ['enabled' => '0']);
        self::assertSame(['enabled' => false], $this->lastCall('hardening.traceroute'));
        $db = $this->app->container->get(Connection::class);
        foreach (['hardening.compilers', 'hardening.smtp', 'hardening.forkbomb', 'hardening.traceroute'] as $a) {
            self::assertNotNull($db->scalar('SELECT id FROM audit_logs WHERE action = ?', [$a]), $a . ' audited');
        }
    }

    public function testOthersCannotSeeOrChangeThem(): void
    {
        $this->login('cust');
        foreach (['compilers', 'smtp', 'forkbomb', 'traceroute'] as $p) {
            self::assertNotSame(200, $this->request('GET', '/security/' . $p)->status(), $p);
            $this->post('/security/' . $p, ['enabled' => '0', 'restricted' => '0']);
        }
        foreach (['hardening.status', 'hardening.compilers', 'hardening.smtp', 'hardening.forkbomb', 'hardening.traceroute'] as $a) {
            self::assertSame(0, $this->calls($a), $a);
        }
    }
}
