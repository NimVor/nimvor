<?php

declare(strict_types=1);

namespace WhaCP\Security;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AuditLogger;

/**
 * Security keys and passkeys (WebAuthn) as a second sign-in factor.
 *
 * Registration uses attestation "none": the key proves possession, not
 * its make. Supported algorithms: ES256, RS256 and EdDSA. Each ceremony
 * uses a one-time random challenge kept in the session for five minutes;
 * the origin and relying-party ID must match the panel's address, user
 * presence is required, and a signature counter that goes backwards is
 * refused (a cloned key).
 */
final class WebAuthn
{
    private const CHALLENGE_KEY = '_webauthn_challenge';
    private const TTL = 300;
    public const MAX_KEYS = 10;

    public function __construct(
        private readonly Connection $db,
        private readonly SessionInterface $session,
        private readonly AuditLogger $audit,
    ) {
    }

    public static function b64u(string $bin): string
    {
        return rtrim(strtr(base64_encode($bin), '+/', '-_'), '=');
    }

    public static function unb64u(string $s): string
    {
        $bin = base64_decode(strtr($s, '-_', '+/') . str_repeat('=', (4 - strlen($s) % 4) % 4), true);
        if ($bin === false) {
            throw new WebAuthnException('Invalid encoding.');
        }
        return $bin;
    }

    // ---- stored keys ------------------------------------------------------

    /** @return list<array<string, mixed>> */
    public function credentials(int $userId): array
    {
        return $this->db->fetchAll('SELECT id, name, created_at, last_used_at FROM webauthn_credentials WHERE user_id = ? ORDER BY id', [$userId]);
    }

