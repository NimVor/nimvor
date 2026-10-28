<?php

declare(strict_types=1);

namespace NimVor\Tests\Integration;

use NimVor\Repositories\DatabaseRepository;
use NimVor\Services\DatabaseService;
use NimVor\Services\PlanLimits;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\Tests\Support\PanelHarness;

final class DatabaseServiceTest extends PanelHarness
{
    private DatabaseService $svc;
    private DatabaseRepository $repo;

    protected function setUp(): void
    {
        parent::setUp();
        foreach (['db.create', 'db.user_create', 'db.grant', 'db.drop', 'db.user_password', 'db.user_drop'] as $a) {
            $this->agent->succeed($a, []);
        }
        $this->repo = new DatabaseRepository($this->db);
        $this->svc = new DatabaseService($this->repo, $this->websites, $this->client, $this->audit, $this->server, $this->crypto, new PlanLimits($this->db));
    }

    public function testCreateWithUserReturnsPasswordOnceAndDoesNotStoreIt(): void
    {
        $res = $this->svc->create(['name' => 'shop', 'create_user' => '1'], $this->admin);
        self::assertSame('shop', $res['user']);
        self::assertSame(24, strlen($res['password']));
        self::assertSame($res['password'], $this->lastCall('db.user_create')['password']);
        self::assertSame(['user' => 'shop', 'host' => 'localhost', 'database' => 'shop', 'privileges' => 'all', 'revoke' => false], array_intersect_key($this->lastCall('db.grant'), array_flip(['user', 'host', 'database', 'privileges', 'revoke'])) + ['revoke' => false]);
        $u = $this->repo->user(1, 1, null);
        self::assertSame('', $u['password_enc']);
        self::assertNull($this->svc->revealPassword($u));
        $audit = json_encode($this->auditRows());
        self::assertStringNotContainsString($res['password'], (string) $audit);
    }

    public function testStoredPasswordIsEncryptedAndRevealable(): void
    {
        $res = $this->svc->createUser(['username' => 'app', 'store_password' => '1'], $this->admin);
        $u = $this->repo->user(1, 1, null);
        self::assertStringStartsWith('v1:', $u['password_enc']);
        self::assertStringNotContainsString($res['password'], $u['password_enc']);
        self::assertSame($res['password'], $this->svc->revealPassword($u));
    }

    public function testCustomersArePrefixedAndScoped(): void
    {
        $carol = $this->users->find($this->createUser('carol', 'customer'));
        $dave = $this->users->find($this->createUser('dave', 'customer'));
        $res = $this->svc->create(['name' => 'blog', 'create_user' => '1', 'username' => 'blog'], $carol);
        self::assertSame('carol_blog', $this->lastCall('db.create')['name']);
        self::assertSame('carol_blog', $res['user']);
        self::assertCount(1, $this->repo->databases(1, $carol));
        self::assertCount(0, $this->repo->databases(1, $dave));
        self::assertNull($this->repo->database(1, $res['database_id'], $dave));

        $daveSite = $this->makeSite('dave.test', $dave);
        try {
            $this->svc->create(['name' => 'x', 'website_id' => $daveSite['id']], $carol);
            self::fail('attached to another customer\'s website');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('website_id', $e->errors());
        }
    }

    public function testValidationAndConfirmation(): void
    {
        foreach (['bad-name', 'mysql', str_repeat('a', 65), 'a b'] as $name) {
            try {
                $this->svc->create(['name' => $name], $this->admin);
                self::fail("accepted $name");
            } catch (ValidationException $e) {
                self::assertArrayHasKey('name', $e->errors());
            }
        }
        $this->svc->create(['name' => 'keep'], $this->admin);
        $db = $this->repo->database(1, 1, null);
        try {
            $this->svc->drop($db, 'wrong');
            self::fail('dropped without confirmation');
        } catch (UserFacingException) {
        }
        $this->svc->drop($db, 'keep');
        self::assertNull($this->repo->database(1, 1, null));
    }
}
