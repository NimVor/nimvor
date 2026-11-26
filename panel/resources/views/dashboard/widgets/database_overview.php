<?php
/**
 * Databases: MariaDB/MySQL and PostgreSQL. Unmeasured values say N/A.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$n = static fn (?int $v): string => $v === null ? 'N/A' : number_format($v);
$pct = static fn (?float $v): string => $v === null ? 'N/A' : number_format($v, 1) . '%';
$up = static fn (?int $s): string => $s === null ? 'N/A' : ($s >= 86400 ? intdiv($s, 86400) . ' d' : ($s >= 3600 ? intdiv($s, 3600) . ' h' : intdiv($s, 60) . ' min'));
$conn = static fn (?int $c, ?int $m): string => $c === null ? 'N/A' : number_format($c) . ($m !== null ? ' / ' . number_format($m) : '');
$full = static fn (?int $c, ?int $m): bool => $c !== null && $m !== null && $m > 0 && $c / $m >= 0.8;
$tabs = ['conn' => 'Connections and statements'];
if ($d['mysql'] !== null) { $tabs['mysql'] = 'MariaDB / MySQL'; }
if ($d['postgres'] !== null) { $tabs['postgres'] = 'PostgreSQL'; }
?>
<div data-nim-tabs="databases">
    <?= $this->partial('dashboard/tabs', ['tabs' => $tabs, 'label' => 'Databases']) ?>
    <div data-nim-pane="conn">
<?php if ($d['mysql'] !== null && $d['mysql']['running']): ?>
    <div data-chart-scope="database">
        <div class="nim-chart-bar">
            <div class="nim-pills" role="tablist" aria-label="Database metric">
                <button type="button" class="nim-pill active" role="tab" aria-selected="true" data-chart-tab="db_conn">Connections</button>
                <button type="button" class="nim-pill" role="tab" aria-selected="false" data-chart-tab="db_qps">Statements per second</button>
            </div>
            <div class="nim-pills" role="group" aria-label="Time range">
                <?php foreach (['1h' => '1 h', '6h' => '6 h', '24h' => '24 h', '7d' => '7 d'] as $k => $label): ?><button type="button" class="nim-pill" data-range-button="<?= e($k) ?>"><?= e($label) ?></button><?php endforeach; ?>
            </div>
        </div>
        <div data-nim-chart data-metric="db_conn" data-series-url="<?= e($this->url('dashboard.series')) ?>" data-height="110"></div>
        <div class="nim-widget-foot">History is recorded every five minutes for MariaDB / MySQL; PostgreSQL shows its current state only.</div>
    </div>
<?php endif; ?>
        <?php if ($d['mysql'] === null || !$d['mysql']['running']): ?><div class="text-body-secondary">No history to draw: the charts need MariaDB / MySQL to be running.</div><?php endif; ?>
    </div>
<?php if ($d['mysql'] !== null): ?>
    <div data-nim-pane="mysql" hidden>
<?php if ($d['mysql'] !== null): $m = $d['mysql']; ?>
        <div class="fw-semibold mb-1">MariaDB / MySQL <span class="fw-normal <?= $m['running'] ? 'text-success' : 'text-danger' ?>"><?= $m['running'] ? 'running' : 'not answering' ?></span></div>
        <?php if (!$m['running']): ?>
            <div class="text-body-secondary">The database server does not answer<?= $m['error'] !== '' ? ': ' . e(mb_strimwidth($m['error'], 0, 160, '…')) : '' ?>.</div>
        <?php else: ?>
            <table class="table table-sm mb-0"><tbody>
                <tr><td>Version, uptime</td><td class="text-end"><?= e($m['version'] !== '' ? $m['version'] : 'N/A') ?> · <?= e($up($m['uptime'])) ?></td></tr>
                <tr><td>Connections</td><td class="text-end <?= $full($m['connections'], $m['max_connections']) ? 'text-warning' : '' ?>"><?= e($conn($m['connections'], $m['max_connections'])) ?> (<?= e($n($m['running_threads'])) ?> running)</td></tr>
                <tr><td>Slow queries since start</td><td class="text-end"><?= e($n($m['slow_queries'])) ?></td></tr>
                <tr><td>InnoDB cache hit ratio</td><td class="text-end <?= $m['buffer_pool_hit'] !== null && $m['buffer_pool_hit'] < 95 ? 'text-warning' : '' ?>"><?= e($pct($m['buffer_pool_hit'])) ?></td></tr>
                <tr><td>Temporary tables on disk</td><td class="text-end"><?= e($pct($m['tmp_on_disk'])) ?></td></tr>
                <tr><td>Databases, size</td><td class="text-end"><?= e($n($m['databases'])) ?> · <?= $m['bytes'] === null ? 'N/A' : e(format_bytes((int) $m['bytes'])) ?></td></tr>
            </tbody></table>
        <?php endif; ?>
<?php endif; ?>
    </div>
<?php endif; ?>
<?php if ($d['postgres'] !== null): ?>
    <div data-nim-pane="postgres" hidden>
<?php if ($d['postgres'] !== null): $p = $d['postgres']; ?>
        <div class="fw-semibold mb-1">PostgreSQL <span class="fw-normal <?= $p['running'] ? 'text-success' : 'text-danger' ?>"><?= $p['running'] ? 'running' : 'not running' ?></span></div>
        <?php if (!$p['running']): ?>
            <div class="text-body-secondary">PostgreSQL is installed but not running.</div>
        <?php else: ?>
            <table class="table table-sm mb-0"><tbody>
                <tr><td>Version, uptime</td><td class="text-end"><?= e($p['version'] !== '' ? $p['version'] : 'N/A') ?> · <?= e($up($p['uptime'])) ?></td></tr>
                <tr><td>Connections</td><td class="text-end <?= $full($p['connections'], $p['max_connections']) ? 'text-warning' : '' ?>"><?= e($conn($p['connections'], $p['max_connections'])) ?></td></tr>
                <tr><td>Statements waiting on locks</td><td class="text-end <?= ($p['lock_waits'] ?? 0) > 0 ? 'text-warning' : '' ?>"><?= e($n($p['lock_waits'])) ?></td></tr>
                <tr><td>Long-running statements</td><td class="text-end"><?= e($n($p['long_running'])) ?></td></tr>
                <tr><td>Cache hit ratio</td><td class="text-end"><?= e($pct($p['cache_hit'])) ?></td></tr>
            </tbody></table>
        <?php endif; ?>
<?php endif; ?>
    </div>
<?php endif; ?>
</div>
