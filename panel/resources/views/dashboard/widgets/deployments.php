<?php
/**
 * Deployments in the last seven days.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
?>
<div class="row g-2 text-center mb-3">
    <div class="col-4"><div class="fs-5"><?= (int) $d['week'] ?></div><div class="text-body-secondary">Deployments, 7 days</div></div>
    <div class="col-4"><div class="fs-5 <?= (int) $d['failed'] > 0 ? 'text-danger' : '' ?>"><?= (int) $d['failed'] ?></div><div class="text-body-secondary">Failed</div></div>
    <div class="col-4"><div class="fs-5"><?= (int) $d['running'] ?></div><div class="text-body-secondary">Running now</div></div>
</div>
<div class="text-body-secondary">Latest: <span class="font-monospace"><?= e((string) $d['last']['domain']) ?></span>, <?= e((string) $d['last']['status']) ?> at <?= e(str_replace('T', ' ', substr((string) $d['last']['created_at'], 0, 16))) ?> UTC.</div>
<?php if (is_array($d['last_failure'])): ?>
    <div class="text-danger mt-1">Last failure: <span class="font-monospace"><?= e((string) $d['last_failure']['domain']) ?></span> at <?= e(str_replace('T', ' ', substr((string) $d['last_failure']['created_at'], 0, 16))) ?> UTC<?= (string) $d['last_failure']['error'] !== '' ? ' — ' . e(mb_strimwidth((string) $d['last_failure']['error'], 0, 140, '…')) : '' ?></div>
<?php endif; ?>
