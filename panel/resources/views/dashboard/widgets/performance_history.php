<?php
/**
 * Performance history: the latest reading next to a history chart. The chart is drawn by
 * assets/js/charts.js from what the monitor recorded; this page only provides the readings.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$pct = static fn (?float $v): string => $v === null ? 'N/A' : number_format($v, $v >= 10 ? 0 : 1) . '%';
$m = (array) $d['mem'];
$tabs = ['cpu' => 'CPU', 'mem' => 'Memory', 'load' => 'Load', 'net' => 'Network'];
$url = $this->url('dashboard.series');
?>
<div data-chart-scope="performance">
    <div class="nim-chart-bar">
        <div class="nim-pills" role="tablist" aria-label="Metric">
            <?php foreach ($tabs as $k => $label): ?><button type="button" class="nim-pill<?= $k === 'cpu' ? ' active' : '' ?>" role="tab" aria-selected="<?= $k === 'cpu' ? 'true' : 'false' ?>" data-chart-tab="<?= e($k) ?>"><?= e($label) ?></button><?php endforeach; ?>
        </div>
        <div class="nim-pills" role="group" aria-label="Time range">
            <?php foreach (['1h' => '1 h', '6h' => '6 h', '24h' => '24 h', '7d' => '7 d'] as $k => $label): ?><button type="button" class="nim-pill" data-range-button="<?= e($k) ?>"><?= e($label) ?></button><?php endforeach; ?>
        </div>
    </div>
    <div class="nim-perf">
        <div class="nim-perf-now">
            <div data-chart-pane="cpu">
                <div class="nim-now-value c-text-cpu"><?= e($pct($d['cpu'])) ?></div>
                <div class="nim-now-label">CPU now</div>
            </div>
            <div data-chart-pane="mem" hidden>
                <div class="nim-now-value c-text-mem"><?= e($pct($m['percent'])) ?></div>
                <div class="nim-now-label">Memory used</div>
                <div class="nim-now-sub"><?= $m['used'] === null ? 'N/A' : e(format_bytes($m['used'])) . ' used · ' . e(format_bytes((int) $m['available'])) . ' available of ' . e(format_bytes((int) $m['total'])) ?></div>
                <div class="nim-now-sub"><?= $m['swap_total'] === null ? 'No swap configured' : 'Swap ' . e(format_bytes((int) $m['swap_used'])) . ' of ' . e(format_bytes((int) $m['swap_total'])) ?></div>
            </div>
            <div data-chart-pane="load" hidden>
                <div class="nim-now-value c-text-load"><?= $d['load'] === null ? 'N/A' : e(number_format($d['load']['1'], 2)) ?></div>
                <div class="nim-now-label">Load average, 1 min</div>
                <?php if ($d['load'] !== null): ?><div class="nim-now-sub">5 min <?= e(number_format($d['load']['5'], 2)) ?> · 15 min <?= e(number_format($d['load']['15'], 2)) ?></div><?php endif; ?>
            </div>
            <div data-chart-pane="net" hidden>
                <?php if ($d['net'] === null): ?><div class="nim-now-value">N/A</div><div class="nim-now-label">Network now</div>
                <?php else: ?>
                    <div class="nim-now-value c-text-rx"><?= e(format_bytes($d['net']['rx'])) ?>/s</div><div class="nim-now-label">In now</div>
                    <div class="nim-now-sub"><span class="c-text-tx">Out <?= e(format_bytes($d['net']['tx'])) ?>/s</span></div>
                <?php endif; ?>
            </div>
            <div class="nim-now-sub mt-auto">Readings are live; the chart shows what the monitor recorded<?= $d['since'] !== null ? ' since ' . e(gmdate('Y-m-d', (int) $d['since'])) : '' ?>.</div>
        </div>
        <div class="nim-perf-chart" data-nim-chart data-metric="cpu" data-series-url="<?= e($url) ?>" data-height="168"></div>
    </div>
</div>
