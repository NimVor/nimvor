<?php
/**
 * A live metric tile of the dashboard (CPU, memory, disk, load). Its numbers and sparkline are filled by the live poll
 * (public/assets/js/dashboard.js), which finds it by data-metric.
 *
 * @var \NimVor\View\View $this
 * @var string $key
 * @var string $label
 * @var string $icon
 * @var string $series
 */
?>
<div class="card nim-metric h-100" data-metric="<?= e($key) ?>">
    <div class="card-body">
        <div class="d-flex justify-content-between align-items-start">
            <div>
                <div class="nim-metric-label"><i class="bi bi-<?= e($icon) ?> me-1"></i><?= e($label) ?></div>
                <div class="nim-metric-value" data-value>—</div>
                <div class="nim-metric-detail" data-detail>&nbsp;</div>
            </div>
        </div>
        <div class="nim-spark" data-spark="<?= e($series) ?>" aria-hidden="true"></div>
    </div>
</div>
