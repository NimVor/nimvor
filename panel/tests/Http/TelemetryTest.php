<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Agent\AgentClient;
use NimVor\Database\Connection;
use NimVor\Security\Crypto;
use NimVor\Services\AuditLogger;
use NimVor\Services\TelemetryService;
use NimVor\Tests\Support\HttpTestCase;

/** Opt-in telemetry: off by default, what is sent, retries, deletion and who may change it. */
final class TelemetryTest extends HttpTestCase
{
    /** @var list<array{method: string, url: string, headers: array<string, string>, body: array<string, mixed>}> */
    private array $sent = [];
    /** @var list<array{0: int, 1: ?string}> answers in order; the last one repeats */
    private array $answers = [];
    private string $optIn;
    private string $events;

    protected function setUp(): void
    {
        parent::setUp();
        $this->optIn = $this->dir . '/telemetry-optin';
        $this->events = $this->dir . '/release-events.jsonl';
        $this->agent->succeed('system.info', ['agent_version' => '1.9.0', 'os' => [], 'system' => ['hostname' => 'secret-host.example.com', 'uptime_seconds' => 3 * 86400 + 5]]);
        $this->agent->succeed('telemetry.collect', [
            'os' => ['id' => 'ubuntu', 'version' => '24.04', 'arch' => 'amd64', 'kernel_major' => '6.8', 'virtualization' => 'kvm', 'init_system' => 'systemd', 'cpu_cores' => 4, 'memory_mb' => 7900, 'disk_gb' => 100],
            'software' => [['component' => 'nginx', 'version' => '1.24', 'running' => true]],
            'health' => ['services_total' => 6, 'services_failed' => 1, 'disk_used_percent' => 41, 'memory_used_percent' => 30, 'updates_pending' => true],
        ]);
    }

    private function svc(string $version = '1.9.0'): TelemetryService
    {
        $c = $this->app->container;
        return new TelemetryService(
            $c->get(Connection::class),
            $c->get(AgentClient::class),
            $c->get(Crypto::class),
            $c->get(AuditLogger::class),
            $version,
            'https://telemetry.example.test/api/v1/telemetry',
            $this->optIn,
            $this->events,
            function (string $method, string $url, array $headers, string $body): array {
                $this->sent[] = ['method' => $method, 'url' => $url, 'headers' => $headers, 'body' => (array) json_decode($body, true)];
                $a = $this->answers[min(count($this->sent) - 1, count($this->answers) - 1)] ?? [200, '{"status":"ok"}'];
                return $a;
            },
        );
    }

    private function db(): Connection
    {
        return $this->app->container->get(Connection::class);
    }

    private function endpoints(): array
    {
        return array_map(static fn (array $s): string => basename($s['url']), $this->sent);
    }

    /** register answered with a token, everything else ok */
    private function accept(): void
    {
        $this->answers = [[201, '{"status":"registered","token":"tok-abc-123"}'], [200, '{"status":"ok"}']];
    }

    public function testNothingIsMadeOrSentUntilAnAdministratorTurnsItOn(): void
    {
        $t = $this->svc();
        self::assertSame([], $t->run());
        self::assertSame([], $this->sent);
        self::assertFalse($t->enabled());
        self::assertSame(0, (int) $this->db()->scalar('SELECT COUNT(*) FROM telemetry_state'), 'not even an installation id exists');
        self::assertNull($t->status()['uuid']);
    }

