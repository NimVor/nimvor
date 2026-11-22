<?php
/**
 * Disk: the filesystems and the biggest places from the latest scan.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$disk = $d;
$bar = static fn (float $p): string => $p >= 90 ? 'bg-danger' : ($p >= 75 ? 'bg-warning' : '');
?>
    <div class="nim-donuts">
        <?php foreach ($disk['filesystems'] as $f): $p = (float) $f['used_percent']; $cls = $p >= 90 ? 'c-bad' : ($p >= 75 ? 'c-warn' : 'c-ok'); ?>
            <div class="nim-donut-item">
                <?= $this->partial('dashboard/charts/donut', [
                    'segments' => [['label' => 'Used', 'value' => (int) $f['used_bytes'], 'cls' => $cls], ['label' => 'Free', 'value' => max(0, (int) $f['total_bytes'] - (int) $f['used_bytes']), 'cls' => 'c-track']],
                    'centerValue' => number_format($p, 0) . '%', 'centerLabel' => 'Used on ' . (string) $f['mount'], 'size' => 78,
                ]) ?>
                <div class="nim-donut-cap"><span class="font-monospace"><?= e((string) $f['mount']) ?></span><span class="text-body-secondary"><?= e(format_bytes($f['used_bytes'])) ?> / <?= e(format_bytes($f['total_bytes'])) ?></span></div>
            </div>
        <?php endforeach; ?>
    </div>
    <?php if ($disk['filesystems'] === []): ?><div class="text-body-secondary">No filesystem data.</div><?php endif; ?>
    <?php if ($disk['areas'] !== []): ?>
        <div class="text-body-secondary mt-2 mb-1">Biggest places (scanned <?= e(time_ago((string) $disk['scanned_at'])) ?>)</div>
        <?php foreach ($disk['areas'] as $a): ?><div class="d-flex justify-content-between"><span><?= e((string) $a['label']) ?></span><span><?= e(format_bytes($a['bytes'])) ?></span></div><?php endforeach; ?>
    <?php else: ?>
        <div class="text-body-secondary mt-2">Scan the disk on the Disk usage page to see where the space goes.</div>
    <?php endif; ?>
    <div class="nim-sub mt-3">Fullest disk, last 7 days</div>
    <div data-nim-chart data-metric="disk" data-fixed-range="7d" data-series-url="<?= e($this->url('dashboard.series')) ?>" data-height="84"></div>
