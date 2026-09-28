<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $series */
use WhaCP\Http\Controllers\MonitoringController;

$pct = static fn (float $v): string => number_format($v, 0) . '%';
$num = static fn (float $v): string => number_format($v, 2);
$bps = static fn (float $v): string => format_bytes((int) $v) . '/s';
$ago = static function (string $iso): string {
    $s = max(0, time() - (int) strtotime($iso));
    return $s < 3600 ? intdiv($s, 60) . ' min ago' : ($s < 86400 ? intdiv($s, 3600) . ' h ago' : intdiv($s, 86400) . ' d ago');
};
?>
<?php $this->start(); ?>Monitoring<?php $this->end('title'); ?>
<?php $this->start(); ?>Resource history and alerts<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<div class="btn-group btn-group-sm" role="group" aria-label="Range">
    <?php foreach (array_keys(MonitoringController::RANGES) as $r): ?>
        <a class="btn btn-outline-secondary<?= $r === $range ? ' active' : '' ?>" href="<?= e($this->url('monitoring', [], ['range' => $r])) ?>"<?= $r === $range ? ' aria-current="true"' : '' ?>><?= e($r) ?></a>
    <?php endforeach; ?>
</div>
<?php $this->end('actions'); ?>

<?php if ($this->serverScope()): ?>
<nav class="wcp-pagetabs" aria-label="Monitoring"><a class="active" href="<?= e($this->url('monitoring')) ?>" aria-current="page">History and alerts</a><a href="<?= e($this->url('monitoring.live')) ?>">Live</a></nav>
<?php endif; ?>
<div class="card mb-3">
    <div class="card-header"><i class="bi bi-bell me-1"></i>Active alerts</div>
    <?php if ($open === []): ?>
        <div class="card-body text-body-secondary"><i class="bi bi-check-circle text-success me-1"></i>No active alerts.</div>
    <?php else: ?>
        <ul class="list-group list-group-flush">
            <?php foreach ($open as $a): ?>
                <li class="list-group-item d-flex gap-2 align-items-center">
                    <?= $this->statusBadge($a['severity'] === 'critical' ? 'error' : 'warning', (string) $a['severity']) ?>
                    <span><?= e($a['message']) ?></span>
                    <span class="ms-auto small text-body-secondary">since <?= e($ago((string) $a['first_seen'])) ?></span>
                </li>
            <?php endforeach; ?>
        </ul>
    <?php endif; ?>
    <div class="card-footer small text-body-secondary">Checked every 5 minutes. Thresholds and the notification webhook are in <a href="<?= e($this->url('settings')) ?>">Settings</a>.</div>
</div>

<div class="row g-3">
    <div class="col-xl-6"><div class="card"><div class="card-header">CPU and memory</div><div class="card-body">
        <?= $this->partial('monitoring/_chart', ['series' => $series, 'lines' => [['cpu', 'CPU', 'is-a'], ['mem', 'Memory', 'is-b']], 'max' => 100.0, 'fmt' => $pct]) ?>
    </div></div></div>
    <div class="col-xl-6"><div class="card"><div class="card-header">Load average (1 min)</div><div class="card-body">
        <?= $this->partial('monitoring/_chart', ['series' => $series, 'lines' => [['load1', 'Load', 'is-c']], 'max' => null, 'fmt' => $num]) ?>
    </div></div></div>
    <div class="col-xl-6"><div class="card"><div class="card-header">Fullest disk</div><div class="card-body">
        <?= $this->partial('monitoring/_chart', ['series' => $series, 'lines' => [['disk', 'Disk', 'is-d']], 'max' => 100.0, 'fmt' => $pct]) ?>
    </div></div></div>
    <div class="col-xl-6"><div class="card"><div class="card-header">Network</div><div class="card-body">
        <?= $this->partial('monitoring/_chart', ['series' => $series, 'lines' => [['rx', 'In', 'is-a'], ['tx', 'Out', 'is-b']], 'max' => null, 'fmt' => $bps]) ?>
    </div></div></div>
</div>

<?php if ($recent !== []): ?>
    <div class="card mt-3">
        <div class="card-header"><i class="bi bi-clock-history me-1"></i>Resolved alerts</div>
        <div class="table-responsive"><table class="table table-sm mb-0">
            <thead><tr><th>Alert</th><th>Started</th><th>Resolved</th></tr></thead>
            <tbody><?php foreach ($recent as $a): ?><tr><td class="small"><?= e($a['message']) ?></td><td class="small text-nowrap"><?= e(substr((string) $a['first_seen'], 0, 16)) ?></td><td class="small text-nowrap"><?= e(substr((string) $a['resolved_at'], 0, 16)) ?></td></tr><?php endforeach; ?></tbody>
        </table></div>
    </div>
<?php endif; ?>
