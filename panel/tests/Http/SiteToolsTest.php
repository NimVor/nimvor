<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * Cron, workers and Git deployment through the HTTP kernel.
 */
final class SiteToolsTest extends HttpTestCase
{
    private int $siteId;
    private int $adminId;

    protected function setUp(): void
    {
        parent::setUp();
        $this->adminId = $this->seedUser('admin', 'admin');
        $this->login('admin');
        $admin = $this->app->container->get(UserRepository::class)->find($this->adminId);
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $admin);
        $this->agent->succeed('cron.apply', ['changed' => true]);
        $this->agent->succeed('cron.run', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->agent->succeed('workers.apply', ['unit' => 'x', 'changed' => [], 'active' => true]);
        $this->agent->succeed('workers.status', []);
        $this->agent->succeed('workers.remove', ['removed' => 'x']);
    }

    public function testCronLifecycle(): void
    {
        $base = '/sites/' . $this->siteId . '/cron';
        self::assertSame(200, $this->request('GET', $base)->status());

        $this->post($base, ['schedule' => 'custom', 'schedule_custom' => '0 25 * * *', 'command' => 'php x']);
        self::assertArrayHasKey('schedule', (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('cron.apply'));

        $this->post($base, ['schedule' => '*/5 * * * *', 'command' => 'php app/artisan schedule:run', 'enabled' => '1']);
        $call = $this->lastCall('cron.apply');
        self::assertSame('shop.test', $call['domain']);
        self::assertSame('*/5 * * * *', $call['jobs'][0]['schedule']);
        self::assertTrue($call['jobs'][0]['enabled']);
        $jobId = (int) $call['jobs'][0]['id'];

        $this->post($base . '/' . $jobId . '/toggle');
        self::assertFalse($this->lastCall('cron.apply')['jobs'][0]['enabled']);

        $this->post($base . '/' . $jobId . '/run');
        self::assertSame('php app/artisan schedule:run', $this->lastCall('cron.run')['command']);
        self::assertSame('aaaaaaaaaaaaaaaa', $this->flash('job'));

        // A failed apply leaves the stored schedule unchanged.
        $this->agent->fail('cron.apply', 422, 'invalid_params', 'bad');
        $this->post($base . '/' . $jobId . '/delete');
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar('SELECT COUNT(*) FROM cron_jobs'));
        $this->agent->succeed('cron.apply', ['changed' => true]);
        $this->post($base . '/' . $jobId . '/delete');
        self::assertSame([], $this->lastCall('cron.apply')['jobs']);
    }

    public function testWorkersAndCleanupOnWebsiteDelete(): void
    {
        $base = '/sites/' . $this->siteId . '/workers';
        $this->post($base, ['name' => 'Queue', 'command' => 'php artisan queue:work', 'directory' => 'app', 'env' => "APP_ENV=production\nBAD LINE", 'restart' => 'always']);
        self::assertArrayHasKey('env', (array) $this->flash('errors'));

        $this->post($base, ['name' => 'Queue', 'command' => 'php artisan queue:work', 'directory' => 'app', 'env' => "APP_ENV=production", 'restart' => 'always', 'enabled' => '1']);
        $call = $this->lastCall('workers.apply');
        self::assertSame('shop-queue', $call['slug']);
        self::assertSame(['APP_ENV' => 'production'], $call['env']);
        self::assertTrue($call['enabled']);
        self::assertStringContainsString('Queue', $this->request('GET', $base)->body());

        // Disabling the website stops its workers.
        $this->post('/sites/' . $this->siteId . '/toggle');
        self::assertFalse($this->lastCall('workers.apply')['enabled']);

        $this->agent->succeed('sites.delete', ['warnings' => []]);
        $this->post('/sites/' . $this->siteId . '/delete', ['confirm_domain' => 'shop.test']);
        self::assertSame('shop-queue', $this->lastCall('workers.remove')['slug']);
        self::assertSame([], $this->lastCall('cron.apply')['jobs']);
        self::assertSame(0, (int) $this->app->container->get(Connection::class)->scalar('SELECT COUNT(*) FROM workers'));
    }

