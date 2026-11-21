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
    <?php foreach ($disk['filesystems'] as $f): $p = (float) $f['used_percent']; ?>
        <div class="d-flex justify-content-between"><span class="font-monospace"><?= e((string) $f['mount']) ?></span><span><?= e(format_bytes($f['used_bytes'])) ?> / <?= e(format_bytes($f['total_bytes'])) ?></span></div>
        <div class="progress mb-2" role="progressbar" aria-valuenow="<?= e((string) $p) ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar <?= $bar($p) ?>" data-width="<?= e((string) $p) ?>"></div></div>
    <?php endforeach; ?>
    <?php if ($disk['filesystems'] === []): ?><div class="text-body-secondary">No filesystem data.</div><?php endif; ?>
    <?php if ($disk['areas'] !== []): ?>
        <div class="text-body-secondary mt-2 mb-1">Biggest places (scanned <?= e(time_ago((string) $disk['scanned_at'])) ?>)</div>
        <?php foreach ($disk['areas'] as $a): ?><div class="d-flex justify-content-between"><span><?= e((string) $a['label']) ?></span><span><?= e(format_bytes($a['bytes'])) ?></span></div><?php endforeach; ?>
    <?php else: ?>
        <div class="text-body-secondary mt-2">Scan the disk on the Disk usage page to see where the space goes.</div>
    <?php endif; ?>
