<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Services\JobTracker;
use WhaCP\Tests\Support\HttpTestCase;

final class ScopingTest extends HttpTestCase
{
    private Connection $db;
    private int $reseller;
    private int $customer;
    private int $other;

    protected function setUp(): void
    {
        parent::setUp();
        $this->db = $this->app->container->get(Connection::class);
        $this->reseller = $this->seedUser('res', 'reseller');
        $this->customer = $this->seedUser('cust', 'customer');
        $this->other = $this->seedUser('stranger', 'customer');
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$this->reseller, $this->customer]);
        $now = gmdate('c');
        foreach ([2 => 'edge-1', 3 => 'edge-2'] as $id => $name) {
            $this->db->run("INSERT INTO servers (id, name, kind, url, created_at) VALUES (?, ?, 'remote', 'https://192.0.2.1:8443', ?)", [$id, $name, $now]);
        }
        $this->db->run("INSERT INTO websites (server_id, owner_id, domain, system_user, root, document_root, kind, created_at, updated_at) VALUES (2, ?, 'cust.test', 'cust', '/home/whacp/cust.test', '/home/whacp/cust.test/public_html', 'static', ?, ?)", [$this->customer, $now, $now]);
    }

    public function testServerSwitchIsLimitedToServersWithOwnWebsites(): void
    {
        $this->login('res');
        $this->post('/servers/switch', ['server_id' => '3']);
        self::assertStringContainsString('does not exist', (string) $this->flash('error'));
        $this->post('/servers/switch', ['server_id' => '2']);
        self::assertNull($this->flash('error'), 'a reseller may switch to a server hosting a customer website');

        $this->post('/logout');
        $this->login('stranger');
        $this->post('/servers/switch', ['server_id' => '2']);
        self::assertStringContainsString('does not exist', (string) $this->flash('error'));
        $body = $this->request('GET', '/')->body();
        self::assertStringNotContainsString('edge-1', $body, 'other servers are not listed');
    }

    public function testResellerSeesCustomerJobs(): void
    {
        $jobs = $this->app->container->get(JobTracker::class);
        $jobs->track('aaaaaaaaaaaaaaaa', 'Backup', 'website', '1', $this->customer);
        $job = (array) $jobs->find('aaaaaaaaaaaaaaaa');
        $user = fn (int $id): array => (array) $this->app->container->get(\WhaCP\Repositories\UserRepository::class)->find($id);
        self::assertTrue($jobs->visibleTo($job, $user($this->customer)));
        self::assertTrue($jobs->visibleTo($job, $user($this->reseller)));
        self::assertFalse($jobs->visibleTo($job, $user($this->other)));
    }
}
