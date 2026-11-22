<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Services\Patreon;
use NimVorSite\Services\PatreonException;
use NimVorSite\Services\SponsorSync;
use NimVorSite\Tests\Support\SiteTestCase;

/** Patreon patrons, sponsors added by hand, and which sources the public page shows. */
final class PatreonSponsorsTest extends SiteTestCase
{
    private const API = 'https://www.patreon.com/api/oauth2/v2/';

    private function members(array $rows, ?string $next = null): array
    {
        $data = [];
        $included = [];
        foreach ($rows as $i => [$id, $name, $status, $cents, $url]) {
            $data[] = ['id' => $id, 'type' => 'member', 'attributes' => ['full_name' => $name, 'patron_status' => $status, 'currently_entitled_amount_cents' => $cents], 'relationships' => ['user' => ['data' => ['id' => 'u' . $id, 'type' => 'user']]]];
            $included[] = ['id' => 'u' . $id, 'type' => 'user', 'attributes' => ['full_name' => $name, 'url' => $url]];
        }
        return ['data' => $data, 'included' => $included, 'meta' => ['pagination' => ['cursors' => ['next' => $next]]]];
    }

    private function patreon(string $token = 'pat_test', string $campaign = ''): Patreon
    {
        return new Patreon($this->http, ['access_token' => $token, 'campaign_id' => $campaign]);
    }

    private function service(?Patreon $p = null): SponsorSync
    {
        $gh = new \NimVorSite\Services\GitHub($this->http, ['repo' => 'NimVor/nimvor', 'branch' => 'main', 'token' => '']);
        return new SponsorSync($gh, $this->app->sponsors, $this->app->cache, 'NimVor', $p ?? $this->patreon());
    }

    private function connect(array $members): void
    {
        $this->http->on(self::API . 'campaigns?', 200, []);
        $this->http->on(self::API . 'campaigns', 200, ['data' => [['id' => '777', 'type' => 'campaign']]]);
        $this->http->on(self::API . 'campaigns/777/members', 200, $members);
    }

    public function testOnlyActivePatronsWithAPledgeAreCopiedAndNoEmailIsAskedFor(): void
    {
        $this->connect($this->members([
            ['1', 'Ada Lovelace', 'active_patron', 10000, 'https://www.patreon.com/ada'],
            ['2', 'Bob', 'active_patron', 500, 'https://www.patreon.com/bob'],
            ['3', 'Gone', 'former_patron', 2500, 'https://www.patreon.com/gone'],
            ['4', 'Free Follower', 'active_patron', 0, 'https://www.patreon.com/free'],
            ['5', 'Phisher', 'active_patron', 3000, 'https://evil.example/login'],
        ]));
        $r = $this->service()->syncPatreon();
        self::assertSame(3, $r['total']);
        self::assertSame('gold', $this->row("SELECT tier FROM sponsors WHERE patreon_id = '1'")['tier']);
        self::assertSame('supporter', $this->row("SELECT tier FROM sponsors WHERE patreon_id = '2'")['tier']);
        self::assertSame('silver', $this->row("SELECT tier FROM sponsors WHERE patreon_id = '5'")['tier']);
        self::assertSame('', $this->row("SELECT url FROM sponsors WHERE patreon_id = '5'")['url'], 'only Patreon profile links are kept');
        self::assertNull($this->row("SELECT id FROM sponsors WHERE patreon_id IN ('3', '4')"));
        $members = array_values(array_filter($this->http->calls, static fn (array $c): bool => str_contains($c['url'], '/members')))[0];
        self::assertStringNotContainsString('email', $members['url'], 'no email address is requested');
        self::assertSame('Bearer pat_test', $members['headers']['Authorization']);
        $page = $this->get('/resources/sponsors')->body;
        self::assertStringContainsString('Ada Lovelace', $page);
        self::assertStringContainsString('Patreon', $page);
        self::assertStringNotContainsString('Gone', $page);
    }

