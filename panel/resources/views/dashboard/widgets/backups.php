<?php
/**
 * Backups: completed, failed and running, and when the last one finished.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$backups = $d;
$needs = ['counts' => ['backup' => (int) ($d['attention'] ?? 0)]];
?>
    <div class="nim-figs">
        <div class="nim-fig"><div class="nim-fig-value"><?= (int) $backups['completed'] ?></div><div class="nim-fig-label">Completed</div></div>
        <div class="nim-fig"><div class="nim-fig-value<?= $backups['failed'] > 0 ? ' text-danger' : '' ?>"><?= (int) $backups['failed'] ?></div><div class="nim-fig-label">Failed (7 days)</div></div>
        <div class="nim-fig"><div class="nim-fig-value"><?= (int) $backups['running'] ?></div><div class="nim-fig-label">Running</div></div>
    </div>
    <div class="text-body-secondary">Last completed: <?= $backups['last'] !== null ? e(time_ago($backups['last'])) : 'never' ?><?= $backups['schedules'] === 0 ? ' · no schedule set' : '' ?></div>
    <?php if ($needs !== null && ($needs['counts']['backup'] ?? 0) > 0): ?><div class="mt-1"><a href="<?= e($this->url('sites.healthcheck')) ?>?category=backup"><?= (int) $needs['counts']['backup'] ?> website(s) without a recent backup</a></div><?php endif; ?>
