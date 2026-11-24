<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\DnsService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * REST API v1: DNS zones and records. Every endpoint calls DnsService, as the web interface does, so
 * records are validated per type, a zone is only changed when the DNS server accepts it, and a customer
 * only sees the zones of their own websites.
 */
final class DnsApiController
{
    use ApiHelpers;

    public function __construct(private readonly DnsService $dns)
    {
    }

    /**
     * @param array<string, mixed> $z
     * @return array<string, mixed>
     */
    private static function zoneOut(array $z): array
    {
        return [
            'id' => (int) $z['id'],
            'name' => $z['name'],
            'website_id' => ($z['website_id'] ?? null) === null ? null : (int) $z['website_id'],
            'owner_id' => ($z['owner_id'] ?? null) === null ? null : (int) $z['owner_id'],
            'ttl' => (int) $z['ttl'],
            'primary_ns' => $z['primary_ns'],
            'hostmaster' => $z['hostmaster'],
            'secondaries' => array_values(array_filter(explode(',', (string) $z['secondaries']))),
            'serial' => (int) $z['serial'],
            'loaded_at' => $z['applied_at'] ?: null,
            'records' => isset($z['records']) ? (int) $z['records'] : null,
            'created_at' => $z['created_at'],
            'updated_at' => $z['updated_at'],
        ];
    }

    /**
     * @param array<string, mixed> $r
     * @return array<string, mixed>
     */
    private static function recordOut(array $r): array
    {
        return [
            'id' => (int) $r['id'], 'name' => $r['name'], 'type' => $r['type'], 'ttl' => (int) $r['ttl'], 'value' => $r['value'],
            'priority' => (int) $r['priority'], 'weight' => (int) $r['weight'], 'port' => (int) $r['port'], 'disabled' => !empty($r['disabled']),
        ];
    }

    /** @return array<string, mixed>|null */
    private function zone(Request $r, string $id): ?array
    {
        return $this->dns->zone((int) $id, $this->user($r));
    }

    /** @return array<string, mixed> */
    private function zoneWithRecords(int $id, array $actor): array
    {
        $z = (array) $this->dns->zone($id, $actor);
        $records = $this->dns->records($id);
        return self::zoneOut($z + ['records' => count($records)]) + [
            'warnings' => DnsService::warnings((string) $z['name'], $records),
        ];
    }

    public function listZones(Request $r): Response
    {
        return self::ok(array_map([self::class, 'zoneOut'], $this->dns->zones($this->user($r))));
    }

    public function createZone(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $in = self::body($r);
            $in['template'] = filter_var($in['template'] ?? false, FILTER_VALIDATE_BOOL);
            $id = $this->dns->create($in, $this->user($r));
            return self::ok($this->zoneWithRecords($id, $this->user($r)), 201);
        });
    }

    public function showZone(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        return $z === null ? self::notFound() : self::ok($this->zoneWithRecords((int) $z['id'], $this->user($r)));
    }

    public function updateZone(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        if ($z === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $z): Response {
            $in = self::body($r);
            $secondaries = array_key_exists('secondaries', $in) ? implode(',', array_map('strval', (array) $in['secondaries'])) : (string) $z['secondaries'];
            $this->dns->settings($z, (int) ($in['ttl'] ?? $z['ttl']), $secondaries);
            return self::ok($this->zoneWithRecords((int) $z['id'], $this->user($r)));
        });
    }

    public function deleteZone(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        if ($z === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $z): Response {
            $this->dns->delete($z, (string) $r->query('confirm', ''));
            return self::ok(['deleted' => (int) $z['id'], 'name' => $z['name']]);
        });
    }

    public function syncZone(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        if ($z === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $z): Response {
            $this->dns->sync($z);
            return self::ok($this->zoneWithRecords((int) $z['id'], $this->user($r)));
        });
    }

    public function exportZone(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        if ($z === null) {
            return self::notFound();
        }
        return $this->guard(fn (): Response => self::ok(['zone' => $z['name'], 'zone_file' => $this->dns->export($z)]));
    }

    public function importZone(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        if ($z === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $z): Response {
            $res = $this->dns->import($z, (string) (self::body($r)['zone_file'] ?? ''));
            return self::ok($res);
        });
    }

    public function listRecords(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        return $z === null ? self::notFound() : self::ok(array_map([self::class, 'recordOut'], $this->dns->records((int) $z['id'])));
    }

    public function createRecord(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        if ($z === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $z): Response {
            $before = array_column($this->dns->records((int) $z['id']), 'id');
            $this->dns->saveRecord($z, null, self::body($r));
            $new = array_values(array_filter($this->dns->records((int) $z['id']), static fn (array $x): bool => !in_array($x['id'], $before, true)));
            return self::ok(self::recordOut((array) ($new[0] ?? [])), 201);
        });
    }

    public function updateRecord(Request $r, string $id, string $record): Response
    {
        $z = $this->zone($r, $id);
        $rec = $z === null ? null : $this->find((int) $z['id'], (int) $record);
        if ($z === null || $rec === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $z, $rec): Response {
            $this->dns->saveRecord($z, (int) $rec['id'], self::body($r));
            return self::ok(self::recordOut((array) $this->find((int) $z['id'], (int) $rec['id'])));
        });
    }

    public function deleteRecord(Request $r, string $id, string $record): Response
    {
        $z = $this->zone($r, $id);
        $rec = $z === null ? null : $this->find((int) $z['id'], (int) $record);
        if ($z === null || $rec === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($z, $rec): Response {
            $this->dns->saveRecord($z, (int) $rec['id'], null, true);
            return self::ok(['deleted' => (int) $rec['id']]);
        });
    }

    public function bulkRecords(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        if ($z === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $z): Response {
            $in = self::body($r);
            $n = $this->dns->bulk($z, array_map('intval', (array) ($in['ids'] ?? [])), (string) ($in['op'] ?? ''));
            return self::ok(['changed' => $n, 'op' => (string) $in['op']]);
        });
    }

    public function dnssec(Request $r, string $id): Response
    {
        $z = $this->zone($r, $id);
        if ($z === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $z): Response {
            $this->assertAdmin($r);
            return self::ok($this->dns->dnssec((string) $z['name']));
        });
    }

    public function dnssecEnable(Request $r, string $id): Response
    {
        return $this->dnssecSwitch($r, $id, true);
    }

    public function dnssecDisable(Request $r, string $id): Response
    {
        return $this->dnssecSwitch($r, $id, false);
    }

    private function dnssecSwitch(Request $r, string $id, bool $on): Response
    {
        $z = $this->zone($r, $id);
        if ($z === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $z, $on): Response {
            $this->assertAdmin($r);
            $warning = '';
            if ($on) {
                $warning = $this->dns->dnssecEnable($z);
            } else {
                $this->dns->dnssecDisable($z);
            }
            return self::ok($this->dns->dnssec((string) $z['name']) + ['warning' => $warning]);
        });
    }

    private function assertAdmin(Request $r): void
    {
        if (!Scope::isAll($this->user($r))) {
            throw new UserFacingException('Only an administrator can change or read a zone\'s DNSSEC signing.', status: 403);
        }
    }

    /** @return array<string, mixed>|null */
    private function find(int $zoneId, int $recordId): ?array
    {
        foreach ($this->dns->records($zoneId) as $rec) {
            if ((int) $rec['id'] === $recordId) {
                return $rec;
            }
        }
        return null;
    }
}
