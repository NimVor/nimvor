<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Tests\Support\HttpTestCase;

/** The Security overview and SSL certificates dashboard widgets. */
final class SecuritySslWidgetsTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('plain', 'customer');
    }

    /** @return array<string, mixed> */

    /** The hover card of a Software tile (/dashboard/popover/KEY): its HTML. */
    private function tile(string $key): string
    {
        $res = $this->request('GET', '/dashboard/popover/' . $key, [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertSame(200, $res->status(), $res->body());
        return (string) ((array) json_decode($res->body(), true))['html'];
    }

    private function widget(string $id, int $expect = 200): array
    {
        $res = $this->request('GET', '/dashboard/widgets/' . $id, [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertSame($expect, $res->status(), $res->body());
        return (array) json_decode($res->body(), true);
    }

    private function security(): void
    {
        $this->agent->succeed('firewall.status', ['installed' => true, 'active' => true]);
        $this->agent->succeed('fail2ban.status', ['installed' => true, 'running' => true, 'jails' => [['name' => 'sshd', 'currently_banned' => 3], ['name' => 'nginx', 'currently_banned' => 2]]]);
        $this->agent->succeed('ssh.logins', ['available' => true, 'today' => [1, 14], 'yesterday' => [0, 0], 'week' => [9, 120], 'last_failure' => ['ip' => '203.0.113.7']]);
        $this->agent->succeed('av.status', ['installed' => true, 'version' => 'ClamAV 1.4/1/2026', 'freshclam' => true]);
        $this->agent->succeed('modsec.status', ['active' => false]);
        $this->agent->succeed('security.audit', [
            ['id' => 'a', 'status' => 'pass', 'severity' => 'high'],
            ['id' => 'b', 'status' => 'fail', 'severity' => 'high'],
            ['id' => 'c', 'status' => 'warn', 'severity' => 'low'],
        ]);
    }

    public function testSecurityOverviewShowsRealReadings(): void
    {
        $this->security();
        $this->login('admin');
        $w = $this->widget('security_overview');
        self::assertSame('ok', $w['state']);
        foreach (['Firewall', 'fail2ban', 'Malware scanner', 'ModSecurity', 'Failed SSH today', '203.0.113.7', '(2 jails)', '(1 high)'] as $s) {
            self::assertStringContainsString($s, $w['html']);
        }
        self::assertStringContainsString('>14<', $w['html'], 'failed SSH logins today');
        self::assertStringContainsString('>5<', $w['html'], 'banned across jails');
    }

    public function testASourceThatCannotAnswerIsNotAGoodNews(): void
    {
        $this->agent->succeed('firewall.status', ['installed' => true, 'active' => false]);
        $this->login('admin');
        $html = $this->widget('security_overview')['html'];
        self::assertStringContainsString('N/A', $html);
        self::assertStringNotContainsString('Active', str_replace('Brute force protection', '', explode('Firewall', $html)[1] ?? ''), 'unreadable sources are not "Active"');
    }

    public function testNoAgentAnswerAtAllIsUnavailableNotZeros(): void
    {
        $this->login('admin');
        $w = $this->widget('security_overview');
        self::assertSame('unavailable', $w['state']);
        self::assertStringNotContainsString('Failed SSH', $w['html']);
    }

    public function testSslCountsExpiryAndRenewal(): void
    {
        $cert = static fn (string $n, int $days, array $extra = []): array => ['source' => 'letsencrypt', 'name' => $n, 'used_by' => ['x.conf'], 'certificate' => ['days_remaining' => $days, 'expired' => $days < 0, 'issuer' => 'R3', 'self_signed' => false] + $extra];
        $this->agent->succeed('ssl.store', [
            'certificates' => [$cert('ok.test', 80), $cert('soon.test', 5), $cert('old.test', -3), ['source' => 'nimvor', 'name' => 'bad', 'used_by' => [], 'certificate' => ['error' => 'unreadable']]],
            'hosts' => [
                ['name' => 'ok.test', 'enabled' => true, 'https' => true, 'covered' => true],
                ['name' => 'wrong.test', 'enabled' => true, 'https' => true, 'covered' => false],
                ['name' => 'plain.test', 'enabled' => true, 'https' => false, 'covered' => false],
                ['name' => 'off.test', 'enabled' => false, 'https' => false, 'covered' => false],
            ],
        ]);
        $this->agent->succeed('ssl.renewal_status', ['certbot_installed' => true, 'timer_active' => false]);
        $this->login('admin');
        $w = ['html' => $this->tile('ssl')];
        self::assertStringContainsString('Automatic renewal is off', $w['html']);
        self::assertStringContainsString('expired 3 days ago', $w['html']);
        self::assertStringContainsString('wrong.test', $w['html']);
        self::assertStringContainsString('1 enabled website has no HTTPS', $w['html']);
        self::assertStringNotContainsString('off.test', $w['html'], 'a disabled website is not reported');
        self::assertStringNotContainsString('unreadable', $w['html']);
    }

    public function testNothingToShowIsEmptyAndAnUnreachableAgentIsUnavailable(): void
    {
        $this->agent->succeed('ssl.store', ['certificates' => [], 'hosts' => []]);
        $this->login('admin');
        self::assertStringContainsString('Certificates', $this->tile('ssl'), 'with nothing to show the card still opens');
    }

    public function testBothWidgetsNeedTheirPermission(): void
    {
        $this->security();
        $this->login('plain');
        $res = $this->request('GET', '/dashboard/widgets/security_overview', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertContains($res->status(), [302, 303, 403]);
        self::assertStringNotContainsString('Failed SSH', $res->body());
        self::assertSame(0, $this->calls('security.audit'), 'nothing was collected for someone who may not see it');
    }
}