    public function has(int $userId): bool
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?', [$userId]) > 0;
    }

    public function delete(int $userId, int $id): bool
    {
        $row = $this->db->fetch('SELECT name FROM webauthn_credentials WHERE id = ? AND user_id = ?', [$id, $userId]);
        if ($row === null) {
            return false;
        }
        $this->db->run('DELETE FROM webauthn_credentials WHERE id = ?', [$id]);
        $this->audit->record('auth.webauthn_remove', 'user', (string) $userId, AuditLogger::SUCCESS, ['name' => $row['name']]);
        return true;
    }

    public function deleteAll(int $userId): void
    {
        $this->db->run('DELETE FROM webauthn_credentials WHERE user_id = ?', [$userId]);
    }

    // ---- ceremonies ---------------------------------------------------------

    private function challenge(string $purpose, int $userId): string
    {
        $c = random_bytes(32);
        $this->session->set(self::CHALLENGE_KEY, ['purpose' => $purpose, 'user' => $userId, 'challenge' => self::b64u($c), 'at' => time()]);
        return self::b64u($c);
    }

    private function takeChallenge(string $purpose, int $userId): string
    {
        $c = $this->session->get(self::CHALLENGE_KEY);
        $this->session->remove(self::CHALLENGE_KEY);
        if (!is_array($c) || ($c['purpose'] ?? '') !== $purpose || (int) ($c['user'] ?? 0) !== $userId || time() - (int) ($c['at'] ?? 0) > self::TTL) {
            throw new WebAuthnException('The request expired. Try again.');
        }
        return (string) $c['challenge'];
    }

    /**
     * Options for navigator.credentials.create().
     *
     * @param array<string, mixed> $user
     * @return array<string, mixed>
     */
    public function registrationOptions(array $user, string $rpId, string $rpName): array
    {
        $exclude = array_map(static fn (array $r): array => ['type' => 'public-key', 'id' => $r['credential_id']], $this->db->fetchAll('SELECT credential_id FROM webauthn_credentials WHERE user_id = ?', [(int) $user['id']]));
        return [
            'challenge' => $this->challenge('register', (int) $user['id']),
            'rp' => ['id' => $rpId, 'name' => $rpName],
            'user' => ['id' => self::b64u(hash('sha256', 'whacp-user|' . $user['id'], true)), 'name' => (string) $user['username'], 'displayName' => (string) ($user['name'] ?? $user['username'])],
            'pubKeyCredParams' => [['type' => 'public-key', 'alg' => -7], ['type' => 'public-key', 'alg' => -8], ['type' => 'public-key', 'alg' => -257]],
            'timeout' => 120000,
            'attestation' => 'none',
            'authenticatorSelection' => ['userVerification' => 'preferred', 'residentKey' => 'discouraged'],
            'excludeCredentials' => $exclude,
        ];
    }

    /**
     * Verify navigator.credentials.create() output and store the key.
     *
     * @param array<string, mixed> $user
     */
    public function register(array $user, string $origin, string $rpId, string $name, string $clientDataB64, string $attestationB64): void
    {
        $userId = (int) $user['id'];
        $challenge = $this->takeChallenge('register', $userId);
        $name = trim($name);
        if ($name === '' || mb_strlen($name) > 60) {
            throw new WebAuthnException('Give the key a name (up to 60 characters).');
        }
        if ((int) $this->db->scalar('SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?', [$userId]) >= self::MAX_KEYS) {
            throw new WebAuthnException('You can register at most ' . self::MAX_KEYS . ' keys.');
        }
        $clientData = self::unb64u($clientDataB64);
        $this->checkClientData($clientData, 'webauthn.create', $challenge, $origin);
        try {
            [$att] = Cbor::decode(self::unb64u($attestationB64));
        } catch (\InvalidArgumentException) {
            throw new WebAuthnException('The key sent an unreadable response.');
        }
        if (!is_array($att) || !isset($att['authData']) || !is_string($att['authData'])) {
            throw new WebAuthnException('The key sent an unreadable response.');
        }
        $auth = self::parseAuthData($att['authData'], $rpId, true);
        $credId = self::b64u($auth['credential_id']);
        if ($this->db->scalar('SELECT COUNT(*) FROM webauthn_credentials WHERE credential_id = ?', [$credId]) > 0) {
            throw new WebAuthnException('This key is registered already.');
        }
        $this->db->run(
            'INSERT INTO webauthn_credentials (user_id, credential_id, public_key, sign_count, name, created_at) VALUES (?, ?, ?, ?, ?, ?)',
            [$userId, $credId, self::coseToKey($auth['public_key']), $auth['sign_count'], $name, gmdate('c')]
        );
        $this->audit->record('auth.webauthn_add', 'user', (string) $userId, AuditLogger::SUCCESS, ['name' => $name]);
    }

    /**
     * Options for navigator.credentials.get() during sign-in.
     *
     * @return array<string, mixed>
     */
    public function loginOptions(int $userId, string $rpId): array
    {
        $allow = array_map(static fn (array $r): array => ['type' => 'public-key', 'id' => $r['credential_id']], $this->db->fetchAll('SELECT credential_id FROM webauthn_credentials WHERE user_id = ?', [$userId]));
        return ['challenge' => $this->challenge('login', $userId), 'rpId' => $rpId, 'allowCredentials' => $allow, 'timeout' => 120000, 'userVerification' => 'preferred'];
    }

    /** Verify navigator.credentials.get() output; returns the key's name. */
    public function verify(int $userId, string $origin, string $rpId, string $credIdB64, string $clientDataB64, string $authDataB64, string $signatureB64): string
    {
        $challenge = $this->takeChallenge('login', $userId);
        $row = $this->db->fetch('SELECT * FROM webauthn_credentials WHERE user_id = ? AND credential_id = ?', [$userId, $credIdB64]);
        if ($row === null) {
            throw new WebAuthnException('This key is not registered for your account.');
        }
        $clientData = self::unb64u($clientDataB64);
        $this->checkClientData($clientData, 'webauthn.get', $challenge, $origin);
        $authData = self::unb64u($authDataB64);
        $auth = self::parseAuthData($authData, $rpId, false);
        $signed = $authData . hash('sha256', $clientData, true);
        if (!self::verifySignature((string) $row['public_key'], $signed, self::unb64u($signatureB64))) {
            throw new WebAuthnException('The key\'s signature is not valid.');
        }
        $stored = (int) $row['sign_count'];
        if (($auth['sign_count'] !== 0 || $stored !== 0) && $auth['sign_count'] <= $stored) {
            $this->audit->record('auth.webauthn_clone', 'user', (string) $userId, AuditLogger::DENIED, ['name' => $row['name'], 'counter' => $auth['sign_count'], 'stored' => $stored]);
            throw new WebAuthnException('This key\'s counter went backwards, which suggests a copied key. It was refused; contact an administrator.');
        }
        $this->db->run('UPDATE webauthn_credentials SET sign_count = ?, last_used_at = ? WHERE id = ?', [$auth['sign_count'], gmdate('c'), $row['id']]);
        return (string) $row['name'];
    }

    // ---- verification helpers ---------------------------------------------

    private function checkClientData(string $json, string $type, string $challenge, string $origin): void
    {
        $cd = json_decode($json, true);
        if (!is_array($cd) || ($cd['type'] ?? '') !== $type) {
            throw new WebAuthnException('The key sent an unexpected response.');
        }
        if (!hash_equals($challenge, (string) ($cd['challenge'] ?? ''))) {
            throw new WebAuthnException('The request expired. Try again.');
        }
        if (($cd['origin'] ?? '') !== $origin) {
            throw new WebAuthnException('The key answered for a different address (' . substr((string) ($cd['origin'] ?? ''), 0, 100) . ').');
        }
        if (!empty($cd['crossOrigin'])) {
            throw new WebAuthnException('Cross-origin use is not allowed.');
        }
    }

    /** @return array{sign_count: int, credential_id: string, public_key: array<int|string, mixed>} */
    public static function parseAuthData(string $d, string $rpId, bool $attested): array
    {
        if (strlen($d) < 37) {
            throw new WebAuthnException('The key sent an unreadable response.');
        }
        if (!hash_equals(hash('sha256', $rpId, true), substr($d, 0, 32))) {
            throw new WebAuthnException('The key answered for a different site.');
        }
        $flags = ord($d[32]);
        if (($flags & 0x01) === 0) {
            throw new WebAuthnException('Touch the key to confirm.');
        }
        $count = unpack('N', substr($d, 33, 4))[1];
        $out = ['sign_count' => (int) $count, 'credential_id' => '', 'public_key' => []];
        if ($attested) {
            if (($flags & 0x40) === 0 || strlen($d) < 55) {
                throw new WebAuthnException('The key did not send its public key.');
            }
            $idLen = unpack('n', substr($d, 53, 2))[1];
            $out['credential_id'] = substr($d, 55, $idLen);
            if (strlen($out['credential_id']) !== $idLen || $idLen < 16 || $idLen > 1023) {
                throw new WebAuthnException('The key sent an unreadable response.');
            }
            try {
                [$key] = Cbor::decode($d, 55 + $idLen);
            } catch (\InvalidArgumentException) {
                throw new WebAuthnException('The key sent an unreadable public key.');
            }
            if (!is_array($key)) {
                throw new WebAuthnException('The key sent an unreadable public key.');
            }
            $out['public_key'] = $key;
        }
        return $out;
    }

    /**
     * COSE public key → stored form: PEM (ES256, RS256) or "ed25519:<b64>".
     *
     * @param array<int|string, mixed> $k
     */
    public static function coseToKey(array $k): string
    {
        $kty = $k[1] ?? null;
        $alg = $k[3] ?? null;
        if ($kty === 2 && $alg === -7 && ($k[-1] ?? null) === 1 && is_string($k[-2] ?? null) && is_string($k[-3] ?? null) && strlen($k[-2]) === 32 && strlen($k[-3]) === 32) {
            $der = hex2bin('3059301306072a8648ce3d020106082a8648ce3d030107034200') . "\x04" . $k[-2] . $k[-3];
            return self::pem($der);
        }
        if ($kty === 3 && $alg === -257 && is_string($k[-1] ?? null) && is_string($k[-2] ?? null) && strlen($k[-1]) >= 256) {
            $rsa = self::der(0x30, self::der(0x02, self::uint($k[-1])) . self::der(0x02, self::uint($k[-2])));
            $algId = self::der(0x30, hex2bin('06092a864886f70d0101010500'));
            return self::pem(self::der(0x30, $algId . self::der(0x03, "\x00" . $rsa)));
        }
        if ($kty === 1 && $alg === -8 && ($k[-1] ?? null) === 6 && is_string($k[-2] ?? null) && strlen($k[-2]) === 32) {
            return 'ed25519:' . base64_encode($k[-2]);
        }
        throw new WebAuthnException('This key uses an algorithm WhaCP does not support (ES256, RS256 and Ed25519 are).');
    }

    public static function verifySignature(string $key, string $data, string $sig): bool
    {
        if (str_starts_with($key, 'ed25519:')) {
            $pk = base64_decode(substr($key, 8), true);
            return $pk !== false && strlen($pk) === SODIUM_CRYPTO_SIGN_PUBLICKEYBYTES && strlen($sig) === SODIUM_CRYPTO_SIGN_BYTES && sodium_crypto_sign_verify_detached($sig, $data, $pk);
        }
        return openssl_verify($data, $sig, $key, OPENSSL_ALGO_SHA256) === 1;
    }

    private static function uint(string $b): string
    {
        $b = ltrim($b, "\x00");
        return (ord($b[0] ?? "\x00") & 0x80) ? "\x00" . $b : $b;
    }

    private static function der(int $tag, string $body): string
    {
        $n = strlen($body);
        if ($n < 128) {
            $len = chr($n);
        } else {
            $bytes = ltrim(pack('N', $n), "\x00");
            $len = chr(0x80 | strlen($bytes)) . $bytes;
        }
        return chr($tag) . $len . $body;
    }

    private static function pem(string $der): string
    {
        return "-----BEGIN PUBLIC KEY-----\n" . chunk_split(base64_encode($der), 64, "\n") . "-----END PUBLIC KEY-----\n";
    }
}
