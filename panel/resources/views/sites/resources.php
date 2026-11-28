<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var string $range
 * @var array<string, mixed> $current site, account, sites_in_account
 * @var list<array<string, mixed>> $events
 * @var list<array<string, mixed>> $accountPoints
 * @var mixed $first
 * @var array<string, mixed>|null $fpm
 * @var bool $admin
 */
$id = (int) $site['id'];
$ranges = ['1h' => '1 hour', '24h' => '24 hours', '7d' => '7 days', '30d' => '30 days'];
$s = $current['site'];
$a = $current['account'];
$series = $this->url('sites.resources.series', ['id' => $id]);
$charts = [
    ['site_cpu', 'CPU', 'CPU time used by the website\'s PHP workers, as a share of one core (100% = one full core).'],
    ['site_mem', 'Memory', 'Memory of the website\'s PHP workers (proportional set size: shared pages are split fairly between processes).'],
    ['site_io', 'Disk I/O', 'Bytes the website\'s PHP workers read from and wrote to disk (what reached the disk, not cached reads).'],
    ['site_workers', 'PHP workers', 'How many PHP-FPM workers the website\'s pool had at each sample.'],
    ['site_fpm', 'Concurrent requests', 'Busy PHP workers (each serves one request at a time) and the requests waiting for a free worker, from the pool\'s own status page.'],
    ['site_req', 'Requests', 'Requests per minute, counted from the website\'s own access log.'],
    ['site_traffic', 'Traffic', 'Response bytes sent to visitors per second (the access log\'s size field).'],
    ['site_err', 'Errors', 'Responses with status 5xx and 4xx per minute.'],
];
$bytes = static fn (mixed $b): string => $b === null ? '—' : format_bytes((int) $b, 1);
$since = ($first === null || $first === false) ? null : (int) $first;
$kinds = [
    'oom_kill' => 'Processes killed for lack of memory', 'memory_limit' => 'Memory limit reached', 'cpu_throttled' => 'CPU throttled',
    'memory_pressure' => 'Memory pressure', 'cpu_near_limit' => 'CPU close to its limit', 'memory_near_limit' => 'Memory close to its limit', 'processes_near_limit' => 'Processes close to their limit',
];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'resources']) ?>

<p class="small text-body-secondary">What this website uses, recorded with every monitoring pass. CPU, memory and disk are those of the website's own PHP-FPM workers (sampled: a worker that starts and ends between two samples is not seen); requests, traffic and errors come from its own access log. A gap in a chart means nothing was measured then, not zero.</p>

<div class="d-flex flex-wrap gap-2 align-items-center mb-3">
    <div class="btn-group btn-group-sm" role="group" aria-label="Period">
        <?php foreach ($ranges as $k => $l): ?><a class="btn btn-outline-secondary<?= $range === $k ? ' active' : '' ?>" href="<?= e($this->url('sites.resources', ['id' => $id], ['range' => $k])) ?>"><?= e($l) ?></a><?php endforeach; ?>
    </div>
    <?php if ($since !== null): ?><span class="small nim-muted">Recording since <?= e(gmdate('Y-m-d H:i', $since)) ?> UTC · latest sample <?= e(gmdate('H:i', (int) $s['taken_at'])) ?> UTC</span><?php endif; ?>
</div>

<?php if ($s === null): ?>
<div class="card mb-3"><div class="card-body">Nothing has been recorded for this website yet. Samples are taken with every monitoring pass, once the website has PHP workers or an access log; the first sample only starts the counters, rates appear from the second.</div></div>
<?php else: ?>
<div class="row g-3 mb-3">
    <div class="col-6 col-lg-3"><div class="card h-100"><div class="card-body"><div class="small nim-muted">PHP workers now</div><div class="fs-4"><?= $s['workers'] === null ? '—' : (int) $s['workers'] ?></div></div></div></div>
    <div class="col-6 col-lg-3"><div class="card h-100"><div class="card-body"><div class="small nim-muted">PHP memory now</div><div class="fs-4"><?= $bytes($s['mem_bytes']) ?></div></div></div></div>
    <div class="col-6 col-lg-3"><div class="card h-100"><div class="card-body"><div class="small nim-muted">CPU (last sample)</div><div class="fs-4"><?= $s['cpu_s'] === null || (float) $s['interval_s'] <= 0 ? '—' : e((string) round((float) $s['cpu_s'] / (float) $s['interval_s'] * 100, 1)) . '%' ?></div></div></div></div>
    <div class="col-6 col-lg-3"><div class="card h-100"><div class="card-body"><div class="small nim-muted">Requests per minute (last sample)</div><div class="fs-4"><?= $s['req'] === null || (float) $s['interval_s'] <= 0 ? '—' : e((string) round((int) $s['req'] / (float) $s['interval_s'] * 60, 1)) ?></div></div></div></div>
</div>
<?php endif; ?>

<div class="row g-3 mb-3" data-chart-scope>
    <?php foreach ($charts as [$metric, $title, $help]): ?>
    <div class="col-lg-6"><div class="card h-100"><div class="card-body">
        <div class="fw-semibold"><?= e($title) ?><?= $this->hint($help, $title) ?></div>
        <div data-nim-chart data-metric="<?= e($metric) ?>" data-series-url="<?= e($series) ?>" data-fixed-range="<?= e($range) ?>" data-height="150"></div>
    </div></div></div>
    <?php endforeach; ?>
</div>

