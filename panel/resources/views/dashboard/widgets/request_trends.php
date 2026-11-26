<?php
/**
 * Request trends: requests to all websites per hour over the last 24 hours against the 24 hours before, the share of
 * errors and the busiest websites, counted from the access logs. Drawn on the server.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$hours = (array) $d['hours'];
$max = max(1, max($hours));
$change = $d['change'];
$fmt = static fn (int $n): string => number_format($n, 0, '.', ' ');
?>
<div data-nim-tabs="request-trends">
    <?= $this->partial('dashboard/tabs', ['tabs' => ['counts' => 'Counts and chart', 'busiest' => 'Busiest websites'], 'label' => 'Request trends']) ?>
    <div data-nim-pane="counts">
<div class="row g-2 text-center mb-3">
    <div class="col-4"><div class="nim-kpi"><?= e($fmt((int) $d['total'])) ?></div><div class="nim-kpi-label">Requests, 24 h</div></div>
    <div class="col-4">
        <?php if ($change === null): ?>
            <div class="nim-kpi">N/A</div><div class="nim-kpi-label">vs the day before</div>
        <?php else: ?>
            <div class="nim-kpi"><?= $change > 0 ? '+' : '' ?><?= e((string) $change) ?>%</div><div class="nim-kpi-label">vs the day before (<?= e($fmt((int) $d['previous'])) ?>)</div>
        <?php endif; ?>
    </div>
    <div class="col-4">
        <?php if ($d['error_share'] === null): ?>
            <div class="nim-kpi">N/A</div><div class="nim-kpi-label">errors (4xx, 5xx)</div>
        <?php else: ?>
            <div class="nim-kpi"><?= e((string) $d['error_share']) ?>%</div><div class="nim-kpi-label">errors (4xx, 5xx)</div>
        <?php endif; ?>
    </div>
</div>
<div class="nim-sub">Requests per hour, last 24 hours (UTC)</div>
<div class="nim-bars" role="img" aria-label="Requests per hour over the last 24 hours">
    <?php $i = 0; foreach ($hours as $start => $n): ?>
        <div class="nim-bar" title="<?= e(gmdate('M j H:00', (int) $start) . ': ' . $fmt((int) $n) . ' requests') ?>">
            <span class="nim-bar-track"><span class="nim-bar-fill" data-bar-height="<?= e((string) round(100 * $n / $max)) ?>"></span></span>
            <span class="nim-bar-l"><?= $i % 6 === 0 ? e(gmdate('H', (int) $start)) : '&nbsp;' ?></span>
        </div>
    <?php $i++; endforeach; ?>
</div>
    </div>
    <div data-nim-pane="busiest" hidden>
        <?php if ($d['top'] === []): ?><div class="text-body-secondary">No website has requests to list yet.</div><?php endif; ?>
<?php if ($d['top'] !== []): ?>
    <div class="nim-sub">Busiest websites</div>
    <table class="table table-sm mb-1"><tbody>
        <?php foreach ((array) $d['top'] as $domain => $n): ?>
            <tr><td class="text-truncate nim-w-9"><?= e((string) $domain) ?></td><td class="text-end text-nowrap"><?= e($fmt((int) $n)) ?></td></tr>
        <?php endforeach; ?>
    </tbody></table>
<?php endif; ?>
    </div>
</div>
<div class="text-body-secondary small mt-2">
    Counted from <?= (int) $d['read'] ?> website access log<?= (int) $d['read'] === 1 ? '' : 's' ?><?= (int) $d['skipped'] > 0 ? ' (' . (int) $d['skipped'] . ' without a readable log left out)' : '' ?>.
    The error share covers the agent's last 48 hours. <?= (int) $d['server_errors'] > 0 ? e($fmt((int) $d['server_errors'])) . ' server errors (5xx) in that time.' : '' ?>
</div>
