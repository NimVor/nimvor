<?php

declare(strict_types=1);

namespace NimVor\Security;

use InvalidArgumentException;

/**
 * Minimal QR code encoder (ISO/IEC 18004): byte mode, error correction
 * level M, versions 1–15 (up to 412 bytes). Enough for otpauth:// URIs, so
 * two-factor setup needs no third-party library or external service.
 */
final class QrCode
{
    /** version => [ec codewords per block, [[blocks, data codewords], ...]] (level M) */
    private const BLOCKS = [
        1 => [10, [[1, 16]]], 2 => [16, [[1, 28]]], 3 => [26, [[1, 44]]], 4 => [18, [[2, 32]]],
        5 => [24, [[2, 43]]], 6 => [16, [[4, 27]]], 7 => [18, [[4, 31]]], 8 => [22, [[2, 38], [2, 39]]],
        9 => [22, [[3, 36], [2, 37]]], 10 => [26, [[4, 43], [1, 44]]], 11 => [30, [[1, 50], [4, 51]]],
        12 => [22, [[6, 36], [2, 37]]], 13 => [22, [[8, 37], [1, 38]]], 14 => [24, [[4, 40], [5, 41]]],
        15 => [24, [[5, 41], [5, 42]]],
    ];

    private const ALIGN = [
        1 => [], 2 => [6, 18], 3 => [6, 22], 4 => [6, 26], 5 => [6, 30], 6 => [6, 34], 7 => [6, 22, 38],
        8 => [6, 24, 42], 9 => [6, 26, 46], 10 => [6, 28, 50], 11 => [6, 30, 54], 12 => [6, 32, 58],
        13 => [6, 34, 62], 14 => [6, 26, 46, 66], 15 => [6, 26, 48, 70],
    ];

    /** @var list<list<bool>> [row][col] */
    private array $modules = [];
    /** @var list<list<bool>> */
    private array $function = [];
    private int $size;

    /** @var list<int> */
    private static array $exp = [];
    /** @var list<int> */
    private static array $log = [];

    private function __construct(public readonly int $version)
    {
        $this->size = $version * 4 + 17;
        $row = array_fill(0, $this->size, false);
        $this->modules = array_fill(0, $this->size, $row);
        $this->function = array_fill(0, $this->size, $row);
    }

    /**
     * Encode $data. $mask forces a mask pattern (0–7); by default the one
     * with the lowest penalty is chosen.
     */
    public static function encode(string $data, ?int $mask = null): self
    {
        $len = strlen($data);
        foreach (self::BLOCKS as $v => [$ec, $groups]) {
            $capacity = 0;
            foreach ($groups as [$n, $d]) {
                $capacity += $n * $d;
            }
            $header = 4 + ($v < 10 ? 8 : 16);
            if ($header + 8 * $len <= $capacity * 8) {
                $qr = new self($v);
                $qr->drawFunctionPatterns();
                $qr->drawCodewords($qr->addEcc(self::dataCodewords($data, $v, $capacity)));
                $qr->applyBestMask($mask);
                return $qr;
            }
        }
        throw new InvalidArgumentException('Data too long for a QR code.');
    }

    public function size(): int
    {
        return $this->size;
    }

    public function dark(int $row, int $col): bool
    {
        return $this->modules[$row][$col];
    }

    /** SVG with a 4-module quiet zone; dark modules drawn as one path. */
    public function svg(int $scale = 4, string $label = 'QR code'): string
    {
        $q = 4;
        $n = $this->size + 2 * $q;
        $d = '';
        for ($y = 0; $y < $this->size; $y++) {
            for ($x = 0; $x < $this->size; $x++) {
                if ($this->modules[$y][$x]) {
                    $d .= 'M' . ($x + $q) . ' ' . ($y + $q) . 'h1v1h-1z';
                }
            }
        }
        $px = $n * $scale;
        return '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ' . $n . ' ' . $n . '" width="' . $px . '" height="' . $px
            . '" shape-rendering="crispEdges" role="img" aria-label="' . htmlspecialchars($label, ENT_QUOTES) . '">'
            . '<rect width="' . $n . '" height="' . $n . '" fill="#fff"/><path fill="#000" d="' . $d . '"/></svg>';
    }

    // --- data -------------------------------------------------------------

    /** @return list<int> */
    private static function dataCodewords(string $data, int $version, int $capacity): array
    {
        $bits = '0100' . str_pad(decbin(strlen($data)), $version < 10 ? 8 : 16, '0', STR_PAD_LEFT);
        foreach (str_split($data) as $c) {
            $bits .= str_pad(decbin(ord($c)), 8, '0', STR_PAD_LEFT);
        }
        $bits .= str_repeat('0', min(4, $capacity * 8 - strlen($bits)));
        $bits .= str_repeat('0', (8 - strlen($bits) % 8) % 8);
        $out = array_map('bindec', str_split($bits, 8));
        for ($pad = 0xEC; count($out) < $capacity; $pad ^= 0xEC ^ 0x11) {
            $out[] = $pad;
        }
        return array_map('intval', $out);
    }

