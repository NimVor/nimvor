<?php

declare(strict_types=1);

namespace WhaCP\Tests\Unit;

use PHPUnit\Framework\TestCase;
use WhaCP\Http\Response;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Security\Csrf;
use WhaCP\Security\PasswordHasher;
use WhaCP\Support\Redactor;

final class SecurityPrimitivesTest extends TestCase
{
    public function testCsrfTokenIsStableAndValidated(): void
    {
        $csrf = new Csrf(new ArraySession());
        $token = $csrf->token();
        self::assertSame(64, strlen($token));
        self::assertSame($token, $csrf->token());
        self::assertTrue($csrf->validate($token));
        self::assertFalse($csrf->validate(''));
        self::assertFalse($csrf->validate(null));
        self::assertFalse($csrf->validate(strrev($token)));
        $csrf->rotate();
        self::assertFalse($csrf->validate($token));
    }

    public function testCsrfFailsWithoutSessionToken(): void
    {
        self::assertFalse((new Csrf(new ArraySession()))->validate('anything'));
    }

    public function testPasswordHashing(): void
    {
        $h = new PasswordHasher();
        $hash = $h->hash('correct horse battery');
        self::assertTrue($h->verify('correct horse battery', $hash));
        self::assertFalse($h->verify('wrong', $hash));
        self::assertFalse($h->needsRehash($hash));
        self::assertNotNull($h->weakness('short'));
        self::assertNotNull($h->weakness('aaaaaaaaaaaaaaa'));
        self::assertNotNull($h->weakness('adminadmin12345', 'admin'));
        self::assertNull($h->weakness('a sufficiently long passphrase', 'admin'));
    }

    public function testRedactorRemovesSecrets(): void
    {
        $out = Redactor::redact([
            'password' => 'hunter2',
            'api_key' => 'k',
            'nested' => ['token' => 't', 'ok' => 'value'],
            'command' => 'mysqldump -pS3cret db',
            'url' => 'https://user:pa55@example.com/x',
            'plain' => 42,
        ]);
        $json = (string) json_encode($out);
        foreach (['hunter2', 'S3cret', 'pa55'] as $secret) {
            self::assertStringNotContainsString($secret, $json);
        }
        self::assertSame(Redactor::PLACEHOLDER, $out['password']);
        self::assertSame(Redactor::PLACEHOLDER, $out['nested']['token']);
        self::assertSame('value', $out['nested']['ok']);
        self::assertSame(42, $out['plain']);
    }

    public function testRedirectsAreSameOriginOnly(): void
    {
        self::assertSame('/users', Response::redirect('/users')->header('Location'));
        foreach (['https://evil.test/', '//evil.test', '/\\evil.test', 'javascript:alert(1)'] as $bad) {
            self::assertSame('/', Response::redirect($bad)->header('Location'), $bad);
        }
    }
}
