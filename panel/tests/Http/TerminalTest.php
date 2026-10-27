<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Response;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Tests\Support\HttpTestCase;

/** Web terminal: who may open which shell, password confirmation, tickets, sign-out. */
final class TerminalTest extends HttpTestCase
{
    private int $siteId;
    private int $custId;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('resell', 'reseller');
        $this->custId = $this->seedUser('cust', 'customer');
        $this->seedUser('other', 'customer');
        $db = $this->app->container->get(Connection::class);
        $db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, 'shop.test', 'static', '/home/whacp/shop.test', '/home/whacp/shop.test/public_html', 'shop', ?, 'active', 'x', 'x')", [$this->custId]);
        $this->siteId = (int) $db->lastInsertId();
        $this->agent->succeed('shell.status', ['jail_problem' => '', 'accounts' => [
            ['user' => 'shop', 'folder' => 'shop.test', 'mode' => 'jailed', 'effective' => 'jailed', 'ssh_login' => true, 'keys' => 1, 'password_login' => false, 'login_shell' => '/usr/local/sbin/whacp-jailsh'],
        ]]);
        $this->agent->succeed('terminal.status', ['enabled' => true, 'root_allowed' => false, 'simulation' => false, 'max_sessions' => 20, 'max_per_account' => 3, 'sessions' => [
            ['id' => '0123456789abcdef', 'user' => 'shop', 'mode' => 'jailed', 'label' => 'cust (#3)', 'client_ip' => '192.0.2.10', 'started' => '2026-09-28T10:00:00Z', 'last_input' => '2026-09-28T10:05:00Z', 'bytes_in' => 120, 'bytes_out' => 4096, 'idle_minutes' => 15, 'max_minutes' => 480],
        ]]);
        $this->agent->succeed('terminal.open', ['ticket' => str_repeat('a', 64), 'expires_in' => 60, 'session_id' => 'fedcba9876543210', 'mode' => 'jailed', 'user' => 'shop']);
        $this->agent->succeed('terminal.kill', ['closed' => 1]);
    }

    private function json(string $path, array $body = []): Response
    {
        return $this->post($path, $body);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    public function testOwnerSignsInWithTheLinuxAccountPassword(): void
    {
        $this->login('cust');
        self::assertStringContainsString('/sites/' . $this->siteId . '/terminal"', $this->request('GET', '/sites/' . $this->siteId)->body(), 'menu entry');
        $res = $this->request('GET', '/sites/' . $this->siteId . '/terminal');
        self::assertSame(200, $res->status());
        $page = $res->body();
        foreach (['Sign in as the Linux account <code>shop</code>', 'Password for shop', 'data-auth="linux"', 'assets/vendor/xterm/xterm.js', 'assets/js/terminal.js', 'in a sandbox that shows only'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        // The Linux password field is never submitted to the panel.
        self::assertMatchesRegularExpression('/<input class="form-control" type="password" id="wcp-term-lpw" required/', $page);
        self::assertStringNotContainsString('id="wcp-term-lpw" name=', $page);
        self::assertStringNotContainsString('Your panel password', $page, 'no panel-password step for account terminals');
        // The page's policy allows style elements only with its nonce.
        preg_match('/data-style-nonce="([^"]+)"/', $page, $m);
        $csp = (string) $res->header('Content-Security-Policy');
        self::assertStringContainsString("style-src 'self' 'nonce-" . $m[1] . "'", $csp);
        self::assertStringContainsString("script-src 'self';", $csp);
        self::assertStringNotContainsString('unsafe', $csp);

        // The ticket: authorization by the panel; the password is checked by
        // the agent over the WebSocket, so the panel never passes one on.
        $res = $this->json('/sites/' . $this->siteId . '/terminal/open', ['cols' => 120, 'rows' => 30, 'password' => 'Should-Not-Travel-1']);
        self::assertSame(200, $res->status(), $res->body());
        $data = json_decode($res->body(), true)['data'];
        self::assertSame(str_repeat('a', 64), $data['ticket']);
        self::assertTrue($data['password_required']);
        $call = $this->lastCall('terminal.open');
        self::assertSame('shop', $call['user']);
        self::assertArrayNotHasKey('root', $call);
        self::assertArrayNotHasKey('password', $call);
        self::assertSame([120, 30, '192.0.2.10', 'cust (#' . $this->custId . ')', 15, 480], [$call['cols'], $call['rows'], $call['client_ip'], $call['label'], $call['idle_minutes'], $call['max_minutes']]);
        self::assertMatchesRegularExpression('/^[a-f0-9]{32}$/', $call['owner']);
        $db = $this->app->container->get(Connection::class);
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'terminal.open'"));
        // Nothing the browser sent as a password is kept anywhere.
        foreach ($this->agent->requests as $r) {
            self::assertStringNotContainsString('Should-Not-Travel-1', (string) $r['body']);
        }
        self::assertSame(0, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE details LIKE '%Should-Not-Travel%'"));

        // Signing out ends this sign-in's terminals.
        $this->post('/logout');
        self::assertSame(['owner' => $call['owner']], $this->lastCall('terminal.kill'));
    }

    public function testOtherAccountsAndPlansWithoutSsh(): void
    {
        $this->login('other');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->siteId . '/terminal')->status());
        $this->json('/terminal/unlock', ['password' => 'a-long-test-password']);
        self::assertSame(404, $this->json('/sites/' . $this->siteId . '/terminal/open')->status());
        self::assertSame(0, $this->calls('terminal.open'));

        // A plan without SSH has no terminal (Feature Manager).
        $db = $this->app->container->get(Connection::class);
        $list = $this->app->container->get(\WhaCP\Services\FeatureService::class)->save(null, ['name' => 'No SSH', 'features' => array_values(array_diff(array_keys(\WhaCP\Services\FeatureService::FEATURES), ['ssh']))], 1);
        $db->run("INSERT INTO plans (name, feature_list_id, created_at, updated_at) VALUES ('Basic', ?, 'x', 'x')", [$list]);
        $db->run('UPDATE users SET plan_id = ? WHERE id = ?', [(int) $db->lastInsertId(), $this->custId]);
        $this->switchTo('cust');
        self::assertNotSame(200, $this->request('GET', '/sites/' . $this->siteId . '/terminal')->status());
        $this->json('/terminal/unlock', ['password' => 'a-long-test-password']);
        self::assertNotSame(200, $this->json('/sites/' . $this->siteId . '/terminal/open')->status());
        self::assertSame(0, $this->calls('terminal.open'));
        self::assertStringNotContainsString('/terminal"', $this->request('GET', '/sites/' . $this->siteId)->body(), 'no menu entry');
    }

    public function testShellAccessOffShowsHowToEnableIt(): void
    {
        $this->agent->succeed('shell.status', ['jail_problem' => '', 'accounts' => [
            ['user' => 'shop', 'folder' => 'shop.test', 'mode' => 'disabled', 'effective' => 'disabled', 'ssh_login' => false, 'keys' => 0, 'password_login' => false, 'login_shell' => '/usr/sbin/nologin'],
        ]]);
        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->siteId . '/terminal')->body();
        self::assertStringContainsString('Shell access is off for this website', $page);
        self::assertStringNotContainsString('terminal.js', $page);
    }

    public function testUnlockLocksAfterRepeatedFailures(): void
    {
        $this->login('cust');
        for ($i = 0; $i < 5; $i++) {
            self::assertSame(403, $this->json('/terminal/unlock', ['password' => 'nope'])->status());
        }
        $res = $this->json('/terminal/unlock', ['password' => 'a-long-test-password']);
        self::assertSame(429, $res->status(), 'locked even with the right password');
    }

    public function testAdministratorsSeeSessionsAndCloseThem(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/terminal')->body();
        foreach (['Open sessions', 'cust (#3)', '4 KB out', 'Root shells are <strong>off</strong>', '"terminal_root": true'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('terminal.js', $page, 'no console while root shells are off');
        $this->post('/terminal/sessions/0123456789abcdef/close');
        self::assertSame(['id' => '0123456789abcdef'], $this->lastCall('terminal.kill'));
        $this->post('/terminal/sessions/..%2f/close');
        self::assertSame(1, $this->calls('terminal.kill'));

        // Root shell when the agent allows it, after confirming the password.
        $this->agent->succeed('terminal.status', ['enabled' => true, 'root_allowed' => true, 'sessions' => []]);
        self::assertStringContainsString('terminal.js', $this->request('GET', '/terminal')->body());
        self::assertSame(428, $this->json('/terminal/root/open')->status());
        $this->json('/terminal/unlock', ['password' => 'a-long-test-password']);
        self::assertSame(200, $this->json('/terminal/root/open')->status());
        self::assertTrue($this->lastCall('terminal.open')['root']);
    }

    public function testOnlyAdministratorsReachRootAndTheSessionList(): void
    {
        foreach (['resell', 'cust'] as $who) {
            $this->switchTo($who);
            self::assertNotSame(200, $this->request('GET', '/terminal')->status(), $who);
            $this->json('/terminal/unlock', ['password' => 'a-long-test-password']);
            self::assertNotSame(200, $this->json('/terminal/root/open')->status(), $who);
            $this->post('/terminal/sessions/0123456789abcdef/close');
        }
        self::assertSame(0, $this->calls('terminal.open'));
        self::assertSame(0, $this->calls('terminal.kill'));
    }
}
