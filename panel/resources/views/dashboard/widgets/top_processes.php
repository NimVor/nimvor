<?php
/**
 * Top processes: the five busiest by CPU and by memory. Stopping or killing a
 * process (with its confirmation) stays on the Processes page.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$cols = ['cpu' => 'By CPU', 'mem' => 'By memory'];
$pct = static fn (?float $v): string => $v === null ? 'N/A' : number_format($v, 1) . '%';
?>
<div data-nim-tabs="top-processes">
    <?= $this->partial('dashboard/tabs', ['tabs' => $cols, 'label' => 'Busiest processes by']) ?>
    <?php $first = array_key_first($cols); foreach ($cols as $key => $label): ?>
        <div data-nim-pane="<?= e($key) ?>"<?= $key === $first ? '' : ' hidden' ?>>
            <table class="table table-sm align-middle mb-0 nim-top-table">
                <thead><tr><th>Process</th><th class="text-end">PID</th><th class="text-end"><?= $key === 'cpu' ? 'CPU' : 'Memory' ?></th></tr></thead>
                <tbody>
                <?php foreach ((array) ($d[$key] ?? []) as $p): ?>
                    <tr title="<?= e((string) $p['user'] . ' · ' . (string) $p['state']) ?>">
                        <td class="text-truncate nim-w-9"><?= e((string) $p['name']) ?></td>
                        <td class="text-end font-monospace"><?= (int) $p['pid'] ?></td>
                        <?php if ($key === 'cpu'): ?>
                            <td class="text-end"><?= e($pct($p['cpu'])) ?></td>
                        <?php else: ?>
                            <td class="text-end"<?= $p['mem'] !== null ? ' title="' . e(number_format((float) $p['mem'], 1)) . '% of memory"' : '' ?>><?= $p['rss'] === null ? 'N/A' : e(format_bytes((int) $p['rss'])) ?></td>
                        <?php endif; ?>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endforeach; ?>
</div>
<?php if ($d['processes'] !== null): ?><div class="nim-widget-foot"><?= (int) $d['processes'] ?> processes running.</div><?php endif; ?>
