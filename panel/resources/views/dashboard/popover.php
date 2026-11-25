<?php
/**
 * The details behind a dashboard tile.
 *
 * @var \NimVor\View\View $this
 * @var string $key cpu, mem, disk or load
 * @var array<string, mixed> $live system.live
 * @var array<string, mixed>|null $disk the disk widget's data (key disk)
 */
$cpu = (array) ($live['cpu'] ?? []);
$mem = (array) ($live['memory'] ?? []);
$load = (array) ($live['load'] ?? []);
$bar = static fn (float $p): string => '<div class="nim-pop-bar"><span class="' . ($p >= 90 ? 'is-crit' : ($p >= 75 ? 'is-warn' : '')) . '" style="width:' . max(0, min(100, $p)) . '%"></span></div>';
$procs = static function (array $rows, string $by) use ($live): string {
    $out = '<table class="table table-sm nim-pop-table mb-0"><thead><tr><th>Process</th><th>User</th><th class="text-end">' . ($by === 'cpu' ? 'CPU' : 'Memory') . '</th></tr></thead><tbody>';
    foreach (array_slice($rows, 0, 5) as $p) {
        $p = (array) $p;
        $val = $by === 'cpu' ? number_format((float) ($p['cpu_percent'] ?? 0), 1) . '%' : format_bytes((int) ($p['rss_bytes'] ?? 0));
        $out .= '<tr><td>' . e((string) ($p['name'] ?? '')) . ' <span class="text-body-secondary">' . (int) ($p['pid'] ?? 0) . '</span></td><td>' . e((string) ($p['user'] ?? '')) . '</td><td class="text-end">' . $val . '</td></tr>';
    }
    return $out . '</tbody></table>';
};
?>
<?php if ($key === 'cpu'): ?>
    <div class="nim-pop-title">CPU usage: <?= number_format((float) ($cpu['percent'] ?? 0), 1) ?>%</div>
    <div class="nim-pop-sub"><?= e((string) ($cpu['model'] ?? 'Unknown processor')) ?></div>
    <div class="small text-body-secondary mb-2"><?= (int) ($cpu['sockets'] ?? 1) ?> CPU, <?= (int) ($cpu['cores'] ?? 0) ?> physical core<?= (int) ($cpu['cores'] ?? 0) === 1 ? '' : 's' ?>, <?= (int) ($cpu['threads'] ?? 0) ?> logical<?= !empty($cpu['mhz']) ? ' · ' . number_format((float) $cpu['mhz'], 0) . ' MHz' : '' ?></div>
    <div class="nim-pop-h">Load by core</div>
    <div class="nim-pop-cores">
        <?php foreach ((array) ($cpu['per_core'] ?? []) as $c): $c = (array) $c; ?>
            <div><span class="text-body-secondary"><?= e(str_replace('cpu', 'Core ', (string) ($c['cpu'] ?? ''))) ?></span> <strong><?= number_format((float) ($c['percent'] ?? 0), 1) ?>%</strong></div>
        <?php endforeach; ?>
    </div>
    <div class="nim-pop-h">Busiest processes</div><?= $procs((array) ($live['top_cpu'] ?? []), 'cpu') ?>
<?php elseif (in_array($key, ['mem', 'memory'], true)): ?>
    <?php $total = (int) ($mem['total_bytes'] ?? 0); $used = (int) ($mem['used_bytes'] ?? 0); $p = (float) ($mem['used_percent'] ?? 0); ?>
    <div class="nim-pop-title">Memory usage: <?= number_format($p, 1) ?>%</div>
    <?= $bar($p) ?>
    <dl class="nim-pop-dl">
        <dt>Used</dt><dd><?= e(format_bytes($used)) ?></dd>
        <dt>Available</dt><dd><?= e(format_bytes((int) ($mem['available_bytes'] ?? 0))) ?></dd>
        <dt>Total</dt><dd><?= e(format_bytes($total)) ?></dd>
        <dt>Swap</dt><dd><?= (int) ($mem['swap_total_bytes'] ?? 0) > 0 ? e(format_bytes((int) ($mem['swap_used_bytes'] ?? 0))) . ' used of ' . e(format_bytes((int) $mem['swap_total_bytes'])) : 'none' ?></dd>
    </dl>
    <?php if ($this->hasRoute('server.swap') && $this->routeAllowed('server.swap')): ?><div class="mt-2"><a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('server.swap')) ?>"><i class="bi bi-layers me-1" aria-hidden="true"></i><?= (int) ($mem['swap_total_bytes'] ?? 0) > 0 ? 'Manage swap' : 'Create swap' ?></a></div><?php endif; ?>
    <div class="nim-pop-h">Biggest memory users</div><?= $procs((array) ($live['top_memory'] ?? []), 'mem') ?>
