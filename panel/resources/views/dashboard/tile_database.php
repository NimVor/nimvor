<?php
/**
 * The hover card of the Database server tile: MariaDB / MySQL as the Databases widget reads it. A reading that could not be taken says N/A.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d databases(): mysql, postgres
 */
$m = $d['mysql'] ?? null;
$n = static fn (?int $v): string => $v === null ? 'N/A' : number_format($v);
$pct = static fn (?float $v): string => $v === null ? 'N/A' : number_format($v, 1) . '%';
$up = static fn (?int $s): string => $s === null ? 'N/A' : ($s >= 86400 ? intdiv($s, 86400) . ' d ' . intdiv($s % 86400, 3600) . ' h' : ($s >= 3600 ? intdiv($s, 3600) . ' h ' . intdiv($s % 3600, 60) . ' min' : intdiv($s, 60) . ' min'));
?>
<?php if ($m === null): ?>
    <div class="text-body-secondary">The database server's state could not be read.</div>
<?php else: ?>
    <div class="mb-1"><span class="<?= $m['running'] ? 'text-success' : 'text-danger' ?>"><?= $m['running'] ? 'Running' : 'Not answering' ?></span><?= $m['version'] !== '' ? ' · ' . e((string) $m['version']) : '' ?></div>
    <?php if (!$m['running']): ?>
        <div class="text-body-secondary">The database server does not answer<?= $m['error'] !== '' ? ': ' . e(mb_strimwidth((string) $m['error'], 0, 160, '…')) : '' ?>.</div>
    <?php else: ?>
        <table class="table table-sm nim-pop-table mb-0"><tbody>
            <tr><td>Running for</td><td class="text-end"><?= e($up($m['uptime'])) ?></td></tr>
            <tr><td>Connections</td><td class="text-end"><?= e($n($m['connections'])) ?><?= $m['max_connections'] !== null ? ' / ' . e(number_format((int) $m['max_connections'])) : '' ?></td></tr>
            <tr><td>Statements running now</td><td class="text-end"><?= e($n($m['running_threads'])) ?></td></tr>
            <tr><td>Slow queries since start</td><td class="text-end"><?= e($n($m['slow_queries'])) ?></td></tr>
            <tr><td>InnoDB cache hit ratio</td><td class="text-end"><?= e($pct($m['buffer_pool_hit'])) ?></td></tr>
            <tr><td>Databases, size</td><td class="text-end"><?= e($n($m['databases'])) ?> · <?= $m['bytes'] === null ? 'N/A' : e(format_bytes((int) $m['bytes'])) ?></td></tr>
        </tbody></table>
        <div class="small text-body-secondary mt-1">Counters count from the last start of the server.</div>
    <?php endif; ?>
<?php endif; ?>
