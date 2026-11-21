<?php

declare(strict_types=1);

namespace NimVor\Dashboard;

use NimVor\Database\Connection;

/**
 * Which widgets a user sees, in what order and at what size.
 *
 * Only the user's own choices are stored. Everything else comes from the
 * registry's defaults at read time, so a widget added in a later release shows
 * up without rewriting anyone's saved layout, and a widget that was removed (or
 * one the user may no longer see) is simply left out.
 */
final class DashboardLayout
{
    public const VERSION = 1;

    public function __construct(private readonly Connection $db, private readonly WidgetRegistry $registry)
    {
    }

    /**
     * The widgets to show, in order, with their resolved settings.
     *
     * @param array<string, Widget> $allowed what the viewer may see (WidgetRegistry::visibleTo)
     * @return list<array{widget: Widget, visible: bool, size: string}>
     */
    public function resolve(int $userId, array $allowed): array
    {
        $saved = $this->saved($userId);
        $out = [];
        $position = 0;
        foreach ($allowed as $id => $w) {
            $s = $saved['widgets'][$id] ?? null;
            $out[] = [
                'widget' => $w,
                'visible' => $s !== null ? (bool) ($s['visible'] ?? $w->defaultVisible) : $w->defaultVisible,
                'size' => isset(Widget::SIZES[(string) ($s['size'] ?? '')]) ? (string) $s['size'] : $w->size,
                // Widgets the user never placed follow the placed ones, in registry order.
                'order' => isset($s['order']) ? (int) $s['order'] : 1000 + $position++,
            ];
        }
        usort($out, static fn (array $a, array $b): int => $a['order'] <=> $b['order']);
        return array_map(static fn (array $r): array => ['widget' => $r['widget'], 'visible' => $r['visible'], 'size' => $r['size']], $out);
    }

    /**
     * Save the user's choices. Unknown widgets and sizes are ignored; the order
     * is the order of $order.
     *
     * @param list<string> $order
     * @param array<string, mixed> $widgets id => ['visible' => bool, 'size' => string]
     */
    public function save(int $userId, array $order, array $widgets, array $allowed): void
    {
        $known = $this->registry->all();
        $layout = ['widgets' => []];
        $n = 0;
        foreach ($order as $id) {
            $id = (string) $id;
            if (!isset($known[$id]) || !isset($allowed[$id]) || isset($layout['widgets'][$id])) {
                continue;
            }
            $in = is_array($widgets[$id] ?? null) ? $widgets[$id] : [];
            $size = (string) ($in['size'] ?? '');
            $layout['widgets'][$id] = [
                'visible' => !empty($in['visible']),
                'size' => isset(Widget::SIZES[$size]) ? $size : $known[$id]->size,
                'order' => $n++,
            ];
        }
        // What the viewer may not see keeps what was saved before, so a role change does not erase it.
        foreach ($this->saved($userId)['widgets'] ?? [] as $id => $s) {
            if (!isset($layout['widgets'][$id]) && isset($known[$id]) && !isset($allowed[$id])) {
                $layout['widgets'][$id] = $s;
            }
        }
        $this->db->run(
            'INSERT INTO dashboard_layouts (user_id, version, layout, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(user_id) DO UPDATE SET version = excluded.version, layout = excluded.layout, updated_at = excluded.updated_at',
            [$userId, self::VERSION, (string) json_encode($layout), gmdate('c')]
        );
    }

    public function reset(int $userId): void
    {
        $this->db->run('DELETE FROM dashboard_layouts WHERE user_id = ?', [$userId]);
    }

    public function isCustomised(int $userId): bool
    {
        return $this->db->scalar('SELECT 1 FROM dashboard_layouts WHERE user_id = ?', [$userId]) !== null;
    }

    /** @return array{widgets?: array<string, array<string, mixed>>} */
    private function saved(int $userId): array
    {
        $raw = $this->db->scalar('SELECT layout FROM dashboard_layouts WHERE user_id = ?', [$userId]);
        $data = is_string($raw) ? json_decode($raw, true) : null;
        return is_array($data) && is_array($data['widgets'] ?? null) ? $data : [];
    }
}
