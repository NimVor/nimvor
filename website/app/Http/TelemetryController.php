<?php

declare(strict_types=1);

namespace NimVorSite\Http;

use NimVorSite\Core\Ctx;
use NimVorSite\Core\Response;
use NimVorSite\Services\TelemetryService;

/**
 * POST /api/v1/telemetry/{endpoint}: the opt-in reports of NimVor installations (a server's panel, not a browser:
 * no session, no CSRF token; a bearer token proves which installation sends).
 * GET /admin/telemetry: the developer dashboard (administrators only), and its CSV export.
 */
final class TelemetryController extends Controller
{
    private const PERIODS = [7, 30, 90, 365];

    /** @param array<string, string> $params */
    public function receive(Ctx $ctx, array $params): Response
    {
        $json = static fn (int $status, array $body, array $headers = []): Response => (function () use ($status, $body, $headers): Response {
            $r = Response::text((string) json_encode($body, JSON_UNESCAPED_SLASHES) . "\n", 'application/json', $status)->header('Cache-Control', 'no-store');
            foreach ($headers as $k => $v) {
                $r->header($k, $v);
            }
            return $r;
        })();
        $endpoint = (string) ($params['endpoint'] ?? '');
        if (strlen($ctx->req->body) > TelemetryService::MAX_BODY) {
            return $json(413, ['error' => 'too_large']);
        }
        if (!str_contains((string) ($ctx->req->server['CONTENT_TYPE'] ?? ''), 'json')) {
            return $json(415, ['error' => 'expected_json']);
        }
        $d = json_decode($ctx->req->body, true);
        if (!is_array($d) || array_is_list($d)) {
            return $json(400, ['error' => 'expected_object']);
        }
        $auth = (string) ($ctx->req->server['HTTP_AUTHORIZATION'] ?? '');
        $bearer = stripos($auth, 'Bearer ') === 0 ? trim(substr($auth, 7)) : '';
        [$status, $body, $headers] = $this->app->telemetryService->handle($endpoint, $d, $bearer, $ctx->ip());
        if ($status < 300 && in_array($endpoint, ['unregister', 'register'], true)) {
            $this->app->cache->delete('telemetry:dashboard');
        }
        return $json($status, $body, $headers);
    }

    // ------------------------------------------------------------------ dashboard

    /** @return array{days: int, f: array{version: string, os: string}} */
    private function filters(Ctx $ctx): array
    {
        $days = (int) $ctx->req->input('days', '30');
        $version = $ctx->req->input('version');
        $os = $ctx->req->input('os');
        return [
            'days' => in_array($days, self::PERIODS, true) ? $days : 30,
            'f' => ['version' => in_array($version, $this->app->telemetry->versions(), true) ? $version : '', 'os' => in_array($os, $this->app->telemetry->systems(), true) ? $os : ''],
        ];
    }

    public function dashboard(Ctx $ctx): Response
    {
        ['days' => $days, 'f' => $f] = $this->filters($ctx);
        $t = $this->app->telemetry;
        $chartDays = min($days, 90);
        return $ctx->render('admin/telemetry', [
            'days' => $days, 'f' => $f, 'periods' => self::PERIODS, 'versionsList' => $t->versions(), 'systemsList' => $t->systems(),
            'totals' => $t->totals($days, $f),
            'versions' => $t->breakdown('version', $days, $f), 'agents' => $t->breakdown('agent', $days, $f),
            'systems' => $t->breakdown('os', $days, $f), 'arch' => $t->breakdown('arch', $days, $f), 'virt' => $t->breakdown('virtualization', $days, $f),
            'cpu' => $t->breakdown('cpu', $days, $f), 'memory' => $t->breakdown('memory', $days, $f), 'disk' => $t->breakdown('disk', $days, $f),
            'software' => $t->software($days, $f), 'features' => $t->features($days, $f), 'health' => $t->health($days, $f),
            'releases' => $t->releases($days), 'activeDaily' => $t->activePerDay($chartDays), 'registrations' => $t->registrationsPerDay($chartDays), 'chartDays' => $chartDays,
            'api' => $t->apiHealth(min($days, 30)),
        ]);
    }

    public function export(Ctx $ctx): Response
    {
        ['days' => $days, 'f' => $f] = $this->filters($ctx);
        $t = $this->app->telemetry;
        $what = $ctx->req->input('what');
        $rows = match ($what) {
            'versions' => $this->pairs('nimvor_version', $t->breakdown('version', $days, $f)),
            'systems' => $this->pairs('operating_system', $t->breakdown('os', $days, $f)),
            'architectures' => $this->pairs('architecture', $t->breakdown('arch', $days, $f)),
            'virtualization' => $this->pairs('virtualization', $t->breakdown('virtualization', $days, $f)),
            'software' => $this->table(['component', 'installations', 'running'], array_map(static fn (string $k, array $v): array => [$k, $v['installed'], $v['running']], array_keys($t->software($days, $f)), $t->software($days, $f))),
            'features' => $this->table(['feature', 'installations_using', 'installations_reporting', 'total_items'], array_map(static fn (string $k, array $v): array => [$k, $v['using'], $v['reporting'], $v['total']], array_keys($t->features($days, $f)), $t->features($days, $f))),
            'active' => $this->pairs('date', $t->activePerDay(min($days, 365))),
            'registrations' => $this->pairs('date', $t->registrationsPerDay(min($days, 365))),
            default => null,
        };
        if ($rows === null) {
            return $this->app->error($ctx, 404);
        }
        $fh = fopen('php://temp', 'r+');
        foreach ($rows as $row) {
            fputcsv($fh, array_map(static fn (mixed $c): mixed => is_string($c) && $c !== '' && strpbrk($c[0], "=+-@\t\r") !== false ? "'" . $c : $c, $row), ',', '"', '\\');
        }
        rewind($fh);
        $body = (string) stream_get_contents($fh);
        fclose($fh);
        return Response::text($body, 'text/csv')->header('Content-Disposition', 'attachment; filename="nimvor-telemetry-' . $what . '-' . gmdate('Y-m-d') . '.csv"');
    }

    /** @param array<string, int> $data @return list<list<mixed>> */
    private function pairs(string $label, array $data): array
    {
        return $this->table([$label, 'installations'], array_map(static fn (string $k, int $n): array => [$k, $n], array_keys($data), $data));
    }

    /** @param list<string> $head @param list<list<mixed>> $rows @return list<list<mixed>> */
    private function table(array $head, array $rows): array
    {
        return [$head, ...$rows];
    }
}
