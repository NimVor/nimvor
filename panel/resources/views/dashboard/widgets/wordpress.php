<?php
/**
 * WordPress installations from the last scan.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
?>
<div class="nim-figs">
    <div class="nim-fig"><div class="nim-fig-value"><?= (int) $d['installs'] ?></div><div class="nim-fig-label">Installs</div></div>
    <div class="nim-fig"><div class="nim-fig-value <?= (int) $d['outdated_total'] > 0 ? 'text-warning' : '' ?>"><?= (int) $d['outdated_total'] ?></div><div class="nim-fig-label">Core out of date</div></div>
    <div class="nim-fig"><div class="nim-fig-value <?= (int) $d['plugin_updates'] > 0 ? 'text-warning' : '' ?>"><?= (int) $d['plugin_updates'] ?></div><div class="nim-fig-label">Plugin updates</div></div>
    <div class="nim-fig"><div class="nim-fig-value <?= (int) $d['theme_updates'] > 0 ? 'text-warning' : '' ?>"><?= (int) $d['theme_updates'] ?></div><div class="nim-fig-label">Theme updates</div></div>
</div>
<?php foreach ((array) $d['outdated'] as $o): ?>
    <div><span class="font-monospace"><?= e((string) $o['domain']) ?></span> <span class="text-body-secondary">runs <?= e((string) $o['version']) ?>, latest is <?= e((string) $o['latest']) ?></span></div>
<?php endforeach; ?>
<?php if ((int) $d['issues'] > 0): ?><div class="text-warning mt-1"><?= (int) $d['issues'] ?> open issue<?= (int) $d['issues'] === 1 ? '' : 's' ?> found by the scan.</div><?php endif; ?>
<div class="text-body-secondary mt-1">From the scan of <?= $d['scanned_at'] === null ? 'N/A' : e(str_replace('T', ' ', substr((string) $d['scanned_at'], 0, 16))) . ' UTC' ?>; the numbers change when a site is scanned again.</div>
