<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $rows plan usage rows from the account (key, label, used, limit) */
/** @var string $variant 'bars' (default) or 'inline' */
$variant ??= 'bars';
$shown = array_values(array_filter($rows, static fn (array $u): bool => $u['limit'] > 0 || in_array($u['key'], ['disk', 'bandwidth'], true)));
?>
<?php if ($shown === []): ?><p class="small text-body-secondary mb-0">Your plan has no limits to show.</p><?php else: ?>
<ul class="nt-usage nt-usage-<?= e($variant) ?>" aria-label="Plan usage">
    <?php foreach ($shown as $u): $pct = $u['limit'] > 0 ? (int) min(100, round(100 * $u['used'] / max(1, $u['limit']))) : 0; ?>
        <li data-usage="<?= e($u['key']) ?>">
            <div class="nt-usage-line"><span><?= e($u['label']) ?></span><span class="fw-semibold<?= $u['limit'] > 0 && $u['used'] >= $u['limit'] ? ' nim-bad' : '' ?>"><?= (int) $u['used'] ?> / <?= $u['limit'] > 0 ? (int) $u['limit'] : '∞' ?></span></div>
            <div class="progress" role="progressbar" aria-label="<?= e($u['label']) ?>" aria-valuenow="<?= $pct ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar<?= $pct >= 100 ? ' bg-danger' : ($pct >= 80 ? ' bg-warning' : '') ?> nim-w-<?= (int) (round($pct / 5) * 5) ?>"></div></div>
        </li>
    <?php endforeach; ?>
</ul>
<?php endif; ?>
