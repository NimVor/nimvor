<?php
/**
 * Websites by state (donut), HTTPS use and websites created per month (bars). All drawn on the server.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$segments = [
    ['label' => 'Active', 'value' => $d['active'], 'cls' => 'c-ok'],
    ['label' => 'Disabled', 'value' => $d['disabled'], 'cls' => 'c-muted'],
    ['label' => 'Suspended', 'value' => $d['suspended'], 'cls' => 'c-warn'],
];
$max = max(1, max((array) $d['created']));
$https = $d['total'] > 0 ? (int) round(100 * $d['https'] / $d['total']) : 0;
?>
<div class="nim-split">
    <?= $this->partial('dashboard/charts/donut', ['segments' => $segments, 'centerValue' => (string) $d['total'], 'centerLabel' => 'Websites', 'size' => 96]) ?>
    <ul class="nim-legend-list">
        <?php foreach ($segments as $s): ?><li><span class="nim-chart-key <?= e($s['cls']) ?>"></span><span class="me-auto"><?= e($s['label']) ?></span><strong><?= (int) $s['value'] ?></strong></li><?php endforeach; ?>
        <li><span class="nim-chart-key c-info"></span><span class="me-auto">Using HTTPS</span><strong><?= (int) $d['https'] ?> <span class="text-body-secondary fw-normal">(<?= $https ?>%)</span></strong></li>
    </ul>
</div>
<div class="nim-sub mt-3">Created per month</div>
<div class="nim-bars" role="img" aria-label="Websites created per month">
    <?php foreach ((array) $d['created'] as $month => $n): ?>
        <div class="nim-bar" title="<?= e($month . ': ' . (int) $n . ' website' . ((int) $n === 1 ? '' : 's') . ' created') ?>">
            <span class="nim-bar-n"><?= (int) $n ?></span>
            <span class="nim-bar-track"><span class="nim-bar-fill" data-bar-height="<?= e((string) round(100 * $n / $max)) ?>"></span></span>
            <span class="nim-bar-l"><?= e(gmdate('M', (int) strtotime($month . '-01'))) ?></span>
        </div>
    <?php endforeach; ?>
</div>
<div class="nim-widget-foot">Counts websites that still exist. Request trends are not collected, so none are shown.</div>
