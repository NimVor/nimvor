<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
/** @var array{installed: bool, enabled: bool, months: list<string>, updated_at: ?string}|null $status */
/** @var array<string, mixed>|null $report */
/** @var string $month */
use WhaCP\Security\Permissions as P;

$id = (int) $site['id'];
$canManage = $this->can(P::WEBSITES_MANAGE);
$monthName = static fn (string $m): string => date('F Y', (int) mktime(0, 0, 0, (int) substr($m, 4, 2), 1, (int) substr($m, 0, 4)));
$bar = static fn (int $v, int $max): int => $max > 0 ? (int) (ceil($v * 100 / $max / 5) * 5) : 0;
$list = function (string $title, string $icon, array $rows, string $unit, bool $mono = false): string {
    $max = max(array_merge([0], array_column($rows, 'value')));
    ob_start(); ?>
    <div class="card h-100">
        <div class="card-header"><i class="bi bi-<?= e($icon) ?> me-1"></i><?= e($title) ?></div>
        <?php if ($rows === []): ?>
            <div class="card-body small text-body-secondary">No data.</div>
        <?php else: ?>
            <ul class="list-group list-group-flush small">
                <?php foreach ($rows as $r): ?>
                    <li class="list-group-item">
                        <div class="d-flex justify-content-between gap-2"><span class="text-truncate<?= $mono ? ' font-monospace' : '' ?>" title="<?= e($r['label']) ?>"><?= e($r['label']) ?></span><span class="text-nowrap fw-medium"><?= e(number_format($r['value'])) ?> <span class="text-body-secondary fw-normal"><?= e($unit) ?></span></span></div>
                        <div class="progress mt-1 wcp-progress-thin" role="presentation"><div class="progress-bar wcp-w-<?= $max > 0 ? (int) (ceil($r['value'] * 100 / $max / 5) * 5) : 0 ?>"></div></div>
                    </li>
                <?php endforeach; ?>
            </ul>
        <?php endif; ?>
    </div>
    <?php return (string) ob_get_clean();
};
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'stats']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>

<?php if (!$status['installed']): ?>
    <div class="card"><div class="wcp-empty-state">
        <i class="bi bi-bar-chart-line" aria-hidden="true"></i>
        <div class="fw-medium">Statistics are not available on this server yet</div>
        <?php if ($this->can(P::PACKAGES_MANAGE) && $this->hasRoute('packages')): ?>
            <div class="small mb-3">Install AWStats under Packages, then turn statistics on here.</div>
            <a class="btn btn-primary" href="<?= e($this->url('packages')) ?>">Open Packages</a>
        <?php else: ?>
            <div class="small">Please ask your hosting provider to install AWStats.</div>
        <?php endif; ?>
    </div></div>
    <?php return; ?>
<?php endif; ?>

<?php if (!$status['enabled'] && $status['months'] === []): ?>
    <div class="card"><div class="wcp-empty-state">
        <i class="bi bi-bar-chart-line" aria-hidden="true"></i>
        <div class="fw-medium">Visitor statistics are off</div>
        <div class="small mb-3">AWStats reads this website's access log every 6 hours: visitors, pages, bandwidth, browsers, countries and more. The data stays in the website's folder.</div>
        <?php if ($canManage): ?>
            <form method="post" action="<?= e($this->url('sites.stats.enable', ['id' => $id])) ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="enabled" value="1"><button class="btn btn-primary"><i class="bi bi-toggle-on me-1"></i>Turn on statistics</button></form>
        <?php endif; ?>
    </div></div>
    <?php return; ?>
<?php endif; ?>

<div class="wcp-toolbar mb-3">
    <?php if ($status['months'] !== []): ?>
        <form method="get" class="d-flex gap-2 align-items-center">
            <label class="small text-body-secondary" for="st-month">Month</label>
            <select class="form-select form-select-sm w-auto" id="st-month" name="month" data-wcp-autosubmit>
                <?php foreach ($status['months'] as $m): ?><option value="<?= e($m) ?>"<?= $m === $month ? ' selected' : '' ?>><?= e($monthName($m)) ?></option><?php endforeach; ?>
            </select>
        </form>
    <?php endif; ?>
    <span class="small text-body-secondary me-auto"><?= $status['enabled'] ? 'Updated every 6 hours' : 'Statistics are off; showing the data collected before' ?><?= $status['updated_at'] !== null ? ' · last update ' . e(time_ago($status['updated_at'])) : '' ?></span>
    <?php if ($canManage && $status['enabled']): ?>
        <form method="post" action="<?= e($this->url('sites.stats.update', ['id' => $id])) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-primary"><i class="bi bi-arrow-clockwise me-1"></i>Update now</button></form>
        <form method="post" action="<?= e($this->url('sites.stats.enable', ['id' => $id])) ?>" data-wcp-confirm="Turn statistics off? The data collected so far is kept."><?= $this->csrfField() ?><input type="hidden" name="enabled" value="0"><button class="btn btn-sm btn-outline-secondary">Turn off</button></form>
    <?php elseif ($canManage): ?>
        <form method="post" action="<?= e($this->url('sites.stats.enable', ['id' => $id])) ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="enabled" value="1"><button class="btn btn-sm btn-primary">Turn on again</button></form>
    <?php endif; ?>
