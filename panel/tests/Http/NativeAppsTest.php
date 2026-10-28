<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\AppInstallerService;
use NimVor\Tests\Support\HttpTestCase;

/** Natively deployed applications in the installer: Gitea, Django, FastAPI. */
final class NativeAppsTest extends HttpTestCase
{
    private Connection $db;
    private int $php;
    private int $proxy;
    private int $python;
    private int $otherProxy;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $site = function (string $domain, int $owner, string $kind, string $phpV, string $proxy, string $runtime): int {
            $this->db->run(
                "INSERT INTO websites (server_id, domain, kind, php_version, proxy_pass, app_runtime, root, document_root, system_user, owner_id, status, https, created_at, updated_at) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 1, 'x', 'x')",
                [$domain, $kind, $phpV, $proxy, $runtime, '/home/u/' . $domain, '/home/u/' . $domain . '/public_html', 'u' . preg_replace('/\W/', '', $domain), $owner]
            );
            return (int) $this->db->scalar('SELECT id FROM websites WHERE domain = ?', [$domain]);
        };
        $this->php = $site('shop.test', $cust, 'php', '8.3', '', '');
        $this->proxy = $site('git.test', $cust, 'proxy', '', 'http://127.0.0.1:3101', '');
        $this->python = $site('py.test', $cust, 'proxy', '', 'http://127.0.0.1:3201', 'python:3.12.7');
        $this->otherProxy = $site('theirs.test', $other, 'proxy', '', 'http://127.0.0.1:3301', '');
        $this->agent->succeed('apps.install', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->login('admin');
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    private function apps(int $id): string
    {
        return '/sites/' . $id . '/apps';
    }

    private function enable(string ...$slugs): void
    {
        foreach ($slugs as $s) {
            $this->db->run("UPDATE app_catalog SET status = 'enabled' WHERE slug = ?", [$s]);
        }
    }

    public function testWhichWebsitesCanRunWhichApplications(): void
    {
        $php = ['kind' => 'php', 'php_version' => '8.3', 'app_runtime' => ''];
        $proxy = ['kind' => 'proxy', 'php_version' => '', 'app_runtime' => ''];
        $py = ['kind' => 'proxy', 'php_version' => '', 'app_runtime' => 'python:3.12.7'];
        $node = ['kind' => 'proxy', 'php_version' => '', 'app_runtime' => 'node:20.17.0'];
        $static = ['kind' => 'static', 'php_version' => '', 'app_runtime' => ''];
        foreach (['joomla' => [1, 0, 0, 0, 0], 'gitea' => [0, 1, 0, 0, 0], 'django' => [0, 0, 1, 0, 0], 'fastapi' => [0, 0, 1, 0, 0]] as $app => $want) {
            foreach ([$php, $proxy, $py, $node, $static] as $i => $s) {
                self::assertSame((bool) $want[$i], AppInstallerService::compatible($s, $app), $app . ' on ' . $s['kind'] . '/' . $s['app_runtime']);
            }
        }
    }

    public function testTheCatalogPageExplainsWhatEachApplicationNeeds(): void
    {
        $page = $this->request('GET', $this->apps($this->proxy))->body();
        self::assertStringContainsString('data-bs-target="#app-gitea"', $page);
        self::assertStringContainsString('Gitea', $page);
        self::assertStringContainsString('Needs a Python website', $page, 'Django and FastAPI cannot go on a plain proxy website');
        self::assertStringContainsString('>Program<', $page);
        self::assertStringContainsString('>SQLite<', $page);
        self::assertStringContainsString('>Native<', $page);
        self::assertStringContainsString('>Testing<', $page);
        // On a PHP website the PHP applications are installable and the natives say what they need.
        $phpPage = $this->request('GET', $this->apps($this->php))->body();
        self::assertStringContainsString('Needs a reverse-proxy website', $phpPage);
        self::assertStringContainsString('WordPress + plugin', $phpPage, 'plugin applications are marked as extensions');
    }

    public function testNativeApplicationsInTestingAreForAdministratorsOnly(): void
    {
        $this->switchTo('cust');
        self::assertStringNotContainsString('data-bs-target="#app-gitea"', $this->request('GET', $this->apps($this->proxy))->body());
        $before = $this->calls('apps.install');
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea']);
        self::assertStringContainsString('not available', (string) $this->flash('error'));
        self::assertSame($before, $this->calls('apps.install'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM app_installs'));
    }

    public function testInstallingGiteaSendsTheRightRequestAndCreatesNoDatabase(): void
    {
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea']);
        $sent = $this->lastCall('apps.install');
        self::assertSame('gitea', $sent['app']);
        self::assertSame(3101, $sent['port'], 'the port the website already forwards to');
        self::assertSame('gitea', $sent['path']);
        self::assertSame('', $sent['runtime']);
        self::assertTrue($sent['allow_testing']);
        self::assertMatchesRegularExpression('/^git-gitea(-\d+)?$/', $sent['service_slug']);
        self::assertSame('https://git.test', $sent['url']);
        self::assertArrayNotHasKey('db', $sent);
        self::assertArrayNotHasKey('admin_password', $sent);
        self::assertSame(0, $this->calls('db.create'), 'Gitea uses SQLite: no database');
        $row = (array) $this->db->fetch("SELECT * FROM app_installs WHERE app = 'gitea'");
        self::assertSame(['running', 'binary-service', 'aaaaaaaaaaaaaaaa'], [$row['status'], $row['strategy'], $row['job_id']]);
    }

    public function testInstallingDjangoCreatesAnAdministratorPasswordShownOnce(): void
    {
        $this->post($this->apps($this->python) . '/django', ['path' => 'app', 'admin_user' => 'owner', 'admin_email' => 'owner@py.test']);
        $sent = $this->lastCall('apps.install');
        self::assertSame('python:3.12.7', $sent['runtime']);
        self::assertSame(3201, $sent['port']);
        self::assertSame('owner', $sent['admin_user']);
        self::assertGreaterThanOrEqual(20, strlen($sent['admin_password']));
        $secret = (array) $this->flash('secret');
        self::assertSame($sent['admin_password'], $secret['password']);
        self::assertStringNotContainsString($sent['admin_password'], (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs')), 'never in the audit log');
        self::assertStringNotContainsString($sent['admin_password'], (string) json_encode($this->db->fetchAll('SELECT * FROM app_installs')), 'never stored');
        // Invalid administrator details stay in the panel.
        $this->db->run("UPDATE app_installs SET status = 'failed'");
        $calls = $this->calls('apps.install');
        $this->post($this->apps($this->python) . '/django', ['path' => 'app', 'admin_user' => 'x', 'admin_email' => 'nope']);
        self::assertArrayHasKey('admin_email', (array) $this->flash('errors'));
        self::assertSame($calls, $this->calls('apps.install'));
    }

    public function testNativeApplicationsRefuseTheWrongKindOfWebsite(): void
    {
        foreach ([[$this->php, 'gitea'], [$this->php, 'django'], [$this->proxy, 'django'], [$this->python, 'gitea']] as [$site, $app]) {
            $before = $this->calls('apps.install');
            $this->post($this->apps($site) . '/' . $app, ['path' => 'app', 'admin_user' => 'owner', 'admin_email' => 'o@x.test']);
            self::assertStringContainsString('needs', (string) $this->flash('error'), $app . ' on site ' . $site);
            self::assertSame($before, $this->calls('apps.install'));
        }
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM app_installs'));
        // And a PHP application cannot go on a proxy website.
        $this->post($this->apps($this->proxy) . '/joomla', []);
        self::assertStringContainsString('needs a PHP website', (string) $this->flash('error'));
    }

    public function testPathAndPortValidation(): void
    {
        foreach (['../etc', 'a/b', 'logs', '.ssh', '', 'x y'] as $path) {
            $this->post($this->apps($this->proxy) . '/gitea', ['path' => $path]);
            if ($path === '') {
                continue; // an empty folder falls back to the default
            }
            self::assertArrayHasKey('path', (array) $this->flash('errors'), 'path ' . json_encode($path));
        }
        // A website that forwards to no port cannot host an application.
        $this->db->run("UPDATE websites SET proxy_pass = '' WHERE id = ?", [$this->proxy]);
        $this->db->run("UPDATE app_installs SET status = 'failed'");
        $before = $this->calls('apps.install');
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea2']);
        self::assertStringContainsString('local port', (string) $this->flash('error'));
        self::assertSame($before, $this->calls('apps.install'));
    }

    public function testOneProgramPerProxyWebsite(): void
    {
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea']);
        // Duplicate while running.
        $before = $this->calls('apps.install');
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea']);
        self::assertStringContainsString('already', (string) $this->flash('error'));
        // A different folder is still the same website and port.
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea-two']);
        self::assertStringContainsString('already runs an application', (string) $this->flash('error'));
        self::assertSame($before, $this->calls('apps.install'));
        // Finished and succeeded: still occupied.
        $this->db->run("UPDATE app_installs SET status = 'succeeded'");
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea-three']);
        self::assertStringContainsString('already runs an application', (string) $this->flash('error'));
        // A failed attempt frees the website.
        $this->db->run("UPDATE app_installs SET status = 'failed'");
        $this->agent->succeed('apps.install', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea-three']);
        self::assertSame('bbbbbbbbbbbbbbbb', $this->lastCallJob());
    }

    private function lastCallJob(): string
    {
        return (string) $this->db->scalar("SELECT job_id FROM app_installs ORDER BY id DESC LIMIT 1");
    }

    public function testASuccessfulInstallIsListedWithTheWorkersAndRecordsItsVersion(): void
    {
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea']);
        $service = ['slug' => 'git-gitea', 'name' => 'Gitea', 'directory' => 'gitea', 'command' => "'/home/u/git.test/gitea/bin/gitea' web --config '/home/u/git.test/gitea/custom/conf/app.ini'",
            'env' => ['GITEA_WORK_DIR' => '/home/u/git.test/gitea', 'HOME' => '/home/u/git.test/gitea'], 'restart' => 'always', 'procs' => 1, 'memory_mb' => 0, 'runtime' => ''];
        $this->agent->succeed('jobs.get', ['id' => 'aaaaaaaaaaaaaaaa', 'status' => 'succeeded', 'result' => ['version' => '28.2.0', 'service' => $service]]);
        $page = $this->request('GET', $this->apps($this->proxy))->body();
        self::assertStringContainsString('28.2.0', $page, 'the installed version is shown');
        $row = (array) $this->db->fetch("SELECT * FROM app_installs WHERE app = 'gitea'");
        self::assertSame(['succeeded', '28.2.0'], [$row['status'], $row['version']]);
        $w = (array) $this->db->fetch("SELECT * FROM workers WHERE slug = 'git-gitea'");
        self::assertSame($this->proxy, (int) $w['website_id']);
        self::assertSame($service['command'], $w['command']);
        self::assertSame(1, (int) $w['enabled']);
        // The Workers page lists it.
        self::assertStringContainsString('git-gitea', $this->request('GET', '/sites/' . $this->proxy . '/workers')->body());
        // Seeing the page again does not duplicate it.
        $this->request('GET', $this->apps($this->proxy));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM workers WHERE slug = 'git-gitea'"));
        // The finish link opens the application.
        self::assertStringContainsString('https://git.test/', $page);
    }

    public function testAdoptionIgnoresMalformedServices(): void
    {
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea']);
        $this->agent->succeed('jobs.get', ['id' => 'aaaaaaaaaaaaaaaa', 'status' => 'succeeded', 'result' => ['service' => ['slug' => '../x; rm', 'command' => 'x']]]);
        $this->request('GET', $this->apps($this->proxy));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM workers'));
    }

    public function testAFailedNativeInstallIsAuditedAndFreesTheWebsite(): void
    {
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea']);
        $this->agent->succeed('jobs.get', ['id' => 'aaaaaaaaaaaaaaaa', 'status' => 'failed', 'error' => 'the Gitea download does not match its published checksum; nothing was installed']);
        $this->request('GET', $this->apps($this->proxy));
        self::assertSame('failed', $this->db->scalar("SELECT status FROM app_installs WHERE app = 'gitea'"));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM workers'));
        $audit = (array) $this->db->fetch("SELECT * FROM audit_logs WHERE action = 'app.install_result' ORDER BY id DESC LIMIT 1");
        self::assertSame('failure', $audit['result']);
        self::assertStringContainsString('checksum', (string) $audit['details']);
        $this->agent->succeed('apps.install', ['job_id' => 'cccccccccccccccc']);
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea']);
        self::assertSame('cccccccccccccccc', $this->lastCallJob(), 'the website can be tried again');
    }

    public function testCustomersCanInstallOnceAnApplicationIsEnabled(): void
    {
        $this->enable('gitea');
        $this->switchTo('cust');
        self::assertStringContainsString('data-bs-target="#app-gitea"', $this->request('GET', $this->apps($this->proxy))->body());
        $this->post($this->apps($this->proxy) . '/gitea', ['path' => 'gitea', 'allow_testing' => '1']);
        $sent = $this->lastCall('apps.install');
        self::assertSame('gitea', $sent['app']);
        self::assertFalse($sent['allow_testing'], 'a customer cannot grant themselves the testing allowance');
        // Another customer's website is never touched.
        $before = $this->calls('apps.install');
        $this->post($this->apps($this->otherProxy) . '/gitea', ['path' => 'gitea']);
        self::assertSame($before, $this->calls('apps.install'));
    }

    public function testThePlanForANativeApplication(): void
    {
        $this->agent->succeed('apps.plan', [
            'app' => 'gitea', 'name' => 'Gitea', 'status' => 'testing', 'ok' => true, 'license' => 'MIT', 'disk_needed_mb' => 500, 'disk_free_mb' => 9000,
            'checks' => [['name' => 'git is installed', 'ok' => true], ['name' => "The application's port is free", 'ok' => true]],
            'steps' => [['action' => 'Download', 'detail' => 'dl.gitea.com'], ['action' => 'Create service', 'detail' => 'a supervised service']],
            'conflicts' => [],
        ]);
        $page = $this->request('GET', $this->apps($this->proxy) . '/gitea/plan')->body();
        foreach (['What installing Gitea would do', 'Testing', 'Create service', 'git is installed', 'Everything this installation needs is in place'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $sent = $this->lastCall('apps.plan');
        self::assertSame(3101, $sent['port']);
        self::assertSame('gitea', $sent['app']);
        self::assertArrayNotHasKey('db', $sent);
        self::assertSame(0, $this->calls('apps.install'));
    }
}
