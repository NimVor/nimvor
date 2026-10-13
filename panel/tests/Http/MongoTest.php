<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Tests\Support\HttpTestCase;

/** Server → MongoDB: databases and users, admin only. */
final class MongoTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('mongo.status', ['installed' => true, 'running' => true, 'version' => '7.0.14', 'auth' => false]);
        $this->agent->succeed('mongo.overview', [
            'databases' => [['name' => 'shop', 'size' => 8192, 'empty' => false]],
            'users' => [['user' => 'shopuser', 'db' => 'shop', 'roles' => ['readWrite']]],
        ]);
    }

    public function testAdminManagesDatabasesAndUsers(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/mongo')->body();
        foreach (['MongoDB', '7.0.14', 'shop', 'shopuser', 'readWrite'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }

        $this->agent->succeed('mongo.database_create', ['name' => 'blog']);
        $this->post('/mongo/databases', ['name' => 'blog']);
        self::assertSame(['name' => 'blog'], $this->lastCall('mongo.database_create'));

        $this->agent->succeed('mongo.user_create', ['user' => 'u', 'db' => 'blog']);
        $this->post('/mongo/users', ['user' => 'bloguser', 'password' => 'longenough1', 'db' => 'blog', 'role' => 'read']);
        self::assertSame(['user' => 'bloguser', 'password' => 'longenough1', 'db' => 'blog', 'role' => 'read'], $this->lastCall('mongo.user_create'));

        $this->agent->succeed('mongo.database_drop', ['dropped' => 'shop']);
        $this->post('/mongo/databases/drop', ['name' => 'shop', 'confirm' => 'shop']);
        self::assertSame(['name' => 'shop', 'confirm' => 'shop'], $this->lastCall('mongo.database_drop'));

        self::assertSame(3, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'mongo.%'"));
    }

    public function testCustomersCannotReachMongo(): void
    {
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/mongo')->status());
        self::assertSame(403, $this->post('/mongo/databases', ['name' => 'x'])->status());
        self::assertSame(0, $this->calls('mongo.database_create'));
    }

    public function testNotInstalledOffersRepositoryThenInstall(): void
    {
        $this->agent->succeed('mongo.status', ['installed' => false, 'running' => false, 'version' => '', 'auth' => false]);
        $this->agent->succeed('mongo.repository', ['kind' => 'apt-ubuntu', 'name' => 'MongoDB 8.0 (apt)', 'url' => 'https://repo.mongodb.org/apt/ubuntu', 'version' => '8.0', 'supported' => true, 'enabled' => false, 'note' => '']);
        $this->login('admin');
        $page = $this->request('GET', '/mongo')->body();
        self::assertStringContainsString('Install MongoDB', $page);
        self::assertStringContainsString('Add repository', $page);
        self::assertSame(0, $this->calls('mongo.overview'), 'nothing is queried when MongoDB is not installed');

        // Repository must be added before install is offered.
        self::assertStringNotContainsString('action="/mongo/install"', $page, 'install is not offered until the repository is enabled');

        $this->agent->succeed('mongo.repository.enable', ['job_id' => 'cccccccccccccccc']);
        $this->post('/mongo/repository', []);
        self::assertSame(1, $this->calls('mongo.repository.enable'));
    }

    public function testInstallOfferedOnceRepositoryEnabled(): void
    {
        $this->agent->succeed('mongo.status', ['installed' => false, 'running' => false, 'version' => '', 'auth' => false]);
        $this->agent->succeed('mongo.repository', ['kind' => 'apt-ubuntu', 'name' => 'MongoDB 8.0 (apt)', 'url' => '', 'version' => '8.0', 'supported' => true, 'enabled' => true, 'note' => '']);
        $this->login('admin');
        $page = $this->request('GET', '/mongo')->body();
        self::assertStringContainsString('action="/mongo/install"', $page);

        $this->agent->succeed('mongo.install', ['job_id' => 'dddddddddddddddd']);
        $this->post('/mongo/install', []);
        self::assertSame(1, $this->calls('mongo.install'));
    }

    public function testCustomersCannotInstallOrEnableRepository(): void
    {
        $this->login('cust');
        self::assertSame(403, $this->post('/mongo/repository', [])->status());
        self::assertSame(403, $this->post('/mongo/install', [])->status());
        self::assertSame(403, $this->post('/mongo/service', ['op' => 'start'])->status());
        self::assertSame(0, $this->calls('mongo.install'));
    }
}
