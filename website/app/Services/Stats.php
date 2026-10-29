<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Cache\Cache;
use NimVorSite\Repositories\Installs;

/** The public usage numbers, cached for ten minutes. */
final class Stats
{
    public function __construct(private readonly Installs $installs, private readonly Cache $cache)
    {
    }

    /** @return array{totals: array{total: int, active_30: int, new_30: int, latest: ?string}, versions: array<string, int>, systems: array<string, int>, arch: array<string, int>, months: array<string, int>} */
    public function public(): array
    {
        $c = $this->cache->get('stats:public');
        if (is_array($c)) {
            return $c;
        }
        $s = [
            'totals' => $this->installs->totals(),
            'versions' => $this->installs->breakdown('version'),
            'systems' => $this->installs->breakdown('os'),
            'arch' => $this->installs->breakdown('arch'),
            'months' => $this->installs->perMonth(12),
        ];
        $this->cache->set('stats:public', $s, 600);
        return $s;
    }

    /** A count worth showing on the home page, or null while it would only say "0". */
    public function headline(): ?int
    {
        $t = $this->public()['totals']['total'];
        return $t > 0 ? $t : null;
    }
}
