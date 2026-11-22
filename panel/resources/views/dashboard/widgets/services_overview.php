<?php
/**
 * Services: a compact distribution of the enabled services and, below it, how many ran over time.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$t = max(1, (int) $d['total']);
?>
<div class="nim-figs">
    <div class="nim-fig"><div class="nim-fig-value c-text-ok"><?= (int) $d['up'] ?></div><div class="nim-fig-label">Running</div></div>
    <div class="nim-fig"><div class="nim-fig-value<?= (int) $d['stopped'] > 0 ? ' text-warning' : '' ?>"><?= (int) $d['stopped'] ?></div><div class="nim-fig-label">Stopped</div></div>
    <div class="nim-fig"><div class="nim-fig-value<?= (int) $d['failed'] > 0 ? ' text-danger' : '' ?>"><?= (int) $d['failed'] ?></div><div class="nim-fig-label">Failed</div></div>
</div>
<div class="nim-stack" role="img" aria-label="<?= (int) $d['up'] ?> running, <?= (int) $d['stopped'] ?> stopped, <?= (int) $d['failed'] ?> failed">
    <?php foreach ([['up', 'c-ok', 'running'], ['stopped', 'c-warn', 'stopped'], ['failed', 'c-bad', 'failed']] as [$k, $cls, $label]): if ((int) $d[$k] > 0): ?>
        <span class="nim-stack-seg <?= e($cls) ?>" data-grow="<?= (int) $d[$k] ?>" title="<?= (int) $d[$k] ?> <?= e($label) ?>"></span>
    <?php endif; endforeach; ?>
</div>
<?php if ($d['problems'] !== []): ?><div class="text-warning mt-2">Not running: <span class="font-monospace"><?= e(implode(', ', (array) $d['problems'])) ?></span></div><?php endif; ?>
<div class="nim-sub mt-3">Services running over time</div>
<div data-nim-chart data-metric="svc" data-series-url="<?= e($this->url('dashboard.series')) ?>" data-height="96"></div>
