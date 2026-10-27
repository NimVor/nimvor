<?php
/**
 * Server-rendered line chart (SVG; no inline styles or scripts).
 * @var list<array<string, float|int>> $series
 * @var list<array{0: string, 1: string, 2: string}> $lines key, label, css class
 * @var float|null $max fixed maximum (e.g. 100 for percentages)
 * @var callable(float): string $fmt
 */
$w = 600;
$h = 160;
$vals = [];
foreach ($lines as [$k]) {
    foreach ($series as $p) {
        $vals[] = (float) $p[$k];
    }
}
$top = $max ?? max(1.0, $vals === [] ? 1.0 : max($vals) * 1.15);
$n = count($series);
$x = static fn (int $i): float => $n <= 1 ? 0.0 : round($i * $w / ($n - 1), 1);
$y = static fn (float $v): float => round($h - min($v, $top) / $top * $h, 1);
?>
<figure class="wcp-chart mb-0">
    <?php if ($n < 2): ?>
        <div class="text-body-secondary small py-4 text-center">Not enough data yet. Samples are recorded every 5 minutes.</div>
    <?php else: ?>
        <svg viewBox="0 0 <?= $w ?> <?= $h ?>" preserveAspectRatio="none" role="img" aria-label="<?= e(implode(', ', array_column($lines, 1))) ?>">
            <?php foreach ([0.25, 0.5, 0.75] as $g): ?><line class="wcp-chart-grid" x1="0" x2="<?= $w ?>" y1="<?= $h * $g ?>" y2="<?= $h * $g ?>"/><?php endforeach; ?>
            <?php foreach ($lines as [$k, $label, $class]): ?>
                <polyline class="wcp-chart-line <?= e($class) ?>" points="<?php foreach ($series as $i => $p): ?><?= $x($i) ?>,<?= $y((float) $p[$k]) ?> <?php endforeach; ?>"/>
            <?php endforeach; ?>
        </svg>
        <figcaption class="d-flex flex-wrap gap-3 small text-body-secondary mt-1">
            <?php foreach ($lines as [$k, $label, $class]): $last = (float) end($series)[$k]; ?>
                <span><span class="wcp-chart-key <?= e($class) ?>"></span><?= e($label) ?>: <?= e($fmt($last)) ?> now</span>
            <?php endforeach; ?>
            <span class="ms-auto">scale: <?= e($fmt($top)) ?></span>
        </figcaption>
    <?php endif; ?>
</figure>
