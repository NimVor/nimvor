<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Tests\Support\HttpTestCase;

final class ImportTest extends HttpTestCase
{
    private Connection $db;

    private const PREVIEW = [
        'domain' => 'old.test', 'source' => '/var/www/old', 'target' => '/home/old/old.test/public_html', 'user' => 'old',
        'steps' => [['file' => '/etc/nginx/sites-available/old', 'action' => 'unlink', 'path' => '/etc/nginx/sites-enabled/old', 'names' => ['old.test']]],
        'files' => 12, 'bytes' => 4096, 'skipped_links' => [], 'database' => 'olddb', 'database_source' => 'wp-config.php',
        'new_config' => 'server { # whacp }', 'old_config' => 'server { root /var/www/old; }', 'blockers' => [], 'warnings' => [],
    ];

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('discovery.inventory', ['websites' => ['websites' => [[
            'id' => 'abc123', 'domain' => 'old.test', 'aliases' => ['www.old.test'], 'enabled' => true, 'kind' => 'php',
            'document_root' => '/var/www/old', 'document_root_exists' => true, 'https' => false, 'https_redirect' => false,
            'php' => ['version' => '8.3', 'pool' => 'www', 'listen' => 'unix:/run/php/php8.3-fpm.sock', 'user' => 'www-data', 'resolved' => true],
            'ssl' => null, 'listen' => ['80'], 'config_files' => ['/etc/nginx/sites-available/old'], 'access_logs' => [], 'error_logs' => [],
            'managed_by' => '', 'proxy_pass' => '',
        ]], 'catch_all_vhosts' => 0, 'warnings' => []], 'cron' => ['jobs' => [], 'warnings' => []]]);
        $this->agent->succeed('adopt.preview', self::PREVIEW);
    }

    public function testPreviewShowsThePlan(): void
    {
        $body = $this->request('GET', '/websites/abc123')->body();
        self::assertStringContainsString('Import into NimVor', $body);
        $body = $this->request('GET', '/websites/abc123/import')->body();
        self::assertStringContainsString('/etc/nginx/sites-enabled/old', $body);
        self::assertStringContainsString('olddb', $body);
        self::assertStringContainsString('server { # whacp }', $body);
        $call = $this->lastCall('adopt.preview');
        self::assertSame('/var/www/old', $call['source']);
        self::assertSame('old.test', $call['spec']['domain']);
        self::assertContains('www.old.test', $call['spec']['aliases']);
        self::assertSame('8.3', $call['spec']['php_version']);
    }

    public function testBlockersPreventTheImport(): void
    {
        $this->agent->succeed('adopt.preview', ['blockers' => ['The configuration file also serves other.test.']] + self::PREVIEW);
        $body = $this->request('GET', '/websites/abc123/import')->body();
        self::assertStringContainsString('cannot run yet', $body);
        $this->post('/websites/abc123/import', ['confirm' => '1']);
        self::assertStringContainsString('blocked', (string) $this->flash('error'));
        self::assertSame(0, $this->calls('adopt.run'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM websites'));
    }

    public function testRunFinishAndUndo(): void
    {
        $this->post('/websites/abc123/import', []);
        self::assertSame(0, $this->calls('adopt.run'), 'the plan must be confirmed');

        $this->agent->succeed('adopt.run', ['job_id' => 'cccccccccccccccc']);
        $this->post('/websites/abc123/import', ['confirm' => '1', 'system_user' => 'oldsite']);
        self::assertSame('oldsite', $this->lastCall('adopt.run')['spec']['user']);
        $id = (int) $this->db->scalar('SELECT id FROM websites');
        self::assertSame('importing', $this->db->scalar('SELECT status FROM websites'));

        // Still running: the overview says so.
        $this->agent->succeed('jobs.get', ['id' => 'cccccccccccccccc', 'status' => 'running']);
        self::assertStringContainsString('Importing from', $this->request('GET', '/sites/' . $id)->body());

        // Done: the website is active and the detected database is tracked.
        $this->agent->succeed('jobs.get', ['id' => 'cccccccccccccccc', 'status' => 'succeeded']);
        $this->agent->succeed('db.overview', ['databases' => [['name' => 'olddb']]]);
        self::assertStringContainsString('Undo import', $this->request('GET', '/sites/' . $id)->body());
        self::assertSame('active', $this->db->scalar('SELECT status FROM websites'));
        self::assertSame($id, (int) $this->db->scalar("SELECT website_id FROM databases WHERE name = 'olddb'"));

        $this->agent->succeed('adopt.undo', ['ok' => true]);
        $this->agent->succeed('sites.delete', ['ok' => true]);
        $this->post('/sites/' . $id . '/import/undo', []);
        self::assertSame('/var/www/old', $this->lastCall('adopt.undo')['source']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM websites'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM databases'));
        self::assertSame('undone', $this->db->scalar('SELECT status FROM website_imports'));
    }

    public function testFailedImportRemovesTheNewWebsite(): void
    {
        $this->agent->succeed('adopt.run', ['job_id' => 'dddddddddddddddd']);
        $this->post('/websites/abc123/import', ['confirm' => '1']);
        $id = (int) $this->db->scalar('SELECT id FROM websites');
        $this->agent->succeed('jobs.get', ['id' => 'dddddddddddddddd', 'status' => 'failed', 'error' => 'nginx -t failed']);
        $this->agent->succeed('sites.delete', ['ok' => true]);
        $res = $this->request('GET', '/sites/' . $id);
        self::assertSame(303, $res->status());
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM websites'));
        self::assertSame('failed', $this->db->scalar('SELECT status FROM website_imports'));
    }

    public function testUsersWithoutPermissionCannotImport(): void
    {
        $this->seedUser('client', 'customer');
        $this->login('client');
        self::assertSame(403, $this->request('GET', '/websites/abc123/import')->status());
        self::assertSame(403, $this->post('/websites/abc123/import', ['confirm' => '1'])->status());
    }
}
