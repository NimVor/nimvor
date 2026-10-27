<?php

declare(strict_types=1);

namespace WhaCP\Security;

use RuntimeException;

/**
 * Authenticated encryption (libsodium secretbox) for secrets the panel must
 * keep: remote agent tokens, backup keys and — only when a user explicitly
 * asks — database passwords. The key lives in a file readable only by the
 * panel (default /etc/whacp/panel.key), never in the database.
 */
final class Crypto
{
    private ?string $key = null;

    public function __construct(private readonly string $keyFile)
    {
    }

    public function encrypt(string $plain): string
    {
        $nonce = random_bytes(SODIUM_CRYPTO_SECRETBOX_NONCEBYTES);
        return 'v1:' . base64_encode($nonce . sodium_crypto_secretbox($plain, $nonce, $this->key()));
    }

    public function decrypt(string $encoded): string
    {
        if (!str_starts_with($encoded, 'v1:')) {
            throw new RuntimeException('Unknown secret format');
        }
        $raw = base64_decode(substr($encoded, 3), true);
        if ($raw === false || strlen($raw) <= SODIUM_CRYPTO_SECRETBOX_NONCEBYTES) {
            throw new RuntimeException('Corrupted secret');
        }
        $plain = sodium_crypto_secretbox_open(
            substr($raw, SODIUM_CRYPTO_SECRETBOX_NONCEBYTES),
            substr($raw, 0, SODIUM_CRYPTO_SECRETBOX_NONCEBYTES),
            $this->key(),
        );
        if ($plain === false) {
            throw new RuntimeException('Secret cannot be decrypted with the current key');
        }
        return $plain;
    }

    /** Random password of printable ASCII without quotes, backslashes or spaces. */
    public static function password(int $length = 24): string
    {
        $alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.!@#%+=';
        $out = '';
        $max = strlen($alphabet) - 1;
        for ($i = 0; $i < $length; $i++) {
            $out .= $alphabet[random_int(0, $max)];
        }
        return $out;
    }

    private function key(): string
    {
        if ($this->key !== null) {
            return $this->key;
        }
        if (!is_file($this->keyFile)) {
            $dir = dirname($this->keyFile);
            if (!is_dir($dir) || !is_writable($dir)) {
                throw new RuntimeException('The encryption key file is missing: ' . $this->keyFile);
            }
            $old = umask(0077);
            file_put_contents($this->keyFile, base64_encode(random_bytes(SODIUM_CRYPTO_SECRETBOX_KEYBYTES)) . "\n", LOCK_EX);
            umask($old);
        }
        $key = base64_decode(trim((string) file_get_contents($this->keyFile)), true);
        if ($key === false || strlen($key) !== SODIUM_CRYPTO_SECRETBOX_KEYBYTES) {
            throw new RuntimeException('The encryption key file is invalid');
        }
        return $this->key = $key;
    }
}
