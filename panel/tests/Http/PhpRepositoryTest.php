<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Tests\Support\HttpTestCase;

/** Adding the third-party repository with more PHP versions. */
final class PhpRepositoryTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('oper', 'operator');
        $this->agent->succeed('php.repository.enable', ['job_id' => 'j1', 'repository' => 'ppa:ondrej/php']);
    }

    public function testAdministratorAddsRepositoryAfterConfirming(): void
    {
        $this->login('admin');
        $this->post('/php/repository', []);
        self::assertSame(0, $this->calls('php.repository.enable'), 'added without the confirmation');
        $res = $this->post('/php/repository', ['confirm' => 'yes']);
        self::assertSame('/php', $res->header('Location'));
        self::assertSame(1, $this->calls('php.repository.enable'));
        self::assertSame(1, (int) $this->app->container->get(\WhaCP\Database\Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'php.repository.enable'"));
    }

    public function testOperatorCannotAddRepository(): void
    {
        $this->login('oper');
        $res = $this->post('/php/repository', ['confirm' => 'yes']);
        self::assertContains($res->status(), [302, 403]);
        self::assertSame(0, $this->calls('php.repository.enable'));
    }
}
