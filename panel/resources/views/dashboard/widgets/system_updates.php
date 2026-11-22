<?php
/**
 * System updates.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$n = static fn (?int $v): string => $v === null ? 'N/A' : number_format($v);
?>
<div class="nim-figs">
    <div class="nim-fig"><div class="nim-fig-value <?= ($d['count'] ?? 0) > 0 ? 'text-warning' : '' ?>"><?= e($n($d['count'])) ?></div><div class="nim-fig-label">Updates waiting</div></div>
    <div class="nim-fig"><div class="nim-fig-value <?= ($d['security'] ?? 0) > 0 ? 'text-danger' : '' ?>"><?= e($n($d['security'])) ?></div><div class="nim-fig-label">Security updates</div></div>
</div>
<?php if ($d['reboot_required'] === true): ?>
    <div class="text-warning mb-1"><i class="bi bi-arrow-repeat me-1" aria-hidden="true"></i>A restart is needed to finish installing updates<?= $d['reboot_by'] !== [] ? ' (' . e(implode(', ', (array) $d['reboot_by'])) . ')' : '' ?>.</div>
<?php elseif ($d['reboot_required'] === null): ?>
    <div class="text-body-secondary mb-1">Whether a restart is needed could not be read.</div>
<?php endif; ?>
<?php if ($d['index_stale']): ?>
    <div class="text-warning">The package list was not refreshed for a while<?= $d['index_refreshed'] !== '' ? ' (last ' . e(str_replace('T', ' ', substr((string) $d['index_refreshed'], 0, 16))) . ')' : '' ?>, so updates may be missing here.</div>
<?php endif; ?>