<?php elseif ($key === 'load'): ?>
    <?php $threads = max(1, (int) ($cpu['threads'] ?? 1)); ?>
    <div class="nim-pop-title">Load average</div>
    <dl class="nim-pop-dl">
        <dt>1 minute</dt><dd><?= number_format((float) ($load['load1'] ?? 0), 2) ?></dd>
        <dt>5 minutes</dt><dd><?= number_format((float) ($load['load5'] ?? 0), 2) ?></dd>
        <dt>15 minutes</dt><dd><?= number_format((float) ($load['load15'] ?? 0), 2) ?></dd>
        <dt>Logical CPUs</dt><dd><?= $threads ?></dd>
        <dt>Processes</dt><dd><?= (int) ($live['processes'] ?? 0) ?> (<?= (int) ($live['running'] ?? 0) ?> running<?= (int) ($live['zombies'] ?? 0) > 0 ? ', ' . (int) $live['zombies'] . ' zombie' : '' ?>)</dd>
    </dl>
    <div class="small text-body-secondary">A 1-minute load above <?= $threads ?> means work is waiting for a CPU.</div>
    <div class="nim-pop-h">Busiest processes</div><?= $procs((array) ($live['top_cpu'] ?? []), 'cpu') ?>
<?php else: ?>
    <?php $fs = (array) (($disk['filesystems'] ?? [])[0] ?? []); $p = (float) ($fs['used_percent'] ?? 0); ?>
    <div class="nim-pop-title">Disk space: <?= number_format($p, 0) ?>%</div>
    <?= $bar($p) ?>
    <?php if ($fs !== []): ?>
    <dl class="nim-pop-dl">
        <dt>Used</dt><dd><?= e(format_bytes((int) $fs['used_bytes'])) ?></dd>
        <dt>Free</dt><dd><?= e(format_bytes(max(0, (int) $fs['total_bytes'] - (int) $fs['used_bytes']))) ?></dd>
        <dt>Total</dt><dd><?= e(format_bytes((int) $fs['total_bytes'])) ?></dd>
        <?php if (isset($fs['inodes_percent'])): ?><dt>Inodes used</dt><dd><?= number_format((float) $fs['inodes_percent'], 1) ?>%</dd><?php endif; ?>
    </dl>
    <?php endif; ?>
    <div class="nim-pop-h">Where the space goes</div>
    <?php if (!empty($disk['areas'])): foreach ((array) $disk['areas'] as $a): ?>
        <div class="d-flex justify-content-between small"><span><?= e((string) $a['label']) ?></span><span><?= e(format_bytes((int) $a['bytes'])) ?></span></div>
    <?php endforeach; ?>
        <div class="small text-body-secondary mt-1">Scanned <?= e(time_ago((string) $disk['scanned_at'])) ?></div>
    <?php else: ?><div class="small text-body-secondary">No scan yet.</div><?php endif; ?>
    <?php if ($this->hasRoute('server.disk') && $this->can(\NimVor\Security\Permissions::SERVER_VIEW)): ?><a class="btn btn-sm btn-outline-primary mt-2" href="<?= e($this->url('server.disk')) ?>">Scan and clean up</a><?php endif; ?>
<?php endif; ?>
