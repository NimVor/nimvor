<?php

declare(strict_types=1);

namespace NimVor\Tests\Integration;

use NimVor\Services\UserService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\Tests\Support\DatabaseTestCase;

final class UserServiceTest extends DatabaseTestCase
{
    private UserService $service;

    protected function setUp(): void
    {
        parent::setUp();
        $this->service = new UserService($this->users, $this->roles, $this->hasher, $this->audit);
    }

    /** @return array<string, mixed> */
    private function input(array $overrides = []): array
    {
        return $overrides + [
            'username' => 'carol',
            'email' => 'carol@example.test',
            'display_name' => 'Carol',
            'password' => 'a-secure-passphrase',
            'password_confirmation' => 'a-secure-passphrase',
            'role_id' => $this->roleId('operator'),
            'is_active' => '1',
        ];
    }

    public function testCreateStoresHashedPasswordAndAudits(): void
    {
        $id = $this->service->create($this->input());
        $hash = $this->users->passwordHash($id);
        self::assertNotSame('a-secure-passphrase', $hash);
        self::assertTrue($this->hasher->verify('a-secure-passphrase', (string) $hash));
        $audit = $this->auditRows();
        self::assertSame('user.create', $audit[0]['action']);
        self::assertStringNotContainsString('a-secure-passphrase', (string) $audit[0]['details']);
    }

    /** @return iterable<string, array{array<string, mixed>, string}> */
    public static function invalidInputs(): iterable
    {
        yield 'bad username' => [['username' => 'Bad Name'], 'username'];
        yield 'short username' => [['username' => 'ab'], 'username'];
        yield 'bad email' => [['email' => 'not-an-email'], 'email'];
        yield 'weak password' => [['password' => 'short', 'password_confirmation' => 'short'], 'password'];
        yield 'mismatch' => [['password_confirmation' => 'something-else-entirely'], 'password_confirmation'];
        yield 'bad role' => [['role_id' => 999], 'role_id'];
        yield 'control chars' => [['display_name' => "a\x07b"], 'display_name'];
    }

    /** @param array<string, mixed> $override */
    #[\PHPUnit\Framework\Attributes\DataProvider('invalidInputs')]
    public function testValidation(array $override, string $field): void
    {
        try {
            $this->service->create($this->input($override));
            self::fail('expected validation error');
        } catch (ValidationException $e) {
            self::assertArrayHasKey($field, $e->errors());
        }
    }

    public function testDuplicateUsernameAndEmailRejected(): void
    {
        $this->service->create($this->input());
        try {
            $this->service->create($this->input(['email' => 'other@example.test']));
            self::fail('expected duplicate username');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('username', $e->errors());
        }
        try {
            $this->service->create($this->input(['username' => 'carol2', 'email' => 'CAROL@example.test']));
            self::fail('expected duplicate email');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('email', $e->errors());
        }
    }

    public function testLastAdministratorIsProtected(): void
    {
        $admin = $this->createUser('root-admin', 'admin');
        $other = $this->createUser('ops', 'operator');

        try {
            $this->service->update($admin, ['email' => 'a@example.test', 'role_id' => $this->roleId('viewer'), 'is_active' => true], $other);
            self::fail('demoting the last admin must fail');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('role_id', $e->errors());
        }
        $this->expectException(UserFacingException::class);
        $this->service->delete($admin, $other);
    }

    public function testCannotDeleteOrDisableSelf(): void
    {
        $a = $this->createUser('admin1', 'admin');
        $this->createUser('admin2', 'admin');
        try {
            $this->service->update($a, ['email' => 'x@example.test', 'role_id' => $this->roleId('admin'), 'is_active' => false], $a);
            self::fail('self-disable must fail');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('is_active', $e->errors());
        }
        $this->expectException(UserFacingException::class);
        $this->service->delete($a, $a);
    }

    public function testAdminCanBeDemotedWhenAnotherAdminExists(): void
    {
        $a = $this->createUser('admin1', 'admin');
        $b = $this->createUser('admin2', 'admin');
        $this->service->update($a, ['email' => 'x@example.test', 'role_id' => $this->roleId('viewer'), 'is_active' => true], $b);
        self::assertSame('viewer', $this->users->find($a)['role_name']);
        $this->service->delete($a, $b);
        self::assertNull($this->users->find($a));
    }

    public function testChangeOwnPassword(): void
    {
        $id = $this->createUser('dave', 'viewer', 'dave-old-password');
        try {
            $this->service->changeOwnPassword($id, 'wrong', 'dave-new-password!', 'dave-new-password!');
            self::fail('wrong current password must fail');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('current_password', $e->errors());
        }
        $this->service->changeOwnPassword($id, 'dave-old-password', 'a-completely-new-one', 'a-completely-new-one');
        self::assertTrue($this->hasher->verify('a-completely-new-one', (string) $this->users->passwordHash($id)));
    }
}
