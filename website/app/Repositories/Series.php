<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

final class Series
{
    /**
     * @param array<string, mixed> $byDay Y-m-d => count
     * @return array<string, int> the last $days days, oldest first, missing days as 0
     */
    public static function fill(array $byDay, int $days): array
    {
        $out = [];
        for ($i = $days - 1; $i >= 0; $i--) {
            $d = gmdate('Y-m-d', time() - $i * 86400);
            $out[$d] = (int) ($byDay[$d] ?? 0);
        }
        return $out;
    }
}
