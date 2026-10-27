<?php

declare(strict_types=1);

namespace WhaCP\Tests\Support;

use WhaCP\Security\WebAuthn;

/**
 * A software WebAuthn authenticator for tests: creates credentials and
 * signs assertions like a security key (ES256, RS256 or Ed25519).
 */
final class SoftAuthenticator
{
    public string $credentialId;
    public int $counter = 0;
    private \OpenSSLAsymmetricKey|string $key;

    public function __construct(public readonly string $alg = 'ES256')
    {
        $this->credentialId = random_bytes(32);
        $this->key = match ($alg) {
            'ES256' => openssl_pkey_new(['private_key_type' => OPENSSL_KEYTYPE_EC, 'curve_name' => 'prime256v1']),
            'RS256' => openssl_pkey_new(['private_key_type' => OPENSSL_KEYTYPE_RSA, 'private_key_bits' => 2048]),
            default => sodium_crypto_sign_keypair(),
        };
    }

    public static function cbor(mixed $v): string
    {
        $head = static function (int $major, int $n): string {
            if ($n < 24) {
                return chr(($major << 5) | $n);
            }
            if ($n < 256) {
                return chr(($major << 5) | 24) . chr($n);
            }
            if ($n < 65536) {
                return chr(($major << 5) | 25) . pack('n', $n);
            }
            return chr(($major << 5) | 26) . pack('N', $n);
        };
        if (is_int($v)) {
            return $v >= 0 ? $head(0, $v) : $head(1, -1 - $v);
        }
        if (is_string($v)) {
            return $head(2, strlen($v)) . $v; // byte string
        }
        if ($v instanceof CborText) {
            return $head(3, strlen($v->s)) . $v->s;
        }
        if (is_array($v)) {
            $out = $head(5, count($v));
            foreach ($v as $k => $val) {
                $out .= (is_int($k) ? self::cbor($k) : self::cbor(new CborText($k))) . self::cbor($val);
            }
            return $out;
        }
        throw new \LogicException('unsupported');
    }

    /** @return array<int, mixed> */
    private function cose(): array
    {
        if ($this->alg === 'ES256') {
            $d = openssl_pkey_get_details($this->key)['ec'];
            // OpenSSL drops leading zero bytes; COSE coordinates are always 32.
            return [1 => 2, 3 => -7, -1 => 1, -2 => str_pad($d['x'], 32, "\0", STR_PAD_LEFT), -3 => str_pad($d['y'], 32, "\0", STR_PAD_LEFT)];
        }
        if ($this->alg === 'RS256') {
            $d = openssl_pkey_get_details($this->key)['rsa'];
            return [1 => 3, 3 => -257, -1 => $d['n'], -2 => $d['e']];
        }
        return [1 => 1, 3 => -8, -1 => 6, -2 => sodium_crypto_sign_publickey($this->key)];
    }

    private function authData(string $rpId, bool $attested, int $flags = 0x05): string
    {
        $d = hash('sha256', $rpId, true) . chr($flags | ($attested ? 0x40 : 0)) . pack('N', $this->counter);
        if ($attested) {
            $d .= str_repeat("\0", 16) . pack('n', strlen($this->credentialId)) . $this->credentialId . self::cbor($this->cose());
        }
        return $d;
    }

    /** @param array<string, mixed> $options @return array{client_data: string, attestation: string} */
    public function create(array $options, string $origin, ?string $rpId = null): array
    {
        $cd = json_encode(['type' => 'webauthn.create', 'challenge' => $options['challenge'], 'origin' => $origin, 'crossOrigin' => false]);
        $att = self::cbor(['fmt' => new CborText('none'), 'attStmt' => [], 'authData' => $this->authData($rpId ?? $options['rp']['id'], true)]);
        return ['client_data' => WebAuthn::b64u((string) $cd), 'attestation' => WebAuthn::b64u($att)];
    }

    /** @param array<string, mixed> $options @return array{id: string, client_data: string, authenticator_data: string, signature: string} */
    public function get(array $options, string $origin, int $flags = 0x05): array
    {
        $this->counter++;
        $cd = (string) json_encode(['type' => 'webauthn.get', 'challenge' => $options['challenge'], 'origin' => $origin]);
        $ad = $this->authData($options['rpId'], false, $flags);
        $data = $ad . hash('sha256', $cd, true);
        if ($this->alg === 'EdDSA') {
            $sig = sodium_crypto_sign_detached($data, sodium_crypto_sign_secretkey($this->key));
        } else {
            openssl_sign($data, $sig, $this->key, OPENSSL_ALGO_SHA256);
        }
        return ['id' => WebAuthn::b64u($this->credentialId), 'client_data' => WebAuthn::b64u($cd), 'authenticator_data' => WebAuthn::b64u($ad), 'signature' => WebAuthn::b64u($sig)];
    }
}

final class CborText
{
    public function __construct(public readonly string $s)
    {
    }
}