<div class="card mb-3">
    <div class="card-header"><i class="bi bi-speedometer me-1"></i>The account's limits<?= $this->hint('Resource limits are set per hosting account (its plan) and enforced by the kernel with cgroup v2. They apply to everything the account runs, so websites of one account share them.', 'Account limits') ?></div>
    <div class="card-body">
        <?php if ($a === null): ?>
            <p class="mb-0 text-body-secondary">This website's account has no resource limits (no cgroup slice), so there are no limit counters to show. Memory and CPU above are still those of its PHP workers.</p>
        <?php else: $memLimit = (int) ($a['mem_limit_mb'] ?? 0); $cpuLimit = (int) ($a['cpu_limit_pct'] ?? 0); $thr = array_sum(array_map(static fn (array $p): int => (int) ($p['throttled'] ?? 0), $accountPoints)); ?>
            <?php if ((int) $current['sites_in_account'] > 1): ?><p class="small text-body-secondary">The account's slice is shared by <?= (int) $current['sites_in_account'] ?> websites: its numbers cover all of them, so they can be higher than this website's own.</p><?php endif; ?>
            <dl class="row mb-0 small">
                <dt class="col-sm-4">Memory now</dt><dd class="col-sm-8"><?= $bytes($a['mem_bytes']) ?><?= $memLimit > 0 ? ' of ' . $memLimit . ' MB (' . round((int) $a['mem_bytes'] / ($memLimit * 1048576) * 100) . '%)' : ' (no limit)' ?></dd>
                <dt class="col-sm-4">Memory peak</dt><dd class="col-sm-8"><?= $a['mem_peak'] === null ? 'not reported by this kernel' : $bytes($a['mem_peak']) . ' since the slice was created' ?></dd>
                <dt class="col-sm-4">CPU limit</dt><dd class="col-sm-8"><?= $cpuLimit > 0 ? $cpuLimit . '% (100% = one core)' : 'none' ?></dd>
                <dt class="col-sm-4">Processes</dt><dd class="col-sm-8"><?= (int) $a['tasks'] ?><?= (int) ($a['tasks_limit'] ?? 0) > 0 ? ' of ' . (int) $a['tasks_limit'] : ' (no limit)' ?></dd>
                <dt class="col-sm-4">CPU throttled in this period</dt><dd class="col-sm-8"><?= $thr ?> period<?= $thr === 1 ? '' : 's' ?></dd>
            </dl>
            <p class="small text-body-secondary mt-2 mb-0">The limits come from the hosting plan. Nothing is changed by looking here, and NimVor never lowers a limit by itself. The website's PHP process limit is on its <a href="<?= e($this->url('sites.php', ['id' => $id])) ?>">PHP settings</a> page.</p>
        <?php endif; ?>
    </div>
</div>

<div class="card mb-3">
    <div class="card-header"><i class="bi bi-exclamation-triangle me-1"></i>Faults and warnings<?= $this->hint('A fault means a limit was actually hit (the kernel throttled CPU, reached the memory limit or killed a process). A warning means a threshold (90% of a limit) was crossed and nothing was cut off.', 'Faults and warnings') ?></div>
    <?php if ($events === []): ?>
        <div class="card-body text-body-secondary">None recorded in this period.</div>
    <?php else: ?>
    <div class="table-responsive"><table class="table table-sm align-middle mb-0 nim-table">
        <thead><tr><th>Type</th><th>What happened</th><th>First</th><th>Last</th><th class="text-end">Times</th></tr></thead>
        <tbody>
        <?php foreach ($events as $ev): ?>
            <tr>
                <td><?= $ev['severity'] === 'fault' ? '<span class="badge text-bg-danger">Fault</span>' : '<span class="badge text-bg-warning">Warning</span>' ?> <?= e($kinds[$ev['kind']] ?? (string) $ev['kind']) ?></td>
                <td class="small"><?= e((string) $ev['detail']) ?></td>
                <td class="small"><?= e(gmdate('Y-m-d H:i', (int) $ev['first_at'])) ?></td>
                <td class="small"><?= e(gmdate('Y-m-d H:i', (int) $ev['last_at'])) ?></td>
                <td class="text-end"><?= (int) $ev['occurrences'] ?></td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table></div>
    <?php endif; ?>
</div>

<div class="card mb-3">
    <div class="card-header"><i class="bi bi-info-circle me-1"></i>Not measured, and why</div>
    <div class="card-body small">
        <ul class="mb-0">
            <?php if ($fpm !== null && $fpm['enabled']): ?>
            <li><strong>PHP-FPM busy workers and queue:</strong> recorded from the pool's status page (switched on). A pool that did not answer at a sample leaves a gap.</li>
            <?php else: ?>
            <li><strong>PHP-FPM busy and idle workers, the request queue, worker-limit faults:</strong> the pools' status page is off, so these are not recorded; the worker count above is read from the processes.<?= $admin ? ' An administrator can switch it on under <a href="' . e($this->url('resources')) . '">Resource usage</a>.' : '' ?></li>
            <?php endif; ?>
            <li><strong>Response times:</strong> the access log format has no request time, so none is shown rather than guessed.</li>
            <li><strong>Database activity per website:</strong> not shown. MariaDB's Performance Schema is not used by NimVor, and attribution by default schema is not reliable enough to display as a website's figure.</li>
            <li><strong>Disk I/O limit exhaustion and IOPS:</strong> the kernel keeps no counter for I/O limit throttling; per-account IOPS are recorded when the account has a slice (<?= $a === null ? 'this one has none' : 'see the account limits above' ?>).</li>
        </ul>
    </div>
</div>

<script src="<?= e($this->asset('assets/js/charts.js')) ?>" defer></script>