    public function testTurningItOnRegistersFirstAndThenSendsEachReportWithTheToken(): void
    {
        $t = $this->svc();
        $this->accept();
        $t->enable(1);
        $t->run();
        self::assertSame(['register'], $this->endpoints(), 'nothing else before the service has answered');
        $reg = $this->sent[0];
        self::assertSame(1, $reg['body']['schema']);
        self::assertMatchesRegularExpression('/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/', $reg['body']['installation_uuid']);
        self::assertSame('1.9.0', $reg['body']['agent_version']);
        self::assertArrayNotHasKey('Authorization', $reg['headers']);
        self::assertNotSame('tok-abc-123', $this->db()->scalar("SELECT value FROM telemetry_state WHERE key = 'token'"), 'the token is stored encrypted');

        $this->sent = [];
        $t->run();
        self::assertEqualsCanonicalizing(['heartbeat', 'health', 'inventory', 'features'], $this->endpoints());
        foreach ($this->sent as $s) {
            self::assertSame('Bearer tok-abc-123', $s['headers']['Authorization']);
            self::assertSame($reg['body']['installation_uuid'], $s['body']['installation_uuid']);
            self::assertMatchesRegularExpression('/^[0-9a-f-]{36}$/', $s['body']['event_id']);
        }
        $by = array_column($this->sent, 'body', 'url');
        $hb = $by['https://telemetry.example.test/api/v1/telemetry/heartbeat'];
        self::assertSame([3, '1.9.0'], [$hb['uptime_days'], $hb['nimvor_version']]);
        $inv = $by['https://telemetry.example.test/api/v1/telemetry/inventory'];
        self::assertSame('ubuntu', $inv['os']['id']);
        self::assertSame('nginx', $inv['software'][0]['component']);
        $feat = $by['https://telemetry.example.test/api/v1/telemetry/features']['features'];
        self::assertArrayHasKey('websites', $feat);
        self::assertSame([], array_filter($feat, static fn (mixed $v): bool => !is_int($v)), 'counts only');
        $health = $by['https://telemetry.example.test/api/v1/telemetry/health'];
        self::assertSame(41, $health['disk_used_percent']);
        self::assertArrayHasKey('failed_jobs', $health);

        $this->sent = [];
        $t->run();
        self::assertSame([], $this->sent, 'nothing is due again for a day');
    }

    public function testNothingThatNamesTheServerIsEverSent(): void
    {
        $t = $this->svc();
        $this->accept();
        $t->enable(1);
        $t->run();
        $t->run();
        $all = (string) json_encode($this->sent);
        foreach (['secret-host', 'example.com', '/etc/', '192.0.2'] as $bad) {
            self::assertStringNotContainsString($bad, $all, $bad . ' leaked into a report');
        }
        $stored = (string) json_encode($this->db()->fetchAll('SELECT * FROM telemetry_queue'));
        self::assertStringNotContainsString('secret-host', $stored);
    }

    public function testAnAgentThatCannotBeReachedSkipsInventoryAndHealthButNotTheRest(): void
    {
        $this->agent->fail('telemetry.collect', 500, 'internal', 'down');
        $t = $this->svc();
        $this->accept();
        $t->enable(1);
        $t->run();
        $this->sent = [];
        $t->run();
        self::assertEqualsCanonicalizing(['heartbeat', 'features', 'health'], $this->endpoints(), 'inventory needs the agent; health still has what the panel knows');
        self::assertNotContains('inventory', $this->endpoints());
    }

    public function testTurningItOffStopsEverythingAndForgetsWhatWasWaiting(): void
    {
        $t = $this->svc();
        $this->accept();
        $t->enable(1);
        $t->run();
        $t->disable(1);
        $this->sent = [];
        self::assertSame([], $t->run());
        self::assertSame([], $this->sent);
        self::assertSame(0, (int) $this->db()->scalar('SELECT COUNT(*) FROM telemetry_queue'));
        $t->enable(1);
        self::assertSame($this->db()->scalar("SELECT value FROM telemetry_state WHERE key = 'uuid'"), $t->status()['uuid'], 'turning it on again continues the same installation');
    }

    public function testAFailureIsRetriedWithBackOffAndNeverAllAtOnce(): void
    {
        $t = $this->svc();
        $this->answers = [[0, null]];
        $t->enable(1);
        $t->run();
        self::assertSame(['register'], $this->endpoints());
        $row = $this->db()->fetch('SELECT * FROM telemetry_queue');
        self::assertSame(1, (int) $row['attempts']);
        self::assertGreaterThan(time() + 60, strtotime((string) $row['next_attempt_at']), 'the next try waits');
        $t->run();
        self::assertSame(['register'], $this->endpoints(), 'not tried again before its time');
        $this->db()->run('UPDATE telemetry_queue SET next_attempt_at = ?', [gmdate('c', time() - 5)]);
        $this->answers = [[201, '{"token":"tok"}']];
        $this->sent = [];
        $t->run();
        self::assertSame('1', $this->db()->scalar("SELECT value FROM telemetry_state WHERE key = 'registered'"));
        self::assertSame('', $t->status()['last_error']);
    }

