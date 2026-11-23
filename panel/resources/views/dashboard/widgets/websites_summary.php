<?php
/**
 * The Websites card: counts and PHP versions from the last discovery.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$summary = (array) ($d['summary'] ?? []);
?>

    <div class="row g-2 text-center mb-3">
        <div class="col-4"><div class="nim-kpi"><?= (int) $summary['total'] ?></div><div class="nim-kpi-label">Total</div></div>
        <div class="col-4"><div class="nim-kpi"><?= (int) $summary['enabled'] ?></div><div class="nim-kpi-label">Enabled</div></div>
        <div class="col-4"><div class="nim-kpi"><?= (int) $summary['https'] ?></div><div class="nim-kpi-label">HTTPS</div></div>
    </div>
    <?php if ($summary['expiring'] > 0): ?>
        <div class="alert alert-warning py-2 small"><i class="bi bi-exclamation-triangle me-1"></i><?= (int) $summary['expiring'] ?> certificate(s) expiring soon or expired.</div>
    <?php endif; ?>
    <div class="small text-body-secondary mb-1">PHP versions in use</div>
    <?php if ($summary['php_versions'] === []): ?>
        <div class="small">No PHP websites detected.</div>
    <?php else: ?>
        <?php foreach ($summary['php_versions'] as $v => $n): ?>
            <span class="badge text-bg-light border me-1">PHP <?= e($v) ?> · <?= (int) $n ?></span>
        <?php endforeach; ?>
    <?php endif; ?>
    <div class="small text-body-secondary mt-3">Discovered <?= e(time_ago($summary['discovered_at'])) ?></div>