    /**
     * Split into blocks, add Reed–Solomon codewords and interleave.
     *
     * @param list<int> $data
     * @return list<int>
     */
    private function addEcc(array $data): array
    {
        [$ecLen, $groups] = self::BLOCKS[$this->version];
        $gen = self::generator($ecLen);
        $blocks = $ecc = [];
        $pos = 0;
        foreach ($groups as [$n, $d]) {
            for ($i = 0; $i < $n; $i++) {
                $block = array_slice($data, $pos, $d);
                $pos += $d;
                $blocks[] = $block;
                $ecc[] = self::remainder($block, $gen);
            }
        }
        $out = [];
        $max = max(array_map('count', $blocks));
        for ($i = 0; $i < $max; $i++) {
            foreach ($blocks as $b) {
                if ($i < count($b)) {
                    $out[] = $b[$i];
                }
            }
        }
        for ($i = 0; $i < $ecLen; $i++) {
            foreach ($ecc as $e) {
                $out[] = $e[$i];
            }
        }
        return $out;
    }

    private static function gf(): void
    {
        if (self::$exp !== []) {
            return;
        }
        $x = 1;
        for ($i = 0; $i < 255; $i++) {
            self::$exp[$i] = $x;
            self::$log[$x] = $i;
            $x <<= 1;
            if ($x & 0x100) {
                $x ^= 0x11D;
            }
        }
    }

    private static function mul(int $a, int $b): int
    {
        return ($a === 0 || $b === 0) ? 0 : self::$exp[(self::$log[$a] + self::$log[$b]) % 255];
    }

    /** @return list<int> coefficients, highest degree first, leading 1 omitted */
    private static function generator(int $degree): array
    {
        self::gf();
        $g = [1];
        for ($i = 0; $i < $degree; $i++) {
            $next = array_fill(0, count($g) + 1, 0);
            foreach ($g as $j => $c) {
                $next[$j] ^= $c;
                $next[$j + 1] ^= self::mul($c, self::$exp[$i]);
            }
            $g = $next;
        }
        return array_slice($g, 1);
    }

    /**
     * @param list<int> $data
     * @param list<int> $gen
     * @return list<int>
     */
    private static function remainder(array $data, array $gen): array
    {
        $rem = array_fill(0, count($gen), 0);
        foreach ($data as $b) {
            $factor = $b ^ array_shift($rem);
            $rem[] = 0;
            foreach ($gen as $i => $g) {
                $rem[$i] ^= self::mul($g, $factor);
            }
        }
        return $rem;
    }

    // --- layout -----------------------------------------------------------

    private function set(int $x, int $y, bool $dark, bool $function = true): void
    {
        $this->modules[$y][$x] = $dark;
        if ($function) {
            $this->function[$y][$x] = true;
        }
    }

    private function drawFunctionPatterns(): void
    {
        for ($i = 0; $i < $this->size; $i++) {
            $this->set(6, $i, $i % 2 === 0);
            $this->set($i, 6, $i % 2 === 0);
        }
        foreach ([[3, 3], [$this->size - 4, 3], [3, $this->size - 4]] as [$cx, $cy]) {
            for ($dy = -4; $dy <= 4; $dy++) {
                for ($dx = -4; $dx <= 4; $dx++) {
                    $x = $cx + $dx;
                    $y = $cy + $dy;
                    if ($x >= 0 && $x < $this->size && $y >= 0 && $y < $this->size) {
                        $dist = max(abs($dx), abs($dy));
                        $this->set($x, $y, $dist !== 2 && $dist !== 4);
                    }
                }
            }
        }
        $pos = self::ALIGN[$this->version];
        $last = count($pos) - 1;
        foreach ($pos as $i => $cx) {
            foreach ($pos as $j => $cy) {
                if (($i === 0 && $j === 0) || ($i === 0 && $j === $last) || ($i === $last && $j === 0)) {
                    continue; // overlaps a finder pattern
                }
                for ($dy = -2; $dy <= 2; $dy++) {
                    for ($dx = -2; $dx <= 2; $dx++) {
                        $this->set($cx + $dx, $cy + $dy, max(abs($dx), abs($dy)) !== 1);
                    }
                }
            }
        }
        $this->drawFormatBits(0); // reserve; redrawn with the chosen mask
        if ($this->version >= 7) {
            $rem = $this->version;
            for ($i = 0; $i < 12; $i++) {
                $rem = ($rem << 1) ^ (($rem >> 11) * 0x1F25);
            }
            $bits = $this->version << 12 | $rem;
            for ($i = 0; $i < 18; $i++) {
                $dark = (($bits >> $i) & 1) === 1;
                $a = $this->size - 11 + $i % 3;
                $b = intdiv($i, 3);
                $this->set($a, $b, $dark);
                $this->set($b, $a, $dark);
            }
        }
    }

