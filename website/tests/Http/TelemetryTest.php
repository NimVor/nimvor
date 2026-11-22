<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Core\Request;
use NimVorSite\Core\Response;
use NimVorSite\Tests\Support\SiteTestCase;

final class TelemetryTest extends SiteTestCase
{
    private const UUID = '3f2b8c1e-5a47-4d0e-9b1c-7a6d2e4f8091';

    private static function id(): string
    {
        $b = random_bytes(16);
        $b[6] = chr((ord($b[6]) & 0x0f) | 0x40);
        $b[8] = chr((ord($b[8]) & 0x3f) | 0x80);
        return vsprintf('%s%s-%s-%s-%s-%s%s%s', str_split(bin2hex($b), 4));
    }

    /** @param array<string, mixed>|string $body */
    private function send(string $endpoint, array|string $body, ?string $token = null, string $type = 'application/json'): Response
    {
        $raw = is_array($body) ? (string) json_encode($body) : $body;
        $server = ['REMOTE_ADDR' => $this->ip, 'CONTENT_TYPE' => $type] + ($token === null ? [] : ['HTTP_AUTHORIZATION' => 'Bearer ' . $token]);
        return $this->app->handle(new Request('POST', '/api/v1/telemetry/' . $endpoint, [], [], [], $server, $raw));
    }

    /** @return array<string, mixed> */
    private function json(Response $r): array
    {
        return (array) json_decode($r->body, true);
    }

    /** @return array<string, mixed> */
    private function env(array $more = [], string $uuid = self::UUID): array
    {
        return $more + ['schema' => 1, 'installation_uuid' => $uuid, 'event_id' => self::id()];
    }

    private function register(string $uuid = self::UUID): string
    {
        $r = $this->send('register', $this->env(['nimvor_version' => '1.9.0', 'agent_version' => '1.9.0'], $uuid));
        self::assertSame(201, $r->status, $r->body);
        return (string) $this->json($r)['token'];
    }

    public function testRegisteringGivesATokenAndStoresOnlyTheIdAndVersions(): void
    {
        $r = $this->send('register', $this->env(['nimvor_version' => '1.9.0', 'agent_version' => '1.9.1']));
        self::assertSame(201, $r->status);
        self::assertSame([], $r->cookies());
        self::assertStringContainsString('no-store', $r->headers['Cache-Control']);
        $token = $this->json($r)['token'];
        self::assertMatchesRegularExpression('/^[a-f0-9]{48}$/', $token);
        $row = $this->row('SELECT * FROM telemetry_installations');
        self::assertSame(hash('sha256', $token), $row['token_hash'], 'only a hash of the token is kept');
        self::assertSame(['1.9.0', '1.9.1', 'active'], [$row['nimvor_version'], $row['agent_version'], $row['status']]);
        $cols = array_column($this->app->db->all('SHOW COLUMNS FROM telemetry_installations'), 'Field');
        self::assertSame(['id', 'installation_uuid', 'token_hash', 'token_confirmed', 'status', 'schema_version', 'nimvor_version', 'agent_version', 'registered_at', 'last_seen', 'unregistered_at'], $cols, 'no address, host name or contact is stored');
        $api = array_column($this->app->db->all('SHOW COLUMNS FROM telemetry_api_events'), 'Field');
        self::assertSame(['id', 'received_at', 'endpoint', 'status', 'code', 'schema_version'], $api, 'the request log has no address');
    }