    public function testPagesAreFollowedAndALeavingPatronIsRemoved(): void
    {
        $this->http->on(self::API . 'campaigns/9/members', 200, $this->members([['1', 'Ada', 'active_patron', 2500, 'https://www.patreon.com/ada']], 'CURSOR1'));
        $this->http->on(self::API . 'campaigns/9/members?include=user&fields%5Bmember%5D=full_name,patron_status,currently_entitled_amount_cents&fields%5Buser%5D=full_name,url&page%5Bsize%5D=100&page%5Bcursor%5D=CURSOR1', 200, $this->members([['2', 'Bob', 'active_patron', 500, 'https://www.patreon.com/bob']]));
        $svc = $this->service($this->patreon('pat_test', '9'));
        self::assertSame(2, $svc->syncPatreon()['total']);
        $this->http->on(self::API . 'campaigns/9/members', 200, $this->members([['1', 'Ada', 'active_patron', 2500, 'https://www.patreon.com/ada']]));
        $this->http->on(self::API . 'campaigns/9/members?include=user&fields%5Bmember%5D=full_name,patron_status,currently_entitled_amount_cents&fields%5Buser%5D=full_name,url&page%5Bsize%5D=100&page%5Bcursor%5D=CURSOR1', 200, $this->members([]));
        $r = $svc->syncPatreon();
        self::assertSame(1, $r['removed']);
        self::assertSame(1, $this->rows('sponsors'));
    }

    public function testManualSponsorsStayAndRefusalsAreExplained(): void
    {
        $this->app->sponsors->create('Acme Hosting', 'https://acme.example', 'gold');
        $this->http->on(self::API . 'campaigns', 401, '{}');
        try {
            $this->service()->syncPatreon();
            self::fail('expected a failure');
        } catch (PatreonException $e) {
            self::assertStringContainsString('did not accept the access token', $e->getMessage());
        }
        self::assertSame(1, $this->rows('sponsors'));
        $this->http->on(self::API . 'campaigns', 403, '{}');
        try {
            $this->service()->syncPatreon();
            self::fail('expected a failure');
        } catch (PatreonException $e) {
            self::assertStringContainsString('campaigns.members', $e->getMessage());
        }
        $this->expectException(PatreonException::class);
        $this->service($this->patreon(''))->syncPatreon();
    }

    public function testEachSourceCanBeSwitchedOffOnThePublicPage(): void
    {
        $this->app->sponsors->create('Acme Hosting', 'https://acme.example', 'gold');
        $this->app->sponsors->upsertGithub('ghuser', 'Git Hubber', 'https://github.com/ghuser', 'silver', 25);
        $this->app->sponsors->upsertPatreon('11', 'Pat Ron', 'https://www.patreon.com/pat', 'silver', 30);
        $this->loginAs('admin', 'boss@example.test');
        $show = static fn (array $on): array => ['patreon_url' => 'https://www.patreon.com/nimvor'] + array_combine(array_map(static fn (string $s): string => 'show_' . $s, $on), array_fill(0, count($on), '1'));
        $page = fn (): string => $this->get('/resources/sponsors')->body;

        $res = $this->post('/admin/sponsors/display', $show(['github', 'patreon', 'manual']));
        self::assertSame('/admin/sponsors', $res->headers['Location']);
        $b = $page();
        foreach (['Acme Hosting', 'Git Hubber', 'Pat Ron', 'Support on Patreon', 'Sponsor on GitHub'] as $t) {
            self::assertStringContainsString($t, $b, $t);
        }
        $this->post('/admin/sponsors/display', $show(['github', 'manual']));
        $b = $page();
        self::assertStringNotContainsString('Pat Ron', $b, 'Patreon is switched off');
        self::assertStringNotContainsString('Support on Patreon', $b);
        self::assertStringContainsString('Git Hubber', $b);
        $this->post('/admin/sponsors/display', $show(['patreon']));
        $b = $page();
        self::assertStringContainsString('Pat Ron', $b);
        self::assertStringNotContainsString('Git Hubber', $b);
        self::assertStringNotContainsString('Acme Hosting', $b);
        self::assertStringNotContainsString('Sponsor on GitHub', $b);
        $this->post('/admin/sponsors/display', ['patreon_url' => '']);
        self::assertStringContainsString('no sponsors yet', $page());
    }