    private function drawFormatBits(int $mask): void
    {
        $data = 0 << 3 | $mask; // level M = 00
        $rem = $data;
        for ($i = 0; $i < 10; $i++) {
            $rem = ($rem << 1) ^ (($rem >> 9) * 0x537);
        }
        $bits = ($data << 10 | $rem) ^ 0x5412;
        $bit = static fn (int $i): bool => (($bits >> $i) & 1) === 1;
        for ($i = 0; $i <= 5; $i++) {
            $this->set(8, $i, $bit($i));
        }
        $this->set(8, 7, $bit(6));
        $this->set(8, 8, $bit(7));
        $this->set(7, 8, $bit(8));
        for ($i = 9; $i < 15; $i++) {
            $this->set(14 - $i, 8, $bit($i));
        }
        for ($i = 0; $i < 8; $i++) {
            $this->set($this->size - 1 - $i, 8, $bit($i));
        }
        for ($i = 8; $i < 15; $i++) {
            $this->set(8, $this->size - 15 + $i, $bit($i));
        }
        $this->set(8, $this->size - 8, true);
    }

    /** @param list<int> $data */
    private function drawCodewords(array $data): void
    {
        $i = 0;
        $total = count($data) * 8;
        for ($right = $this->size - 1; $right >= 1; $right -= 2) {
            if ($right === 6) {
                $right = 5;
            }
            for ($vert = 0; $vert < $this->size; $vert++) {
                for ($j = 0; $j < 2; $j++) {
                    $x = $right - $j;
                    $up = (($right + 1) & 2) === 0;
                    $y = $up ? $this->size - 1 - $vert : $vert;
                    if (!$this->function[$y][$x] && $i < $total) {
                        $this->modules[$y][$x] = (($data[$i >> 3] >> (7 - ($i & 7))) & 1) === 1;
                        $i++;
                    }
                }
            }
        }
    }

    private static function masked(int $mask, int $x, int $y): bool
    {
        return match ($mask) {
            0 => ($x + $y) % 2 === 0,
            1 => $y % 2 === 0,
            2 => $x % 3 === 0,
            3 => ($x + $y) % 3 === 0,
            4 => (intdiv($x, 3) + intdiv($y, 2)) % 2 === 0,
            5 => ($x * $y) % 2 + ($x * $y) % 3 === 0,
            6 => (($x * $y) % 2 + ($x * $y) % 3) % 2 === 0,
            default => (($x + $y) % 2 + ($x * $y) % 3) % 2 === 0,
        };
    }

    private function applyMask(int $mask): void
    {
        for ($y = 0; $y < $this->size; $y++) {
            for ($x = 0; $x < $this->size; $x++) {
                if (!$this->function[$y][$x] && self::masked($mask, $x, $y)) {
                    $this->modules[$y][$x] = !$this->modules[$y][$x];
                }
            }
        }
    }

    private function applyBestMask(?int $forced): void
    {
        $best = $forced;
        if ($best === null) {
            $min = PHP_INT_MAX;
            for ($m = 0; $m < 8; $m++) {
                $this->applyMask($m);
                $this->drawFormatBits($m);
                $p = $this->penalty();
                if ($p < $min) {
                    $min = $p;
                    $best = $m;
                }
                $this->applyMask($m); // undo (XOR)
            }
        }
        $this->applyMask((int) $best);
        $this->drawFormatBits((int) $best);
    }

    private function penalty(): int
    {
        $n = $this->size;
        $p = 0;
        $dark = 0;
        $lines = [];
        for ($i = 0; $i < $n; $i++) {
            $row = $col = '';
            for ($j = 0; $j < $n; $j++) {
                $row .= $this->modules[$i][$j] ? '1' : '0';
                $col .= $this->modules[$j][$i] ? '1' : '0';
            }
            $lines[] = $row;
            $lines[] = $col;
            $dark += substr_count($row, '1');
        }
        foreach ($lines as $line) {
            preg_match_all('/0{5,}|1{5,}/', $line, $m);
            foreach ($m[0] as $run) {
                $p += 3 + strlen($run) - 5;
            }
            $p += 40 * (preg_match_all('/(?=10111010000|00001011101)/', $line));
        }
        for ($y = 0; $y < $n - 1; $y++) {
            for ($x = 0; $x < $n - 1; $x++) {
                $c = $this->modules[$y][$x];
                if ($c === $this->modules[$y][$x + 1] && $c === $this->modules[$y + 1][$x] && $c === $this->modules[$y + 1][$x + 1]) {
                    $p += 3;
                }
            }
        }
        $p += 10 * intdiv(abs(intdiv($dark * 100, $n * $n) - 50), 5);
        return $p;
    }
}
