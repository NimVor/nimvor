<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Tests\Support\HttpTestCase;

/** Security → Shell access: administrators only, per account or for all. */
final class ShellAccessTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('resell', 'reseller');
        $this->seedUser('ops', 'operator');
        $this->agent->succeed('shell.status', ['jail_problem' => '', 'accounts' => [
            ['user' => 'shop', 'folder' => 'shop.test', 'mode' => 'jailed', 'effective' => 'jailed', 'ssh_login' => true, 'keys' => 1, 'password_login' => false, 'login_shell' => '/usr/local/sbin/whacp-jailsh'],
            ['user' => 'blog', 'folder' => 'blog.test', 'mode' => 'normal', 'effective' => 'disabled', 'ssh_login' => false, 'keys' => 0, 'password_login' => false, 'login_shell' => '/usr/sbin/nologin'],
        ]]);
        $this->agent->succeed('shell.set', ['accounts' => [['user' => 'blog', 'mode' => 'jailed', 'effective' => 'disabled']]]);
    }

    public function testAdministratorsSeeAndChangeShellAccess(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/security/shell')->body();
        foreach (['Shell access', '>shop<', 'On (1 key)', 'Set to normal; applies when SSH/SFTP login is on', 'Apply to all accounts'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post('/security/shell', ['users' => ['blog'], 'mode' => 'jailed']);
        self::assertSame(['users' => ['blog'], 'mode' => 'jailed'], $this->lastCall('shell.set'));
        self::assertStringContainsString('takes effect once their login is turned on', (string) $this->flash('success'));
        self::assertSame('shell.set', $this->app->container->get(Connection::class)->scalar("SELECT action FROM audit_logs WHERE action = 'shell.set'"));

        $this->post('/security/shell', ['scope' => 'all', 'confirm' => 'all', 'mode' => 'disabled']);
        self::assertSame(['users' => ['shop', 'blog'], 'mode' => 'disabled'], $this->lastCall('shell.set'));

        foreach ([['mode' => 'root'], ['users' => ['../etc'], 'mode' => 'jailed']] as $bad) {
            $before = $this->calls('shell.set');
            $this->post('/security/shell', $bad + ['users' => ['blog']]);
            self::assertSame($before, $this->calls('shell.set'), json_encode($bad));
        }
    }

    public function testOthersCannotChangeShellAccess(): void
    {
        foreach (['resell', 'ops'] as $who) {
            $this->session = new ArraySession();
            $this->app = $this->makeApp();
            $this->login($who);
            if ($who === 'resell') {
                self::assertNotSame(200, $this->request('GET', '/security/shell')->status(), 'resellers do not see it');
            }
            $this->post('/security/shell', ['users' => ['shop'], 'mode' => 'normal']);
            self::assertSame(0, $this->calls('shell.set'), $who);
        }
    }

    public function testAccountsWhosePlanExcludesSshGetNoShell(): void
    {
        $db = $this->app->container->get(Connection::class);
        $cust = $this->seedUser('cust', 'customer');
        $list = $this->app->container->get(\WhaCP\Services\FeatureService::class)->save(null, ['name' => 'No SSH', 'features' => array_values(array_diff(array_keys(\WhaCP\Services\FeatureService::FEATURES), ['ssh']))], 1);
        $db->run("INSERT INTO plans (name, feature_list_id, created_at, updated_at) VALUES ('Basic', ?, 'x', 'x')", [$list]);
        $db->run('UPDATE users SET plan_id = ? WHERE id = ?', [(int) $db->lastInsertId(), $cust]);
        $db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, 'blog.test', 'static', '/home/whacp/blog.test', '/home/whacp/blog.test/public_html', 'blog', ?, 'active', 'x', 'x')", [$cust]);
        $this->login('admin');
        $this->post('/security/shell', ['users' => ['blog'], 'mode' => 'jailed']);
        self::assertSame(0, $this->calls('shell.set'));
        self::assertStringContainsString('does not include SSH', (string) $this->flash('error'));
        $this->post('/security/shell', ['scope' => 'all', 'confirm' => 'all', 'mode' => 'jailed']);
        self::assertSame(['users' => ['shop'], 'mode' => 'jailed'], $this->lastCall('shell.set'));
        self::assertStringContainsString('Skipped (their plan does not include SSH): blog', (string) $this->flash('success'));
        $this->post('/security/shell', ['users' => ['blog'], 'mode' => 'disabled']);
        self::assertSame(['users' => ['blog'], 'mode' => 'disabled'], $this->lastCall('shell.set'), 'taking a shell away is always allowed');

        // The scheduler takes away a shell the plan no longer includes
        // (blog is "normal" in the agent's status).
        $lines = $this->app->container->get(\WhaCP\Services\ShellService::class)->reconcile();
        self::assertSame(['users' => ['blog'], 'mode' => 'disabled'], $this->lastCall('shell.set'));
        self::assertStringContainsString('blog', $lines[0]);
    }
}