    public function testThePatreonAddressIsValidated(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        foreach (['http://www.patreon.com/x', 'https://evil.example/patreon.com/x', 'javascript:alert(1)'] as $bad) {
            self::assertSame(422, $this->post('/admin/sponsors/display', ['patreon_url' => $bad, 'show_patreon' => '1'])->status, $bad);
        }
        self::assertSame(303, $this->post('/admin/sponsors/display', ['patreon_url' => 'https://www.patreon.com/nimvor', 'show_patreon' => '1'])->status);
    }

    public function testManualSponsorsCanBeEditedAndHiddenAndSyncedOnesOnlyHidden(): void
    {
        $this->loginAs('admin', 'boss@example.test');
        $this->post('/admin/sponsors', ['name' => 'Acme Hosting', 'url' => 'https://acme.test/', 'tier' => 'gold']);
        $id = (int) $this->row("SELECT id FROM sponsors WHERE source = 'manual'")['id'];
        self::assertSame(303, $this->post("/admin/sponsors/{$id}/update", ['name' => 'Acme Cloud', 'url' => 'https://acme.test/cloud', 'tier' => 'silver', 'visible' => '1'])->status);
        self::assertSame(['Acme Cloud', 'silver'], array_values(array_intersect_key((array) $this->row('SELECT name, tier FROM sponsors WHERE id = ?', [$id]), ['name' => 1, 'tier' => 1])));
        self::assertSame(422, $this->post("/admin/sponsors/{$id}/update", ['name' => 'X', 'url' => 'http://insecure.test', 'tier' => 'silver'])->status);
        self::assertStringContainsString('Acme Cloud', $this->get('/resources/sponsors')->body);
        // Saving without the "shown" box hides it.
        $this->post("/admin/sponsors/{$id}/update", ['name' => 'Acme Cloud', 'url' => 'https://acme.test/cloud', 'tier' => 'silver']);
        self::assertStringNotContainsString('Acme Cloud', $this->get('/resources/sponsors')->body);
        $this->post("/admin/sponsors/{$id}/visibility", ['visible' => '1']);
        self::assertStringContainsString('Acme Cloud', $this->get('/resources/sponsors')->body);

        // A synced sponsor cannot be edited, can be hidden, and stays hidden through the next sync.
        $this->app->sponsors->upsertPatreon('5', 'Pat', 'https://www.patreon.com/pat', 'silver', 30);
        $pid = (int) $this->row("SELECT id FROM sponsors WHERE patreon_id = '5'")['id'];
        $this->post("/admin/sponsors/{$pid}/update", ['name' => 'Renamed', 'url' => 'https://acme.test/', 'tier' => 'gold', 'visible' => '1']);
        self::assertSame('Pat', $this->row('SELECT name FROM sponsors WHERE id = ?', [$pid])['name']);
        $this->post("/admin/sponsors/{$pid}/visibility", ['visible' => '0']);
        $this->app->sponsors->upsertPatreon('5', 'Pat R.', 'https://www.patreon.com/pat', 'silver', 30);
        self::assertSame(0, (int) $this->row('SELECT visible FROM sponsors WHERE id = ?', [$pid])['visible'], 'hidden stays hidden');
        self::assertStringNotContainsString('Pat R.', $this->get('/resources/sponsors')->body);
    }

    public function testTheAdminPageExplainsHowToConnectPatreonAndOnlyAdminsChangeThings(): void
    {
        foreach (['/admin/sponsors/sync-patreon', '/admin/sponsors/display', '/admin/sponsors/1/update', '/admin/sponsors/1/visibility'] as $path) {
            self::assertSame(303, $this->post($path)->status, $path . ' needs a login');
        }
        $this->loginAs('admin', 'boss@example.test');
        $page = $this->get('/admin/sponsors')->body;
        self::assertStringContainsString('PATREON_ACCESS_TOKEN', $page);
        self::assertStringNotContainsString('Sync from Patreon now', $page);
        $this->post('/admin/sponsors/sync-patreon');
        self::assertStringContainsString('Patreon access token is needed', $this->get('/admin/sponsors')->body);
    }
}
