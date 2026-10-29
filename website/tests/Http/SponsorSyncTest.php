<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Services\GitHub;
use NimVorSite\Services\GitHubException;
use NimVorSite\Services\SponsorSync;
use NimVorSite\Tests\Support\SiteTestCase;

final class SponsorSyncTest extends SiteTestCase
{
    private function sponsorsFor(string $kind, array $nodes): array
    {
        return ['data' => [$kind => ['sponsorshipsAsMaintainer' => ['nodes' => $nodes]]]];
    }

    private function node(string $login, ?string $name, int $usd, string $privacy = 'PUBLIC', bool $oneTime = false): array
    {
        return ['privacyLevel' => $privacy, 'isOneTimePayment' => $oneTime, 'tier' => ['monthlyPriceInDollars' => $usd], 'sponsorEntity' => ['__typename' => 'User', 'login' => $login, 'name' => $name, 'url' => 'https://github.com/' . $login]];
    }

    private function service(): SponsorSync
    {
        $gh = new GitHub($this->http, ['repo' => 'NimVor/nimvor', 'branch' => 'main', 'token' => 'ghp_test']);
        return new SponsorSync($gh, $this->app->sponsors, $this->app->cache, 'NimVor');
    }

    public function testTiersFollowTheMonthlyAmount(): void
    {
        self::assertSame('supporter', SponsorSync::tier(5));
        self::assertSame('silver', SponsorSync::tier(25));
        self::assertSame('gold', SponsorSync::tier(100));
        self::assertSame('supporter', SponsorSync::tier(0));
    }

    public function testPublicGitHubSponsorsAreCopiedAndManualOnesStay(): void
    {
        $this->app->sponsors->create('Acme Hosting', 'https://acme.example', 'gold');
        $this->http->on('https://api.github.com/graphql', 200, $this->sponsorsFor('organization', [
            $this->node('ada', 'Ada Lovelace', 100),
            $this->node('bob', null, 5),
            $this->node('secret', 'Hidden Person', 50, 'PRIVATE'),
        ]));
        $r = $this->service()->sync();
        self::assertSame(2, $r['total']);
        $page = $this->get('/resources/sponsors')->body;
        self::assertStringContainsString('Ada Lovelace', $page);
        self::assertStringContainsString('bob', $page, 'a sponsor without a name is shown by login');
        self::assertStringContainsString('Acme Hosting', $page);
        self::assertStringNotContainsString('Hidden Person', $page, 'private sponsorships stay private');
        self::assertStringContainsString('GitHub Sponsors', $page);
        self::assertSame('gold', $this->row("SELECT tier FROM sponsors WHERE github_login = 'ada'")['tier']);

        $call = end($this->http->calls);
        self::assertSame('Bearer ghp_test', $call['headers']['Authorization']);

        // Bob stops sponsoring, ada upgrades nothing: bob goes, Acme (manual) stays.
        $this->http->on('https://api.github.com/graphql', 200, $this->sponsorsFor('organization', [$this->node('ada', 'Ada Lovelace', 100)]));
        $r = $this->service()->sync();
        self::assertSame(1, $r['removed']);
        self::assertSame(2, $this->rows('sponsors'));
        self::assertNotNull($this->row("SELECT id FROM sponsors WHERE name = 'Acme Hosting' AND source = 'manual'"));
    }

    public function testAUserAccountIsTriedWhenTheNameIsNotAnOrganisation(): void
    {
        $this->http->on('https://api.github.com/graphql', 200, ['data' => ['organization' => null], 'errors' => [['type' => 'NOT_FOUND']]]);
        try {
            $this->service()->sync();
            self::fail('expected a failure');
        } catch (GitHubException $e) {
            self::assertStringContainsString('did not return sponsors', $e->getMessage());
        }
        self::assertSame(2, $this->http->asked('graphql'), 'organisation, then user');
    }

    public function testNoTokenNoSync(): void
    {
        $gh = new GitHub($this->http, ['repo' => 'NimVor/nimvor', 'branch' => 'main', 'token' => '']);
        $this->expectException(GitHubException::class);
        (new SponsorSync($gh, $this->app->sponsors, $this->app->cache, 'NimVor'))->sync();
    }

    public function testGitHubRefusalsAreExplained(): void
    {
        $this->http->on('https://api.github.com/graphql', 401, '{}');
        try {
            $this->service()->sync();
            self::fail('expected a failure');
        } catch (GitHubException $e) {
            self::assertStringContainsString('did not accept the token', $e->getMessage());
        }
    }

    public function testTheAdminPageExplainsHowToConnectAndOnlyAdminsSync(): void
    {
        self::assertSame(303, $this->post('/admin/sponsors/sync')->status);
        $this->loginAs('admin', 'a@example.test');
        $page = $this->get('/admin/sponsors')->body;
        self::assertStringContainsString('GITHUB_TOKEN', $page);
        self::assertStringNotContainsString('Sync from GitHub now', $page);
        $res = $this->post('/admin/sponsors/sync');
        self::assertSame('/admin/sponsors', $res->headers['Location']);
        self::assertStringContainsString('token is needed', $this->get('/admin/sponsors')->body);
    }

    public function testASponsorWithAGitHubSourceCannotBeRemovedByHand(): void
    {
        $this->app->sponsors->upsertGithub('ada', 'Ada', 'https://github.com/ada', 'gold', 100);
        $this->loginAs('admin', 'a@example.test');
        $page = $this->get('/admin/sponsors')->body;
        self::assertStringContainsString('synced', $page);
        self::assertStringNotContainsString('Remove this sponsor?', $page);
    }
}
