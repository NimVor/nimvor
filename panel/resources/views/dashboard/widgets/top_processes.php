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
<div class="row g-3">
    <?php foreach ($cols as $key => $label): ?>
        <div class="col-12">
            <div class="small text-body-secondary mb-1"><?= e($label) ?></div>
            <div class="table-responsive">
                <table class="table table-sm align-middle mb-0 nim-top-table">
                    <thead><tr><th>Process</th><th class="text-end">PID</th><th>User</th><th class="text-end">CPU</th><th class="text-end">Memory</th><th>State</th></tr></thead>
                    <tbody>
                    <?php foreach ((array) ($d[$key] ?? []) as $p): ?>
                        <tr>
                            <td class="text-truncate nim-w-9"><?= e((string) $p['name']) ?></td>
                            <td class="text-end font-monospace text-nowrap"><?= (int) $p['pid'] ?></td>
                            <td class="text-truncate nim-w-6"><?= e((string) $p['user']) ?></td>
                            <td class="text-end text-nowrap"><?= e($pct($p['cpu'])) ?></td>
                            <td class="text-end text-nowrap"><?= $p['rss'] === null ? 'N/A' : e(format_bytes((int) $p['rss'])) ?><?= $p['mem'] !== null ? ' <span class="text-body-secondary">(' . e(number_format((float) $p['mem'], 1)) . '%)</span>' : '' ?></td>
                            <td class="text-nowrap"><?= e((string) $p['state']) ?></td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table>
            </div>
        </div>
    <?php endforeach; ?>
</div>
<?php if ($d['processes'] !== null): ?><div class="small text-body-secondary mt-2"><?= (int) $d['processes'] ?> processes running.</div><?php endif; ?>
