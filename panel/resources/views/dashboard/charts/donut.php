<?php
/**
 * A donut drawn on the server (no script): segments are share-of-total arcs with a
 * tooltip each; the middle shows one value. Colours come from the .c-* classes in nimvor.css.
 *
 * @var \NimVor\View\View $this
 * @var list<array{label: string, value: float|int, cls: string}> $segments
 * @var string $centerValue
 * @var string $centerLabel
 * @var int $size
 */
$size = (int) ($size ?? 92);
$sum = 0.0;
foreach ($segments as $s) {
    $sum += max(0.0, (float) $s['value']);
}
$offset = 0.0;
?>
<svg class="nim-donut" viewBox="0 0 36 36" width="<?= $size ?>" height="<?= $size ?>" role="img" aria-label="<?= e($centerLabel . ': ' . $centerValue) ?>">
    <circle class="nim-donut-track" cx="18" cy="18" r="15.9155" fill="none" stroke-width="3.6"></circle>
    <?php if ($sum > 0): foreach ($segments as $s): $v = max(0.0, (float) $s['value']); if ($v <= 0) { continue; } $pct = 100 * $v / $sum; ?>
        <circle class="nim-donut-seg <?= e($s['cls']) ?>" cx="18" cy="18" r="15.9155" fill="none" stroke-width="3.6" stroke-dasharray="<?= e(number_format($pct, 3, '.', '')) ?> <?= e(number_format(100 - $pct, 3, '.', '')) ?>" stroke-dashoffset="<?= e(number_format(-$offset, 3, '.', '')) ?>" transform="rotate(-90 18 18)"><title><?= e($s['label'] . ': ' . (int) $v . ' (' . number_format($pct, 0) . '%)') ?></title></circle>
        <?php $offset += $pct; endforeach; endif; ?>
    <text x="18" y="18.4" class="nim-donut-value" text-anchor="middle" dominant-baseline="middle"><?= e($centerValue) ?></text>
</svg>