    public function testAServerErrorBacksOffLongerEachTimeAndARateLimitWaitsAnHour(): void
    {
        $t = $this->svc();
        $this->answers = [[503, '{}']];
        $t->enable(1);
        $t->run();
        $first = strtotime((string) $this->db()->scalar('SELECT next_attempt_at FROM telemetry_queue'));
        $this->db()->run('UPDATE telemetry_queue SET next_attempt_at = ?', [gmdate('c', time() - 1)]);
        $t->run();
        $second = strtotime((string) $this->db()->scalar('SELECT next_attempt_at FROM telemetry_queue'));
        self::assertGreaterThan($first, $second);
        $this->answers = [[429, '{"error":"rate_limited"}']];
        $this->db()->run('UPDATE telemetry_queue SET next_attempt_at = ?', [gmdate('c', time() - 1)]);
        $t->run();
        self::assertGreaterThanOrEqual(time() + 3500, strtotime((string) $this->db()->scalar('SELECT next_attempt_at FROM telemetry_queue')));
    }

    public function testAReportOlderThanSevenDaysIsDropped(): void
    {
        $t = $this->svc();
        $this->answers = [[0, null]];
        $t->enable(1);
        $t->run();
        $this->db()->run('UPDATE telemetry_queue SET created_at = ?, next_attempt_at = ?', [gmdate('c', time() - 8 * 86400), gmdate('c', time() - 1)]);
        $this->sent = [];
        $t->flush();
        self::assertSame([], $this->sent);
        self::assertSame(0, (int) $this->db()->scalar('SELECT COUNT(*) FROM telemetry_queue'));
    }

    public function testARefusedReportIsDroppedAndTheReasonKept(): void
    {
        $t = $this->svc();
        $this->answers = [[201, '{"token":"tok"}'], [422, '{"error":"invalid_report","field":"os.version"}']];
        $t->enable(1);
        $t->run();
        $t->run();
        self::assertSame(0, (int) $this->db()->scalar('SELECT COUNT(*) FROM telemetry_queue WHERE endpoint = \'inventory\''));
        self::assertStringContainsString('refused', $t->status()['last_error']);
    }

    public function testALostTokenMeansANewIdAndANewRegistration(): void
    {
        $t = $this->svc();
        $this->answers = [[201, '{"token":"tok"}'], [401, '{"error":"unauthorized"}']];
        $t->enable(1);
        $t->run();
        $old = $t->status()['uuid'];
        $t->run();
        self::assertNotSame($old, $t->status()['uuid']);
        self::assertFalse($t->status()['registered']);
        $this->answers = [[201, '{"token":"tok2"}']];
        $this->sent = [];
        $t->run();
        self::assertSame(['register'], $this->endpoints());
    }

    public function testTheServiceSayingGoneTurnsReportingOff(): void
    {
        $t = $this->svc();
        $this->answers = [[201, '{"token":"tok"}'], [410, '{"error":"deleted"}']];
        $t->enable(1);
        $t->run();
        $t->run();
        self::assertFalse($t->enabled());
        self::assertStringContainsString('no record', $t->status()['last_error']);
    }

    public function testADeletionRequestIsSentAndRetriedUntilTheServiceAnswers(): void
    {
        $t = $this->svc();
        $this->accept();
        $t->enable(1);
        $t->run();
        $uuid = $t->status()['uuid'];
        $this->sent = [];
        $this->answers = [[0, null]];
        $t->unregister(true, 1);
        self::assertSame(['unregister'], $this->endpoints());
        self::assertTrue($this->sent[0]['body']['delete']);
        self::assertTrue($t->status()['pending_deletion']);
        self::assertSame($uuid, $t->status()['uuid'], 'the id is kept until the service has confirmed');
        $this->db()->run('UPDATE telemetry_queue SET next_attempt_at = ?', [gmdate('c', time() - 1)]);
        $this->answers = [[200, '{"status":"deleted"}']];
        $t->run();
        self::assertNull($t->status()['uuid']);
        self::assertSame(0, (int) $this->db()->scalar('SELECT COUNT(*) FROM telemetry_queue'));
        self::assertFalse($t->enabled());
    }

    public function testUnregisteringWithoutEverHavingRegisteredSendsNothing(): void
    {
        $t = $this->svc();
        $t->enable(1);
        $t->unregister(false, 1);
        self::assertSame([], $this->sent);
        self::assertNull($t->status()['uuid']);
    }

    public function testTheInstallersYesIsAdoptedOnceAndOnlyOnce(): void
    {
        file_put_contents($this->optIn, "yes\n");
        $t = $this->svc();
        $this->accept();
        $t->run();
        self::assertTrue($t->enabled());
        self::assertSame(['register'], $this->endpoints());
        $t->disable(1);
        $t->run();
        self::assertFalse($t->enabled(), 'a later "off" is not undone by the old file');
    }

