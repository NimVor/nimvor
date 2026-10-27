<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use PHPUnit\Framework\TestCase;
use NimVor\Security\TwoFactor;

final class TwoFactorTest extends TestCase
{
    public function testRfc6238Vectors(): void
    {
        $secret = TwoFactor::base32Encode('12345678901234567890');
        self::assertSame('GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', $secret);
        self::assertSame('12345678901234567890', TwoFactor::base32Decode($secret));
        // RFC 6238 appendix B (SHA-1), last six digits.
        foreach ([59 => '287082', 1111111109 => '081804', 1234567890 => '005924', 2000000000 => '279037'] as $t => $code) {
            self::assertSame($code, TwoFactor::code($secret, intdiv($t, 30)), "T={$t}");
        }
    }

    public function testSecretsAndUri(): void
    {
        $s = TwoFactor::newSecret();
        self::assertMatchesRegularExpression('/^[A-Z2-7]{32}$/', $s);
        self::assertNotSame($s, TwoFactor::newSecret());
        self::assertStringStartsWith('otpauth://totp/NimVor%3Aalice?secret=' . $s, TwoFactor::uri($s, 'alice'));
    }
}
