<?php
/**
 * @var \NimVor\View\View $this
 * @var list<array<string, mixed>> $rows
 * @var string $range
 * @var string $sort
 * @var string $q
 * @var bool $faults
 * @var bool $admin
 * @var array<string, mixed>|null $fpm
 */
$ranges = ['1h' => '1 hour', '24h' => '24 hours', '7d' => '7 days', '30d' => '30 days'];
$sorts = ['cpu' => 'CPU', 'memory' => 'Memory', 'io' => 'Disk I/O', 'requests' => 'Requests', 'traffic' => 'Traffic', 'errors' => 'Errors', 'faults' => 'Faults'];
$dash = '—';
$bytes = static fn (?int $b): string => $b === null ? '—' : format_bytes($b, 1);
?>
<?php $this->start(); ?>Resource usage<?php $this->end('title'); ?>
<?php $this->start(); ?>Which websites use the most, from what the monitoring pass recorded<?php $this->end('subtitle'); ?>

<p class="small nim-muted">CPU, memory and disk I/O are those of each website's own PHP-FPM workers (sampled with every monitoring pass: a worker that starts and ends between two samples is not seen). Requests, traffic and errors come from the website's own access log. Fault counts are those of the website's limits account, whose slice the account's websites share. A dash means nothing was measured, not zero.</p>

<?php if ($admin && $fpm !== null): ?>
<div class="card mb-3"><div class="card-body d-flex flex-wrap gap-3 align-items-center">
    <div class="flex-grow-1">
        <div class="fw-semibold">PHP-FPM status<?= $this->hint('Adds one line, pm.status_path, to each website\'s PHP pool so the monitor can read busy and idle workers, the request queue and how often a pool hit its worker limit. It is answered by PHP-FPM itself on the pool\'s socket: it runs no PHP file, shows only counters, and is not reachable from the web. NimVor checks the configuration (php-fpm -t) before reloading PHP-FPM, leaves pools you edited by hand alone, and puts everything back if a step fails. No limit changes.', 'PHP-FPM status') ?>
            <?= $fpm['enabled'] ? '<span class="badge text-bg-success fw-normal">On</span>' : '<span class="badge text-bg-secondary fw-normal">Off</span>' ?></div>
        <div class="small nim-muted"><?= $fpm['enabled'] ? 'Switched on for ' . (int) $fpm['with_status'] . ' of ' . (int) $fpm['php_sites'] . ' PHP websites. Busy workers, the request queue and worker-limit faults are recorded.' : 'Off: the pools publish nothing, so busy/idle workers, the queue and worker-limit faults are not recorded. Switching it on reloads PHP-FPM once.' ?></div>
    </div>
    <form method="post" action="<?= e($this->url('resources.fpm')) ?>" data-nim-busy<?= $fpm['enabled'] ? '' : ' data-nim-confirm="Add the PHP-FPM status page to the pools of your PHP websites? NimVor checks the configuration and reloads PHP-FPM once; no limit changes."' ?>>
        <?= $this->csrfField() ?>
        <input type="hidden" name="enabled" value="<?= $fpm['enabled'] ? '0' : '1' ?>">
        <button class="btn btn-sm <?= $fpm['enabled'] ? 'btn-outline-secondary' : 'btn-primary' ?>"><?= $fpm['enabled'] ? 'Switch off' : 'Switch on' ?></button>
    </form>
</div></div>
<?php endif; ?>

<form class="row g-2 align-items-end mb-3" method="get" action="<?= e($this->url('resources')) ?>">
    <div class="col-auto"><label class="form-label small mb-0" for="r-range">Period</label>
        <select class="form-select form-select-sm" id="r-range" name="range"><?php foreach ($ranges as $k => $l): ?><option value="<?= e($k) ?>"<?= $range === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select></div>
    <div class="col-auto"><label class="form-label small mb-0" for="r-sort">Sort by</label>
        <select class="form-select form-select-sm" id="r-sort" name="sort"><?php foreach ($sorts as $k => $l): ?><option value="<?= e($k) ?>"<?= $sort === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select></div>
    <div class="col-auto"><label class="form-label small mb-0" for="r-q">Website</label>
        <input class="form-control form-control-sm" id="r-q" name="q" value="<?= e($q) ?>" placeholder="part of a domain"></div>
    <div class="col-auto form-check ms-2 mb-1"><input class="form-check-input" type="checkbox" id="r-f" name="faults" value="1"<?= $faults ? ' checked' : '' ?>><label class="form-check-label small" for="r-f">Only with faults</label></div>
    <div class="col-auto"><button class="btn btn-sm btn-primary">Show</button></div>
</form>

<?php if ($rows === []): ?>
    <div class="card"><div class="card-body text-body-secondary">Nothing has been recorded for this period yet. Resource samples are taken with every monitoring pass, for websites that have PHP workers or an access log.</div></div>
<?php else: ?>
<div class="card"><div class="table-responsive">
    <table class="table table-sm align-middle mb-0 nim-table">
        <thead><tr><th>Website</th><th class="text-end">CPU</th><th class="text-end">Memory (peak)</th><th class="text-end">Disk I/O</th><th class="text-end">Requests</th><th class="text-end">Traffic sent</th><th class="text-end">4xx / 5xx</th><th class="text-end">Faults</th></tr></thead>
        <tbody>
        <?php foreach ($rows as $r): ?>
            <tr>
                <td><a href="<?= e($this->url('sites.resources', ['id' => $r['id']], ['range' => $range])) ?>"><?= e($r['domain']) ?></a><?php if (!empty($r['owner'])): ?><div class="small nim-muted"><?= e((string) $r['owner']) ?></div><?php endif; ?></td>
                <td class="text-end"><?= $r['cpu'] === null ? $dash : e((string) $r['cpu']) . '%' ?></td>
                <td class="text-end"><?= $bytes($r['mem_max']) ?></td>
                <td class="text-end"><?= $bytes($r['io_bytes']) ?></td>
                <td class="text-end"><?= $r['requests'] === null ? $dash : e(number_format((int) $r['requests'])) ?></td>
                <td class="text-end"><?= $bytes($r['bytes_sent']) ?></td>
                <td class="text-end"><?= $r['e4'] === null ? $dash : e(number_format((int) $r['e4'])) . ' / ' . e(number_format((int) $r['e5'])) ?></td>
                <td class="text-end"><?= (int) $r['faults'] > 0 ? '<span class="badge text-bg-danger">' . (int) $r['faults'] . '</span>' : '0' ?></td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div></div>
<?php endif; ?>
