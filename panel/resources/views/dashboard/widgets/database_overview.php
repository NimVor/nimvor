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
?>
<div class="row g-3">
<?php if ($d['mysql'] !== null): $m = $d['mysql']; ?>
    <div class="col-12 col-lg-6">
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
    </div>
<?php endif; ?>
<?php if ($d['postgres'] !== null): $p = $d['postgres']; ?>
    <div class="col-12 col-lg-6">
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
    </div>
<?php endif; ?>
</div>
