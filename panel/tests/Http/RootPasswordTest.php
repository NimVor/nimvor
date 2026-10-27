<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** Root password: administrators, confirmed with their panel password; never logged. */
final class RootPasswordTest extends HttpTestCase
{
    private const NEW = 'Blue-Horse-Staple-42x';

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('op', 'operator');
        $this->agent->succeed('server.root_password', ['changed' => true]);
    }

    private function change(array $input): void
    {
        $this->post('/server/root-password', $input + ['root_password' => self::NEW, 'root_password_confirmation' => self::NEW, 'current_password' => 'a-long-test-password']);
    }

    public function testAdministratorChangesIt(): void
    {
        $this->login('admin');
        foreach ([['current_password' => 'wrong'], ['root_password_confirmation' => 'other-Password-123'], ['root_password' => 'alllowercase-letters', 'root_password_confirmation' => 'alllowercase-letters'], ['root_password' => 'Short1!', 'root_password_confirmation' => 'Short1!']] as $bad) {
            $this->change($bad);
            self::assertNotNull($this->flash('error'), json_encode($bad));
        }
        self::assertSame(0, $this->calls('server.root_password'));
        $this->change([]);
        self::assertSame(['password' => self::NEW], $this->lastCall('server.root_password'));
        self::assertSame('The root password was changed.', $this->flash('success'));
        $audit = (string) json_encode($this->app->container->get(Connection::class)->fetchAll('SELECT * FROM audit_logs'));
        self::assertStringNotContainsString(self::NEW, $audit, 'never in the audit log');
        self::assertStringContainsString('server.root_password', $audit);
    }

    public function testOperatorsCannot(): void
    {
        $this->login('op');
        $this->change([]);
        self::assertSame(0, $this->calls('server.root_password'));
    }
}
