<?php
/**
 * WordPress installations from the last scan.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
?>
<div class="row g-2 text-center mb-3">
    <div class="col-3"><div class="fs-5"><?= (int) $d['installs'] ?></div><div class="text-body-secondary">Installs</div></div>
    <div class="col-3"><div class="fs-5 <?= (int) $d['outdated_total'] > 0 ? 'text-warning' : '' ?>"><?= (int) $d['outdated_total'] ?></div><div class="text-body-secondary">Core out of date</div></div>
    <div class="col-3"><div class="fs-5 <?= (int) $d['plugin_updates'] > 0 ? 'text-warning' : '' ?>"><?= (int) $d['plugin_updates'] ?></div><div class="text-body-secondary">Plugin updates</div></div>
    <div class="col-3"><div class="fs-5 <?= (int) $d['theme_updates'] > 0 ? 'text-warning' : '' ?>"><?= (int) $d['theme_updates'] ?></div><div class="text-body-secondary">Theme updates</div></div>
</div>
<?php foreach ((array) $d['outdated'] as $o): ?>
    <div><span class="font-monospace"><?= e((string) $o['domain']) ?></span> <span class="text-body-secondary">runs <?= e((string) $o['version']) ?>, latest is <?= e((string) $o['latest']) ?></span></div>
<?php endforeach; ?>
<?php if ((int) $d['issues'] > 0): ?><div class="text-warning mt-1"><?= (int) $d['issues'] ?> open issue<?= (int) $d['issues'] === 1 ? '' : 's' ?> found by the scan.</div><?php endif; ?>
<div class="text-body-secondary mt-1">From the scan of <?= $d['scanned_at'] === null ? 'N/A' : e(str_replace('T', ' ', substr((string) $d['scanned_at'], 0, 16))) . ' UTC' ?>; the numbers change when a site is scanned again.</div>
