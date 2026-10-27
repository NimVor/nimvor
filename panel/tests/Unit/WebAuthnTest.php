<?php

declare(strict_types=1);

namespace WhaCP\Tests\Unit;

use WhaCP\Http\Session\ArraySession;
use WhaCP\Security\Cbor;
use WhaCP\Security\WebAuthn;
use WhaCP\Security\WebAuthnException;
use WhaCP\Tests\Support\DatabaseTestCase;
use WhaCP\Tests\Support\SoftAuthenticator;

require_once __DIR__ . '/../Support/SoftAuthenticator.php';

final class WebAuthnTest extends DatabaseTestCase
{
    private const ORIGIN = 'https://panel.example.com';
    private const RP = 'panel.example.com';
    private WebAuthn $wa;
    private ArraySession $session;
    /** @var array<string, mixed> */
    private array $user;

    protected function setUp(): void
    {
        parent::setUp();
        $this->session = new ArraySession();
        $this->wa = new WebAuthn($this->db, $this->session, $this->audit);
        $this->user = (array) $this->users->find($this->createUser('alice'));
    }

    private function register(SoftAuthenticator $key, string $name = 'Key'): void
    {
        $opts = $this->wa->registrationOptions($this->user, self::RP, 'NimVor');
        $r = $key->create($opts, self::ORIGIN);
        $this->wa->register($this->user, self::ORIGIN, self::RP, $name, $r['client_data'], $r['attestation']);
    }

    private function login(SoftAuthenticator $key, string $origin = self::ORIGIN, int $flags = 0x05): string
    {
        $opts = $this->wa->loginOptions((int) $this->user['id'], self::RP);
        $a = $key->get($opts, $origin, $flags);
        return $this->wa->verify((int) $this->user['id'], self::ORIGIN, self::RP, $a['id'], $a['client_data'], $a['authenticator_data'], $a['signature']);
    }

    public function testRegisterAndSignInWithEachAlgorithm(): void
    {
        foreach (['ES256', 'RS256', 'EdDSA'] as $alg) {
            $key = new SoftAuthenticator($alg);
            $this->register($key, $alg . ' key');
            self::assertSame($alg . ' key', $this->login($key), $alg);
            self::assertSame($alg . ' key', $this->login($key), $alg . ' twice');
        }
        self::assertCount(3, $this->wa->credentials((int) $this->user['id']));
        self::assertTrue($this->wa->has((int) $this->user['id']));
    }

    public function testRefusals(): void
    {
        $key = new SoftAuthenticator();
        // Wrong origin at registration.
        $opts = $this->wa->registrationOptions($this->user, self::RP, 'NimVor');
        $r = $key->create($opts, 'https://evil.example');
        $this->assertFails(fn () => $this->wa->register($this->user, self::ORIGIN, self::RP, 'k', $r['client_data'], $r['attestation']), 'different address');
        // Credential made for another relying party.
        $opts = $this->wa->registrationOptions($this->user, self::RP, 'NimVor');
        $r = $key->create($opts, self::ORIGIN, 'evil.example');
        $this->assertFails(fn () => $this->wa->register($this->user, self::ORIGIN, self::RP, 'k', $r['client_data'], $r['attestation']), 'different site');
        // Challenge is single use.
        $opts = $this->wa->registrationOptions($this->user, self::RP, 'NimVor');
        $r = $key->create($opts, self::ORIGIN);
        $this->wa->register($this->user, self::ORIGIN, self::RP, 'k', $r['client_data'], $r['attestation']);
        $this->assertFails(fn () => $this->wa->register($this->user, self::ORIGIN, self::RP, 'k2', $r['client_data'], $r['attestation']), 'expired');

        // Sign-in: phishing origin, missing user presence, replayed/cloned counter, bad signature.
        $this->assertFails(fn () => $this->login($key, 'https://evil.example'), 'different address');
        $this->assertFails(fn () => $this->login($key, self::ORIGIN, 0x00), 'Touch the key');
        $this->login($key);
        $key->counter -= 2;
        $this->assertFails(fn () => $this->login($key), 'copied key');
        $key->counter += 5;
        $opts = $this->wa->loginOptions((int) $this->user['id'], self::RP);
        $a = $key->get($opts, self::ORIGIN);
        $sig = WebAuthn::unb64u($a['signature']);
        $sig[10] = chr(ord($sig[10]) ^ 1);
        $this->assertFails(fn () => $this->wa->verify((int) $this->user['id'], self::ORIGIN, self::RP, $a['id'], $a['client_data'], $a['authenticator_data'], WebAuthn::b64u($sig)), 'signature');
        // Another user's key cannot sign in as alice.
        $other = new SoftAuthenticator();
        $this->assertFails(fn () => $this->login($other), 'not registered');
    }

    public function testCborDecoder(): void
    {
        [$v] = Cbor::decode(SoftAuthenticator::cbor(['a' => [1 => -7, -2 => 'bytes'], 'n' => 70000]));
        self::assertSame(['a' => [1 => -7, -2 => 'bytes'], 'n' => 70000], $v);
        foreach (["\x5f", "\x9f", "\xc1\x00", "\x42a", ''] as $bad) {
            try {
                Cbor::decode($bad);
                self::fail('accepted ' . bin2hex($bad));
            } catch (\InvalidArgumentException) {
            }
        }
    }

    private function assertFails(callable $fn, string $message): void
    {
        try {
            $fn();
            self::fail('expected failure: ' . $message);
        } catch (WebAuthnException $e) {
            self::assertStringContainsString($message, $e->getMessage());
        }
    }
}
