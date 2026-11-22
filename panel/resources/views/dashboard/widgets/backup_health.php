<?php
/**
 * Backup health. Whether a backup can be restored is not recorded, so it is not claimed.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$ago = static fn (?string $t): string => $t === null || $t === '' ? 'never' : str_replace('T', ' ', substr($t, 0, 16)) . ' UTC';
?>
<div class="nim-figs">
    <div class="nim-fig"><div class="nim-fig-value <?= (int) $d['covered'] < (int) $d['sites'] ? 'text-warning' : 'text-success' ?>"><?= (int) $d['covered'] ?> / <?= (int) $d['sites'] ?></div><div class="nim-fig-label">Websites backed up, 7 days</div></div>
    <div class="nim-fig"><div class="nim-fig-value"><?= (int) $d['completed'] ?></div><div class="nim-fig-label">Backups kept</div></div>
    <div class="nim-fig"><div class="nim-fig-value"><?= e(format_bytes((int) $d['size'])) ?></div><div class="nim-fig-label">Total size</div></div>
</div>
<?php if ($d['uncovered'] !== []): ?>
    <div class="text-warning mb-2">No backup in the last 7 days: <span class="font-monospace"><?= e(implode(', ', array_slice((array) $d['uncovered'], 0, 4))) ?></span><?= count((array) $d['uncovered']) > 4 || (int) $d['sites'] - (int) $d['covered'] > count((array) $d['uncovered']) ? ', …' : '' ?></div>
<?php endif; ?>
<?php if (is_array($d['failed'])): ?>
    <div class="text-danger mb-2">Last failure: <span class="font-monospace"><?= e((string) $d['failed']['domain']) ?></span> at <?= e($ago((string) $d['failed']['created_at'])) ?><?= (string) $d['failed']['error'] !== '' ? ' — ' . e(mb_strimwidth((string) $d['failed']['error'], 0, 140, '…')) : '' ?></div>
<?php endif; ?>
<table class="table table-sm mb-0"><tbody>
    <tr><td>Last completed backup</td><td class="text-end"><?= e($ago($d['last'])) ?></td></tr>
    <tr><td>Schedules</td><td class="text-end"><?= (int) $d['schedules_on'] ?> on of <?= (int) $d['schedules'] ?><?= (int) $d['schedules_failing'] > 0 ? ', <span class="text-danger">' . (int) $d['schedules_failing'] . ' failing</span>' : '' ?></td></tr>
    <tr><td>Copies kept off this server</td><td class="text-end"><?= $d['offsite_pct'] === null ? 'N/A' : (int) $d['offsite_pct'] . '%' ?></td></tr>
    <tr><td>Encrypted</td><td class="text-end"><?= $d['encrypted_pct'] === null ? 'N/A' : (int) $d['encrypted_pct'] . '%' ?></td></tr>
</tbody></table>
<div class="text-body-secondary mt-1">Test restores are not tracked, so a backup is not shown as verified.</div>
