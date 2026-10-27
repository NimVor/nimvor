<?php

declare(strict_types=1);

namespace WhaCP\Security;

/**
 * Minimal CBOR decoder (RFC 8949) for WebAuthn: integers, byte and text
 * strings, arrays, maps and simple values. Byte strings come back as PHP
 * strings; indefinite lengths, tags and floats are not needed and refused.
 */
final class Cbor
{
    /** @return array{0: mixed, 1: int} value and the offset after it */
    public static function decode(string $data, int $offset = 0, int $depth = 0): array
    {
        if ($depth > 16) {
            throw new \InvalidArgumentException('CBOR nested too deeply');
        }
        $len = strlen($data);
        if ($offset >= $len) {
            throw new \InvalidArgumentException('CBOR data too short');
        }
        $b = ord($data[$offset++]);
        $major = $b >> 5;
        $info = $b & 0x1f;
        if ($info < 24) {
            $arg = $info;
        } elseif ($info <= 27) {
            $n = 1 << ($info - 24);
            if ($offset + $n > $len) {
                throw new \InvalidArgumentException('CBOR data too short');
            }
            $arg = 0;
            for ($i = 0; $i < $n; $i++) {
                $arg = ($arg << 8) | ord($data[$offset + $i]);
            }
            if ($arg < 0) {
                throw new \InvalidArgumentException('CBOR integer too large');
            }
            $offset += $n;
        } else {
            throw new \InvalidArgumentException('CBOR indefinite lengths are not supported');
        }
        switch ($major) {
            case 0:
                return [$arg, $offset];
            case 1:
                return [-1 - $arg, $offset];
            case 2:
            case 3:
                if ($offset + $arg > $len) {
                    throw new \InvalidArgumentException('CBOR data too short');
                }
                return [substr($data, $offset, $arg), $offset + $arg];
            case 4:
                $out = [];
                for ($i = 0; $i < $arg; $i++) {
                    [$out[], $offset] = self::decode($data, $offset, $depth + 1);
                }
                return [$out, $offset];
            case 5:
                $out = [];
                for ($i = 0; $i < $arg; $i++) {
                    [$k, $offset] = self::decode($data, $offset, $depth + 1);
                    [$v, $offset] = self::decode($data, $offset, $depth + 1);
                    if (!is_int($k) && !is_string($k)) {
                        throw new \InvalidArgumentException('CBOR map key type not supported');
                    }
                    $out[$k] = $v;
                }
                return [$out, $offset];
            case 7:
                return [match ($info) { 20 => false, 21 => true, 22 => null, default => throw new \InvalidArgumentException('CBOR simple value not supported') }, $offset];
        }
        throw new \InvalidArgumentException('CBOR tags are not supported');
    }
}