    public function testGitDeployAndWebhooks(): void
    {
        $base = '/sites/' . $this->siteId . '/git';
        $this->agent->succeed('deploy.key', ['public_key' => 'ssh-ed25519 AAAA whacp-deploy@shop.test']);
        $this->agent->succeed('deploy.run', ['job_id' => 'bbbbbbbbbbbbbbbb']);

        $this->post($base, ['repo' => 'file:///etc', 'branch' => 'main', 'path' => 'app']);
        self::assertArrayHasKey('repo', (array) $this->flash('errors'));
        $this->post($base, ['repo' => 'git@github.com:acme/shop.git', 'branch' => 'main', 'path' => 'logs']);
        self::assertArrayHasKey('path', (array) $this->flash('errors'));

        $this->post($base, ['repo' => 'git@github.com:acme/shop.git', 'branch' => 'main', 'path' => 'app', 'hooks' => "composer install\n\nphp artisan migrate --force", 'auto_deploy' => '0']);
        $page = $this->request('GET', $base)->body();
        self::assertStringContainsString('ssh-ed25519 AAAA', $page);

        $this->post($base . '/deploy');
        $call = $this->lastCall('deploy.run');
        self::assertSame(['composer install', 'php artisan migrate --force'], $call['hooks']);
        self::assertSame('bbbbbbbbbbbbbbbb', $this->flash('job'));

        $db = $this->app->container->get(Connection::class);
        $cfg = $db->fetch('SELECT * FROM git_repositories');
        $secret = $this->app->container->get(\WhaCP\Security\Crypto::class)->decrypt((string) $cfg['webhook_secret_enc']);
        $hook = '/hooks/deploy/' . $cfg['webhook_token'];
        $body = (string) json_encode(['ref' => 'refs/heads/main']);
        $sig = 'sha256=' . hash_hmac('sha256', $body, $secret);
        $runs = $this->calls('deploy.run');

        // No session and no CSRF token needed, but the signature is.
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        self::assertSame(401, $this->request('POST', $hook, [], ['x-hub-signature-256' => 'sha256=00'], $body)->status());
        self::assertSame(404, $this->request('POST', '/hooks/deploy/' . str_repeat('0', 40), [], ['x-hub-signature-256' => $sig], $body)->status());
        self::assertSame(202, $this->request('POST', $hook, [], ['x-hub-signature-256' => $sig], $body)->status(), 'auto deploy off: ignored');
        self::assertSame($runs, $this->calls('deploy.run'));

        $db->run('UPDATE git_repositories SET auto_deploy = 1');
        $other = (string) json_encode(['ref' => 'refs/heads/dev']);
        $this->request('POST', $hook, [], ['x-hub-signature-256' => 'sha256=' . hash_hmac('sha256', $other, $secret)], $other);
        self::assertSame($runs, $this->calls('deploy.run'), 'other branches are ignored');

        self::assertSame(202, $this->request('POST', $hook, [], ['x-hub-signature-256' => $sig], $body)->status());
        // A captured delivery replayed (even under another delivery ID, which
        // is not signed) does not deploy again (F-05).
        self::assertSame(409, $this->request('POST', $hook, [], ['x-hub-signature-256' => $sig, 'x-github-delivery' => 'another-id'], $body)->status());
        self::assertSame(409, $this->request('POST', $hook, [], ['x-gitlab-token' => $secret], $body)->status());
        self::assertSame($runs + 1, $this->calls('deploy.run'));
        self::assertSame(2, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'deploy.webhook' AND details LIKE '%replayed%'"), 'both replays audited');
        // The next push (a new body) deploys.
        $next = (string) json_encode(['ref' => 'refs/heads/main', 'after' => 'b2c3']);
        self::assertSame(202, $this->request('POST', $hook, [], ['x-gitlab-token' => $secret], $next)->status());
        self::assertSame($runs + 2, $this->calls('deploy.run'));
        self::assertSame(2, (int) $db->scalar("SELECT COUNT(*) FROM deployments WHERE trigger = 'webhook'"));

        // Other signed-in routes still require CSRF.
        self::assertSame(419, $this->request('POST', $base . '/deploy')->status());
    }

    public function testCustomersOnlySeeTheirOwnSites(): void
    {
        $this->seedUser('cust', 'customer');
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        foreach (['cron', 'workers', 'git'] as $tab) {
            $res = $this->request('GET', '/sites/' . $this->siteId . '/' . $tab);
            self::assertContains($res->status(), [403, 404], $tab);
        }
    }
}