</div>

<?php if ($report === null): ?>
    <div class="card"><div class="wcp-empty-state"><i class="bi bi-hourglass-split" aria-hidden="true"></i><div class="fw-medium">No data yet</div><div class="small">The first report appears after the next update, once the website has had visitors.</div></div></div>
    <?php return; ?>
<?php endif; ?>

<section class="wcp-site-summary mb-3" aria-label="<?= e($monthName($month)) ?> totals" data-wcp-stats-summary>
    <?php foreach (['Unique visitors' => number_format($report['unique']), 'Visits' => number_format($report['visits']), 'Pages' => number_format($report['pages']), 'Hits' => number_format($report['hits']), 'Bandwidth' => format_bytes($report['bytes'])] as $label => $value): ?>
        <div class="wcp-sum"><div class="wcp-sum-label"><?= e($label) ?></div><div class="wcp-sum-value"><?= e($value) ?></div></div>
    <?php endforeach; ?>
</section>

<div class="row g-3 mb-3 align-items-start">
    <div class="col-xl-7">
        <div class="card">
            <div class="card-header"><i class="bi bi-calendar3 me-1"></i>Days of <?= e($monthName($month)) ?></div>
            <?php $maxV = max(array_merge([0], array_column($report['days'], 'visits'))); ?>
            <div class="table-responsive">
                <table class="table table-sm align-middle mb-0 wcp-table" id="stats-days">
                    <thead><tr><th>Day</th><th class="text-end">Visits</th><th class="d-none d-sm-table-cell"></th><th class="text-end">Pages</th><th class="text-end">Hits</th><th class="text-end">Bandwidth</th></tr></thead>
                    <tbody>
                    <?php foreach ($report['days'] as $day => $d): ?>
                        <tr>
                            <td class="text-nowrap"><?= e(date('D j', (int) mktime(0, 0, 0, (int) substr($day, 4, 2), (int) substr($day, 6, 2), (int) substr($day, 0, 4)))) ?></td>
                            <td class="text-end"><?= e(number_format($d['visits'])) ?></td>
                            <td class="d-none d-sm-table-cell wcp-stat-bar"><div class="progress wcp-progress-thin" role="presentation"><div class="progress-bar wcp-w-<?= $bar($d['visits'], $maxV) ?>"></div></div></td>
                            <td class="text-end"><?= e(number_format($d['pages'])) ?></td>
                            <td class="text-end"><?= e(number_format($d['hits'])) ?></td>
                            <td class="text-end text-nowrap"><?= e(format_bytes($d['bytes'])) ?></td>
                        </tr>
                    <?php endforeach; ?>
                    <?php if ($report['days'] === []): ?><tr><td colspan="6" class="text-body-secondary small">No days recorded.</td></tr><?php endif; ?>
                    </tbody>
                </table>
            </div>
        </div>
    </div>
    <div class="col-xl-5">
        <div class="card">
            <div class="card-header"><i class="bi bi-clock me-1"></i>Hours (server time)</div>
            <?php $maxH = max(array_merge([0], array_column($report['hours'], 'pages'))); ?>
            <div class="card-body small">
                <?php foreach ($report['hours'] as $h => $d): ?>
                    <div class="d-flex align-items-center gap-2 mb-1"><span class="font-monospace text-body-secondary wcp-hour"><?= sprintf('%02d', $h) ?></span>
                        <div class="progress flex-grow-1 wcp-progress-thin" role="presentation"><div class="progress-bar wcp-w-<?= $bar($d['pages'], $maxH) ?>"></div></div>
                        <span class="text-end wcp-hour-count"><?= e(number_format($d['pages'])) ?></span></div>
                <?php endforeach; ?>
            </div>
        </div>
    </div>
</div>

<div class="row g-3">
    <div class="col-lg-6"><?= $list('Most viewed pages', 'file-earmark-text', $report['pages_top'], 'views', true) ?></div>
    <div class="col-lg-6"><?= $list('Referring pages', 'link-45deg', $report['referrers'], 'visits', true) ?></div>
    <div class="col-md-6 col-xl-4"><?= $list('Search engines', 'search', $report['search'], 'visits') ?></div>
    <div class="col-md-6 col-xl-4"><?= $list('Browsers', 'browser-chrome', $report['browsers'], 'hits') ?></div>
    <div class="col-md-6 col-xl-4"><?= $list('Operating systems', 'laptop', $report['systems'], 'hits') ?></div>
    <div class="col-md-6 col-xl-6"><?= $list('Countries', 'globe', $report['countries'], 'hits') ?></div>
    <div class="col-md-6 col-xl-6"><?= $list('HTTP errors', 'exclamation-triangle', $report['errors'], 'hits') ?></div>
</div>
