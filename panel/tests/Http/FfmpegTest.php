<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Services\FfmpegService;
use WhaCP\Tests\Support\HttpTestCase;

/** FFmpeg per hosting plan: who may run it, applied through the agent. */
final class FfmpegTest extends HttpTestCase
{
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->db = $this->app->container->get(Connection::class);
        $admin = $this->seedUser('admin', 'admin');
        $resYes = $this->seedUser('resyes', 'reseller');
        $resNo = $this->seedUser('resno', 'reseller');
        $a = $this->seedUser('custa', 'customer');   // plan with FFmpeg
        $b = $this->seedUser('custb', 'customer');   // plan without
        $c = $this->seedUser('custc', 'customer');   // FFmpeg plan, reseller's plan has it
        $d = $this->seedUser('custd', 'customer');   // FFmpeg plan, reseller's plan does not
        $e = $this->seedUser('custe', 'customer');   // FFmpeg plan, suspended
        $now = gmdate('c');
        $plan = function (string $name, int $ffmpeg, ?int $owner = null) use ($now): int {
            $this->db->run('INSERT INTO plans (owner_id, name, ffmpeg, created_at, updated_at) VALUES (?, ?, ?, ?, ?)', [$owner, $name, $ffmpeg, $now, $now]);
            return $this->db->lastInsertId();
        };
        $with = $plan('With', 1);
        $without = $plan('Without', 0);
        $resellerOwn = $plan('Resold', 1, $resYes);
        foreach ([[$a, $with, null], [$b, $without, null], [$resYes, $with, null], [$resNo, $without, null], [$c, $resellerOwn, $resYes], [$d, $with, $resNo], [$e, $with, null]] as [$u, $p, $parent]) {
            $this->db->run('UPDATE users SET plan_id = ?, parent_id = ? WHERE id = ?', [$p, $parent, $u]);
        }
        $this->db->run('UPDATE users SET suspended_at = ? WHERE id = ?', [$now, $e]);
        $site = function (string $domain, string $user, int $owner, string $php = '8.3') use ($now): void {
            $this->db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, php_version, status, created_at, updated_at) VALUES (1, ?, 'php', ?, ?, ?, ?, ?, 'active', ?, ?)",
                [$domain, '/home/whacp/' . $domain, '/home/whacp/' . $domain . '/public_html', $user, $owner, $php, $now, $now]);
        };
        $site('admin.test', 'adminsite', $admin);
        $site('a.test', 'usera', $a);
        $site('b.test', 'userb', $b);
        $site('c.test', 'userc', $c);
        $site('d.test', 'userd', $d);
        $site('e.test', 'usere', $e);
        $site('shared.test', 'usera', $b); // an account shared with an owner without FFmpeg
    }

    public function testWhoMayUseFfmpeg(): void
    {
        self::assertSame(['adminsite', 'userc'], $this->app->container->get(FfmpegService::class)->allowedUsers());
    }

    public function testReconcileSetsTheGroupAndReloadsPhpForRemovedAccounts(): void
    {
        $this->agent->succeed('ffmpeg.status', ['installed' => true, 'restricted' => true, 'members' => ['userb', 'adminsite']]);
        $this->agent->succeed('ffmpeg.access', ['added' => ['userc'], 'removed' => ['userb']]);
        $this->agent->succeed('discovery.php', ['versions' => [['version' => '8.3', 'fpm_service' => 'php8.3-fpm.service']]]);
        $this->agent->succeed('services.control', ['ok' => true]);
        $log = $this->app->container->get(FfmpegService::class)->reconcile();
        self::assertSame(['users' => ['adminsite', 'userc']], $this->lastCall('ffmpeg.access'));
        self::assertSame(['unit' => 'php8.3-fpm.service', 'op' => 'reload'], $this->lastCall('services.control'));
        self::assertSame(['FFmpeg access: 1 account(s) added, 1 removed.'], $log);
        // Nothing to do when it matches.
        $this->agent->succeed('ffmpeg.status', ['installed' => true, 'restricted' => true, 'members' => ['userc', 'adminsite']]);
        $n = $this->calls('ffmpeg.access');
        $this->app->container->get(FfmpegService::class)->reconcile();
        self::assertSame($n, $this->calls('ffmpeg.access'));
        // Not installed: nothing.
        $this->agent->succeed('ffmpeg.status', ['installed' => false]);
        $this->app->container->get(FfmpegService::class)->reconcile(true);
        self::assertSame($n, $this->calls('ffmpeg.access'));
    }

    public function testPlanFormAndAdminPage(): void
    {
        $this->agent->succeed('ffmpeg.status', ['installed' => true, 'version' => 'ffmpeg version 6.1.1', 'package' => 'ffmpeg', 'restricted' => false, 'members' => [], 'group' => 'whacp-ffmpeg', 'binaries' => [['path' => '/usr/bin/ffmpeg', 'present' => true, 'mode' => '0755 0:0', 'restricted' => false]]]);
        $this->agent->succeed('ffmpeg.access', ['added' => ['adminsite', 'userc'], 'removed' => []]);
        $this->login('admin');
        $page = $this->request('GET', '/ffmpeg')->body();
        foreach (['ffmpeg version 6.1.1', 'Update FFmpeg', 'everyone', 'not applied yet', 'adminsite, userc'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $id = (int) $this->db->scalar("SELECT id FROM plans WHERE name = 'Without'");
        $this->post('/plans', ['plan_id' => (string) $id, 'name' => 'Without', 'ffmpeg' => '1']);
        self::assertSame(1, (int) $this->db->scalar('SELECT ffmpeg FROM plans WHERE id = ?', [$id]));
        self::assertGreaterThan(0, $this->calls('ffmpeg.access'), 'applied when the plan is saved');
        self::assertSame(['adminsite', 'usera', 'userb', 'userc', 'userd'], $this->app->container->get(FfmpegService::class)->allowedUsers(), 'resno is on this plan too, so custd gains it');
        $this->agent->succeed('packages.install', ['job_id' => 'abcdef0123456789']);
        $this->post('/ffmpeg/install');
        self::assertSame(['name' => 'ffmpeg'], $this->lastCall('packages.install'));
    }

    public function testOnlyServerAdministrators(): void
    {
        $this->login('resyes');
        self::assertNotSame(200, $this->request('GET', '/ffmpeg')->status());
        $this->post('/ffmpeg/apply');
        $this->post('/ffmpeg/uninstall');
        self::assertSame(0, $this->calls('ffmpeg.access'));
        self::assertSame(0, $this->calls('ffmpeg.uninstall'));
    }
}