    public function testNoInstallerFileMeansNoTelemetry(): void
    {
        $t = $this->svc();
        $t->run();
        self::assertFalse($t->enabled());
        self::assertSame([], $this->sent);
    }

    public function testReleaseEventsFromTheInstallerAreSentOnceInTheHeartbeat(): void
    {
        $id = TelemetryService::uuid();
        file_put_contents($this->events, json_encode(['event_id' => $id, 'kind' => 'upgrade', 'from_version' => '1.8.1', 'to_version' => '1.9.0', 'success' => true, 'occurred_at' => gmdate('c'), 'hostname' => 'leak.example.com']) . "\nnot json\n");
        $t = $this->svc();
        $this->accept();
        $t->enable(1);
        $t->run();
        $t->run();
        $hb = array_values(array_filter($this->sent, static fn (array $s): bool => str_ends_with($s['url'], '/heartbeat')))[0]['body'];
        self::assertSame($id, $hb['events'][0]['event_id']);
        self::assertSame(['event_id', 'kind', 'from_version', 'to_version', 'success', 'occurred_at'], array_keys($hb['events'][0]), 'unknown fields are not forwarded');
        $this->db()->run("UPDATE telemetry_state SET value = '1' WHERE key = 'last_heartbeat'");
        $this->db()->run("UPDATE telemetry_state SET value = '1' WHERE key = 'last_heartbeat'");
        $this->sent = [];
        $t->run();
        self::assertArrayNotHasKey('events', array_values(array_filter($this->sent, static fn (array $s): bool => str_ends_with($s['url'], '/heartbeat')))[0]['body'] ?? [], 'sent events are not repeated');
    }

    public function testADevelopmentBuildReportsNothing(): void
    {
        $t = $this->svc('dev');
        $t->enable(1);
        $t->run();
        self::assertSame([], $this->sent);
    }

    // ------------------------------------------------------------------ the page

    public function testThePageExplainsAndOffersTheChoiceAndShowsExactlyWhatWouldBeSent(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $page = $this->request('GET', '/settings/telemetry');
        self::assertSame(200, $page->status());
        self::assertStringContainsString('Turn telemetry on', $page->body());
        self::assertStringContainsString('Nothing is sent', $page->body());
        self::assertStringContainsString('&quot;component&quot;: &quot;nginx&quot;', $page->body(), 'the real next report is shown');
        self::assertStringNotContainsString('secret-host', $page->body());
        self::assertStringContainsString('/settings/telemetry', (string) $this->request('GET', '/settings')->body());
    }

    public function testAdministratorsTurnItOnAndOffAndItIsAudited(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $on = $this->post('/settings/telemetry', ['action' => 'enable']);
        self::assertSame('/settings/telemetry', $on->header('Location'));
        $row = $this->db()->fetch("SELECT value FROM telemetry_state WHERE key = 'enabled'");
        self::assertSame('1', $row['value']);
        self::assertStringContainsString('Turn off', $this->request('GET', '/settings/telemetry')->body());
        $this->post('/settings/telemetry', ['action' => 'disable']);
        self::assertSame('0', $this->db()->fetch("SELECT value FROM telemetry_state WHERE key = 'enabled'")['value']);
        $actions = array_column($this->db()->fetchAll("SELECT action FROM audit_logs WHERE action LIKE 'telemetry.%' ORDER BY id"), 'action');
        self::assertSame(['telemetry.enable', 'telemetry.disable'], $actions);
        $this->post('/settings/telemetry', ['action' => 'nonsense']);
        self::assertSame('0', $this->db()->fetch("SELECT value FROM telemetry_state WHERE key = 'enabled'")['value']);
    }

    public function testAViewerCanLookButNotChange(): void
    {
        $this->seedUser('viewer', 'viewer');
        $this->login('viewer');
        self::assertSame(403, $this->post('/settings/telemetry', ['action' => 'enable'])->status());
        self::assertSame(0, (int) $this->db()->scalar('SELECT COUNT(*) FROM telemetry_state'));
    }

    public function testASignedOutVisitorIsSentToTheLoginPage(): void
    {
        self::assertSame(303, $this->request('GET', '/settings/telemetry')->status());
    }
}
