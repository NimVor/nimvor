<?php

declare(strict_types=1);

namespace NimVor\Dashboard;

use NimVor\Database\Connection;

/**
 * What a widget measured, kept for a few seconds or minutes. A refresh inside
 * the window returns the kept answer, so many open dashboards (or one that
 * refreshes often) cost the server one measurement, not one each.
 */
final class WidgetCache
{
    public function __construct(private readonly Connection $db)
    {
    }

    /**
     * @param callable():array<string, mixed> $measure
     * @return array{data: array<string, mixed>, at: int}
     */
    public function remember(string $widget, string $scope, int $ttl, callable $measure, ?int $now = null): array
    {
        $now ??= time();
        if ($ttl > 0) {
            $row = $this->db->fetch('SELECT payload, created_at FROM widget_cache WHERE widget = ? AND scope = ?', [$widget, $scope]);
            if ($row !== null && (int) $row['created_at'] + $ttl > $now) {
                $data = json_decode((string) $row['payload'], true);
                if (is_array($data)) {
                    return ['data' => $data, 'at' => (int) $row['created_at']];
                }
            }
        }
        // An exception is not cached: the next refresh measures again.
        $data = $measure();
        if ($ttl > 0) {
            $this->db->run(
                'INSERT INTO widget_cache (widget, scope, payload, created_at) VALUES (?, ?, ?, ?) ON CONFLICT(widget, scope) DO UPDATE SET payload = excluded.payload, created_at = excluded.created_at',
                [$widget, $scope, (string) json_encode($data), $now]
            );
        }
        return ['data' => $data, 'at' => $now];
    }

    public function forget(string $widget): void
    {
        $this->db->run('DELETE FROM widget_cache WHERE widget = ?', [$widget]);
    }
}
