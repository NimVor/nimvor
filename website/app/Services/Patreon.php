<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/**
 * The active patrons of the campaign, read from Patreon's API (v2) with the creator's access token.
 * Only www.patreon.com is contacted. Only what the sponsors page shows is read: the name, the public
 * profile link and the monthly pledge. No email address, no payment data and no private fields are asked for.
 */
final class Patreon
{
    private const API = 'https://www.patreon.com/api/oauth2/v2/';

    /** @param array{access_token: string, campaign_id: string} $cfg */
    public function __construct(private readonly Http $http, private readonly array $cfg)
    {
    }

    public function hasToken(): bool
    {
        return $this->cfg['access_token'] !== '';
    }

    /**
     * Active patrons with a pledge above zero.
     *
     * @return list<array{id: string, name: string, url: string, monthly_usd: int}>
     * @throws PatreonException
     */
    public function patrons(): array
    {
        if (!$this->hasToken()) {
            throw new PatreonException('A Patreon access token is needed to read patrons (set PATREON_ACCESS_TOKEN).');
        }
        $campaign = $this->campaignId();
        $out = [];
        $cursor = '';
        for ($page = 0; $page < 30; $page++) {
            $url = self::API . 'campaigns/' . rawurlencode($campaign) . '/members?include=user'
                . '&fields%5Bmember%5D=full_name,patron_status,currently_entitled_amount_cents'
                . '&fields%5Buser%5D=full_name,url&page%5Bsize%5D=100' . ($cursor !== '' ? '&page%5Bcursor%5D=' . rawurlencode($cursor) : '');
            $data = $this->json($url);
            $users = [];
            foreach ((array) ($data['included'] ?? []) as $inc) {
                if (is_array($inc) && ($inc['type'] ?? '') === 'user' && is_string($inc['id'] ?? null)) {
                    $users[$inc['id']] = (array) ($inc['attributes'] ?? []);
                }
            }
            foreach ((array) ($data['data'] ?? []) as $m) {
                if (!is_array($m) || !is_string($m['id'] ?? null)) {
                    continue;
                }
                $a = (array) ($m['attributes'] ?? []);
                $cents = (int) ($a['currently_entitled_amount_cents'] ?? 0);
                if (($a['patron_status'] ?? '') !== 'active_patron' || $cents <= 0) {
                    continue;
                }
                $userId = (string) ($m['relationships']['user']['data']['id'] ?? '');
                $user = $users[$userId] ?? [];
                $name = trim((string) ($user['full_name'] ?? $a['full_name'] ?? ''));
                $link = (string) ($user['url'] ?? '');
                $out[] = [
                    'id' => (string) $m['id'],
                    'name' => mb_substr($name !== '' ? $name : 'A patron', 0, 120),
                    // Only Patreon's own profile pages are linked.
                    'url' => preg_match('#^https://www\.patreon\.com/[A-Za-z0-9_./?=&%-]{1,200}$#', $link) === 1 ? $link : '',
                    'monthly_usd' => intdiv($cents, 100),
                ];
            }
            $next = $data['meta']['pagination']['cursors']['next'] ?? null;
            if (!is_string($next) || $next === '') {
                break;
            }
            $cursor = $next;
        }
        return $out;
    }

    /** @throws PatreonException */
    private function campaignId(): string
    {
        if ($this->cfg['campaign_id'] !== '') {
            if (preg_match('/^[0-9]{1,20}$/', $this->cfg['campaign_id']) !== 1) {
                throw new PatreonException('PATREON_CAMPAIGN_ID must be the campaign\'s number.');
            }
            return $this->cfg['campaign_id'];
        }
        $data = $this->json(self::API . 'campaigns');
        $id = $data['data'][0]['id'] ?? null;
        if (!is_string($id) || preg_match('/^[0-9]{1,20}$/', $id) !== 1) {
            throw new PatreonException('Patreon returned no campaign for this token.');
        }
        return $id;
    }

    /** @return array<string|int, mixed> */
    private function json(string $url): array
    {
        if ((string) parse_url($url, PHP_URL_HOST) !== 'www.patreon.com') {
            throw new PatreonException('Refusing to contact ' . (string) parse_url($url, PHP_URL_HOST) . '.');
        }
        try {
            $r = $this->http->request('GET', $url, ['Accept' => 'application/json', 'Authorization' => 'Bearer ' . $this->cfg['access_token']], null, 20);
        } catch (\RuntimeException $e) {
            throw new PatreonException('Patreon could not be reached: ' . $e->getMessage(), 0, $e);
        }
        if ($r['status'] !== 200) {
            throw new PatreonException(match (true) {
                $r['status'] === 401 => 'Patreon did not accept the access token.',
                $r['status'] === 403 => 'The Patreon token may not read members (it needs the campaigns and campaigns.members scopes).',
                $r['status'] === 429 => 'Patreon is limiting requests right now; try again later.',
                default => 'Patreon answered HTTP ' . $r['status'] . '.',
            });
        }
        $data = json_decode($r['body'], true);
        if (!is_array($data)) {
            throw new PatreonException('Patreon answered with something unexpected.');
        }
        return $data;
    }
}
