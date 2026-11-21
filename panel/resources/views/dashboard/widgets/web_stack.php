<?php
/**
 * Nginx and PHP-FPM.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$nx = $d['nginx'];
$n = static fn (?int $v): string => $v === null ? 'N/A' : number_format($v);
?>
<?php if ($nx !== null): ?>
    <div class="mb-3">
        <div class="fw-semibold mb-1">Nginx
            <?php if (!$nx['installed']): ?><span class="fw-normal text-body-secondary">not installed</span>
            <?php else: ?><span class="fw-normal <?= $nx['running'] ? 'text-success' : 'text-danger' ?>"><?= $nx['running'] ? 'running' : 'not running' ?></span><?php endif; ?>
        </div>
        <?php if ($nx['installed']): ?>
            <div class="<?= $nx['config_ok'] ? 'text-body-secondary' : 'text-danger' ?>"><?= $nx['config_ok'] ? 'The configuration test passes.' : 'The configuration test fails, so Nginx will not reload: ' . e(mb_strimwidth($nx['config_error'], 0, 200, '…')) ?></div>
            <div class="text-body-secondary">Request rates are not shown: they need Nginx's status module, which is not enabled.</div>
        <?php endif; ?>
    </div>
<?php endif; ?>
<div class="fw-semibold mb-1">PHP-FPM</div>
<?php if ($d['php'] === []): ?>
    <div class="text-body-secondary"><?= $d['fpm_known'] ? 'No enabled website uses PHP.' : 'PHP-FPM status could not be read.' ?></div>
<?php else: ?>
    <table class="table table-sm mb-0">
        <thead><tr><th>PHP</th><th>State</th><th class="text-end" title="Times a pool had no free worker, last 24 hours">Workers exhausted</th><th class="text-end">Busy</th><th class="text-end">Crashes</th></tr></thead>
        <tbody>
        <?php foreach ((array) $d['php'] as $p): ?>
            <tr>
                <td><?= e((string) $p['version']) ?></td>
                <td class="<?= !$p['installed'] ? 'text-warning' : ($p['active'] ? 'text-success' : 'text-danger') ?>"><?= !$p['installed'] ? 'not installed' : ($p['active'] ? 'running' : 'stopped') ?></td>
                <td class="text-end <?= ($p['max_children'] ?? 0) > 0 ? 'text-warning' : '' ?>" <?= $p['last_pool'] !== '' ? 'title="Last: ' . e((string) $p['last_pool']) . '"' : '' ?>><?= e($n($p['max_children'])) ?></td>
                <td class="text-end"><?= e($n($p['busy'])) ?></td>
                <td class="text-end <?= ($p['crashes'] ?? 0) > 0 ? 'text-danger' : '' ?>"><?= e($n($p['crashes'])) ?></td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
    <div class="text-body-secondary mt-1">Counted from the end of each PHP-FPM log; N/A when the log cannot be read.</div>
<?php endif; ?>