    public function testALostRegistrationAnswerCanBeRetriedButAUsedOneCannot(): void
    {
        $first = $this->register();
        $second = $this->register();
        self::assertNotSame($first, $second, 'the token was never used, so a new one is issued');
        self::assertSame(401, $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0']), $first)->status);
        self::assertSame(200, $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0']), $second)->status);
        self::assertSame(409, $this->send('register', $this->env(['nimvor_version' => '1.9.0']))->status, 'once in use, nobody can take the id over');
        self::assertSame(1, $this->rows('telemetry_installations'));
    }

    public function testEveryReportNeedsTheRightToken(): void
    {
        $token = $this->register();
        $body = $this->env(['nimvor_version' => '1.9.0']);
        self::assertSame(401, $this->send('heartbeat', $body)->status);
        self::assertSame(401, $this->send('heartbeat', $body, 'wrong')->status);
        self::assertSame(401, $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0'], '11111111-1111-4111-8111-111111111111'), $token)->status, 'the same answer for an unknown id');
        self::assertSame(401, $this->send('unregister', $this->env(['delete' => true]))->status, 'nobody else can erase an installation');
        self::assertSame(1, $this->rows('telemetry_installations'));
        self::assertSame(0, $this->rows('telemetry_heartbeats'));
    }

    public function testAHeartbeatIsCountedOnceWhenRetried(): void
    {
        $token = $this->register();
        $body = $this->env(['nimvor_version' => '1.9.1', 'agent_version' => '1.9.1', 'uptime_days' => 12]);
        self::assertSame('ok', $this->json($this->send('heartbeat', $body, $token))['status']);
        self::assertSame('duplicate', $this->json($this->send('heartbeat', $body, $token))['status'], 'the same event id is recognised');
        self::assertSame(1, $this->rows('telemetry_heartbeats'));
        self::assertSame('1.9.1', $this->row('SELECT nimvor_version FROM telemetry_installations')['nimvor_version'], 'the installation shows its current version');
    }

    public function testReleaseEventsRideOnTheHeartbeat(): void
    {
        $token = $this->register();
        $up = ['event_id' => self::id(), 'kind' => 'upgrade', 'from_version' => '1.8.1', 'to_version' => '1.9.0', 'success' => true];
        $bad = ['event_id' => self::id(), 'kind' => 'upgrade', 'from_version' => '1.9.0', 'to_version' => '1.9.1', 'success' => false];
        self::assertSame(200, $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0', 'events' => [$up, $bad]]), $token)->status);
        self::assertSame(2, $this->rows('telemetry_release_events'));
        $r = $this->app->telemetry->releases(30);
        self::assertSame([2, 1], [$r['upgrades'], $r['upgrades_ok']]);
        $bad['kind'] = 'format-disk';
        self::assertSame(422, $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0', 'events' => [$bad]]), $token)->status);
    }

    public function testInventoryIsRoundedAndOnlyKnownSoftwareIsKept(): void
    {
        $token = $this->register();
        $inv = ['os' => ['id' => 'ubuntu', 'version' => '24.04', 'arch' => 'amd64', 'kernel_major' => '6.8', 'virtualization' => 'kvm', 'init_system' => 'systemd', 'cpu_cores' => 3, 'memory_mb' => 3900, 'disk_gb' => 52],
            'software' => [['component' => 'nginx', 'version' => '1.24', 'running' => true], ['component' => 'php', 'version' => '8.3', 'running' => true], ['component' => 'mariadb', 'version' => '10.11', 'running' => false]]];
        self::assertSame(200, $this->send('inventory', $this->env($inv), $token)->status);
        $os = $this->row('SELECT * FROM telemetry_os_inventory');
        self::assertSame([4096, 80, 3, 'debian', 'kvm'], [(int) $os['memory_mb_bucket'], (int) $os['disk_gb_bucket'], (int) $os['cpu_cores'], $os['os_family'], $os['virtualization']], 'sizes are rounded up to a bucket');
        self::assertSame(3, $this->rows('telemetry_software_inventory'));
        $inv['software'] = [['component' => 'nginx', 'version' => '1.26', 'running' => true]];
        $this->send('inventory', $this->env($inv), $token);
        self::assertSame(1, $this->rows('telemetry_software_inventory'), 'a new inventory replaces the old list');
        self::assertSame(1, $this->rows('telemetry_os_inventory'));
        $inv['software'] = [['component' => 'my-secret-app', 'version' => '1.0']];
        self::assertSame(422, $this->send('inventory', $this->env($inv), $token)->status, 'a component that is not on the list has no place to go');
    }

    public function testFeaturesAreCountsOnly(): void
    {
        $token = $this->register();
        $r = $this->send('features', $this->env(['features' => ['websites' => 12, 'mailboxes' => 0, 'not_a_feature' => 5]]), $token);
        self::assertSame([200, 1], [$r->status, $this->json($r)['ignored']]);
        self::assertSame(2, $this->rows('telemetry_feature_snapshots'));
        self::assertSame(422, $this->send('features', $this->env(['features' => ['websites' => 'example.com']]), $token)->status, 'a name is not a count');
        self::assertSame(422, $this->send('features', $this->env(['features' => ['websites' => -1]]), $token)->status);
        self::assertSame(422, $this->send('features', $this->env(['features' => ['unknown' => 1]]), $token)->status, 'nothing usable at all is refused');
    }

    public function testHealthIsBucketed(): void
    {
        $token = $this->register();
        $r = $this->send('health', $this->env(['services_total' => 14, 'services_failed' => 1, 'disk_used_percent' => 87, 'memory_used_percent' => 41, 'updates_pending' => true, 'last_backup_ok' => false, 'ssl_expiring' => false, 'failed_jobs' => 0]), $token);
        self::assertSame(200, $r->status);
        $h = $this->row('SELECT * FROM telemetry_health_snapshots');
        self::assertSame([80, 40, 1, 0], [(int) $h['disk_used_bucket'], (int) $h['memory_used_bucket'], (int) $h['updates_pending'], (int) $h['last_backup_ok']]);
        self::assertSame(422, $this->send('health', $this->env(['disk_used_percent' => 101]), $token)->status);
        self::assertSame(422, $this->send('health', $this->env([]), $token)->status, 'an empty report is refused');
    }

    public function testMalformedRequestsAreRefusedWithAClearCode(): void
    {
        self::assertSame(415, $this->send('register', '{}', null, 'text/plain')->status);
        self::assertSame(400, $this->send('register', '[1,2]')->status);
        self::assertSame(400, $this->send('register', 'nope')->status);
        $big = $this->send('register', str_repeat('x', 20000));
        self::assertSame(413, $big->status);
        $old = $this->send('register', ['schema' => 99, 'installation_uuid' => self::UUID, 'event_id' => self::id(), 'nimvor_version' => '1.9.0']);
        self::assertSame([400, 'unsupported_schema', [1]], [$old->status, $this->json($old)['error'], $this->json($old)['supported']]);
        self::assertSame(422, $this->send('register', $this->env(['nimvor_version' => 'abc'])) ->status);
        self::assertSame(422, $this->send('register', ['schema' => 1, 'installation_uuid' => 'not-a-uuid', 'event_id' => self::id(), 'nimvor_version' => '1.9.0'])->status);
        self::assertSame(404, $this->send('nothing', $this->env())->status);
        self::assertSame(0, $this->rows('telemetry_installations'));
    }

    public function testUnregisteringStopsTheReportsAndKeepsWhatWasSent(): void
    {
        $token = $this->register();
        $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0']), $token);
        self::assertSame('unregistered', $this->json($this->send('unregister', $this->env(), $token))['status']);
        self::assertSame(410, $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0']), $token)->status);
        self::assertSame(1, $this->rows('telemetry_heartbeats'));
        self::assertSame(0, $this->app->telemetry->totals(30)['active'], 'it no longer counts as active');
        self::assertSame(1, $this->app->telemetry->totals(30)['unregistered']);
    }

    public function testADeletionRequestErasesEverythingAndTheIdCannotComeBack(): void
    {
        $token = $this->register();
        $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0', 'events' => [['event_id' => self::id(), 'kind' => 'install', 'from_version' => '', 'to_version' => '1.9.0', 'success' => true]]]), $token);
        $this->send('features', $this->env(['features' => ['websites' => 3]]), $token);
        $this->send('inventory', $this->env(['os' => ['id' => 'debian', 'version' => '12', 'arch' => 'amd64'], 'software' => [['component' => 'nginx', 'version' => '1.22']]]), $token);
        $this->send('health', $this->env(['services_failed' => 0]), $token);
        self::assertSame('deleted', $this->json($this->send('unregister', $this->env(['delete' => true]), $token))['status']);
        foreach (['telemetry_installations', 'telemetry_heartbeats', 'telemetry_os_inventory', 'telemetry_software_inventory', 'telemetry_feature_snapshots', 'telemetry_health_snapshots', 'telemetry_release_events'] as $t) {
            self::assertSame(0, $this->rows($t), $t . ' was not emptied');
        }
        self::assertSame(410, $this->send('register', $this->env(['nimvor_version' => '1.9.0']))->status, 'a late retry cannot register the id again');
        self::assertSame(1, $this->rows('telemetry_deleted'));
        self::assertSame(hash('sha256', self::UUID), $this->row('SELECT uuid_hash FROM telemetry_deleted')['uuid_hash'], 'only a hash is left');
    }

    public function testRateLimitsApplyPerInstallationAndPerAddress(): void
    {
        $token = $this->register();
        $last = null;
        for ($i = 0; $i < 14; $i++) {
            $last = $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0']), $token);
        }
        self::assertSame(429, $last->status);
        self::assertSame('3600', $last->headers['Retry-After']);
        self::assertSame(200, $this->send('features', $this->env(['features' => ['websites' => 1]]), $token)->status, 'another report type has its own allowance');
    }

    public function testTheDashboardIsForAdministratorsAndShowsNotAvailableWhenEmpty(): void
    {
        self::assertSame(303, $this->get('/admin/telemetry')->status);
        $this->loginAs('admin', 'boss@example.test');
        $empty = $this->get('/admin/telemetry');
        self::assertSame(200, $empty->status);
        self::assertStringContainsString('No installation has registered yet', $empty->body);
        self::assertStringContainsString('N/A', $empty->body);
        self::assertStringNotContainsString('<svg class="chart"', $empty->body, 'no chart is drawn without data');
    }

    public function testTheDashboardAndExportsShowWhatWasReported(): void
    {
        $token = $this->register();
        $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0']), $token);
        $this->send('inventory', $this->env(['os' => ['id' => 'ubuntu', 'version' => '24.04', 'arch' => 'amd64', 'cpu_cores' => 4, 'memory_mb' => 8000, 'disk_gb' => 100], 'software' => [['component' => 'nginx', 'version' => '1.24', 'running' => true]]]), $token);
        $this->send('features', $this->env(['features' => ['websites' => 5, 'mailboxes' => 0]]), $token);
        $this->send('health', $this->env(['services_failed' => 2, 'disk_used_percent' => 93]), $token);
        $this->loginAs('admin', 'boss@example.test');
        $page = $this->get('/admin/telemetry')->body;
        self::assertStringContainsString('ubuntu 24.04', $page);
        self::assertStringContainsString('nginx', $page);
        self::assertStringContainsString('websites', $page);
        self::assertStringContainsString('<svg class="chart"', $page);
        self::assertStringNotContainsString(self::UUID, $page, 'an installation id is never shown');
        self::assertStringContainsString('ubuntu 24.04', $this->get('/admin/telemetry?os=' . rawurlencode('ubuntu 24.04') . '&days=7')->body);
        $csv = $this->get('/admin/telemetry/export.csv?what=software');
        self::assertSame('text/csv; charset=UTF-8', $csv->headers['Content-Type']);
        self::assertStringContainsString("component,installations,running\nnginx,1,1", $csv->body);
        self::assertStringContainsString('websites,1,1,5', $this->get('/admin/telemetry/export.csv?what=features')->body);
        self::assertSame(404, $this->get('/admin/telemetry/export.csv?what=installations')->status, 'the id list cannot be exported');
    }

    public function testRetentionRemovesOldDetailButNotTheInstallation(): void
    {
        $token = $this->register();
        $this->send('heartbeat', $this->env(['nimvor_version' => '1.9.0']), $token);
        $this->send('features', $this->env(['features' => ['websites' => 1]]), $token);
        $this->app->telemetry->logApi('heartbeat', 200, '', 1);
        $this->app->db->run('UPDATE telemetry_heartbeats SET received_at = DATE_SUB(UTC_TIMESTAMP(), INTERVAL 120 DAY)');
        $this->app->db->run('UPDATE telemetry_feature_snapshots SET captured_at = DATE_SUB(UTC_TIMESTAMP(), INTERVAL 200 DAY)');
        $this->app->db->run('UPDATE telemetry_api_events SET received_at = DATE_SUB(UTC_TIMESTAMP(), INTERVAL 40 DAY)');
        $gone = $this->app->telemetry->purge();
        self::assertSame([1, 1], [$gone['heartbeats'], $gone['features']]);
        self::assertGreaterThanOrEqual(1, $gone['api_events']);
        self::assertSame(1, $this->rows('telemetry_installations'));
    }
}
