<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Cache\Cache;
use NimVorSite\Repositories\Sponsors;

/** Copies the public GitHub sponsors and the active Patreon patrons into the sponsors table; sponsors an administrator added by hand are never touched. */
final class SponsorSync
{
    /** Monthly dollars from which a sponsor is silver or gold. */
    public const SILVER = 25;
    public const GOLD = 100;

    public function __construct(private readonly GitHub $github, private readonly Sponsors $sponsors, private readonly Cache $cache, private readonly string $login, private readonly ?Patreon $patreon = null)
    {
    }

    public static function tier(int $monthlyUsd): string
    {
        return $monthlyUsd >= self::GOLD ? 'gold' : ($monthlyUsd >= self::SILVER ? 'silver' : 'supporter');
    }

    /**
     * @return array{added: int, removed: int, total: int}
     * @throws GitHubException
     */
    public function sync(): array
    {
        $found = $this->github->sponsors($this->login);
        $before = count($this->sponsors->githubLogins());
        $logins = [];
        foreach ($found as $s) {
            $logins[] = $s['login'];
            $this->sponsors->upsertGithub($s['login'], $s['name'], $s['url'], self::tier($s['monthly_usd']), $s['monthly_usd']);
        }
        $removed = $this->sponsors->removeGithubExcept($logins);
        $this->cache->delete('sponsors:list');
        $this->cache->delete('report:overview');
        return ['added' => max(0, count($logins) - ($before - $removed)), 'removed' => $removed, 'total' => count($logins)];
    }

    public function patreonConnected(): bool
    {
        return $this->patreon !== null && $this->patreon->hasToken();
    }

    /**
     * @return array{added: int, removed: int, total: int}
     * @throws PatreonException
     */
    public function syncPatreon(): array
    {
        if ($this->patreon === null) {
            throw new PatreonException('Patreon is not set up (set PATREON_ACCESS_TOKEN).');
        }
        $found = $this->patreon->patrons();
        $before = count($this->sponsors->patreonIds());
        $ids = [];
        foreach ($found as $p) {
            $ids[] = $p['id'];
            $this->sponsors->upsertPatreon($p['id'], $p['name'], $p['url'], self::tier($p['monthly_usd']), $p['monthly_usd']);
        }
        $removed = $this->sponsors->removePatreonExcept($ids);
        $this->cache->delete('sponsors:list');
        $this->cache->delete('report:overview');
        return ['added' => max(0, count($ids) - ($before - $removed)), 'removed' => $removed, 'total' => count($ids)];
    }
}
