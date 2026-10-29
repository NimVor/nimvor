<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $sponsors */
/** @var list<string> $tiers */
/** @var bool $hasToken */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Sponsors');
?>
<div class="app-top"><div><h1>Sponsors</h1><p class="muted mb-0">Shown on the public Sponsors page: the public sponsors from GitHub Sponsors, and the ones you add here.</p></div></div>
<div class="panel form">
    <h2 class="mb-0">GitHub Sponsors</h2>
    <?php if ($hasToken): ?>
        <p class="muted mb-0">Copies the public, active sponsors of the organisation. Tiers follow the monthly amount (silver from $25, gold from $100). Sponsors you added by hand are never changed.</p>
        <form method="post" action="/admin/sponsors/sync" data-once><?= $v->csrf() ?><button class="btn btn-primary" type="submit">Sync from GitHub now</button></form>
    <?php else: ?>
        <p class="muted mb-0">Not connected. GitHub only shows sponsors to a signed-in maintainer: set <code>GITHUB_TOKEN</code> (a token that can read the organisation's sponsors) in the site's configuration, then come back here. Until then add sponsors by hand below.</p>
    <?php endif; ?>
</div>
<form class="panel form" method="post" action="/admin/sponsors" novalidate data-once>
    <?= $v->csrf() ?><h2 class="mb-0">Add a sponsor</h2>
    <div class="field"><label for="name">Name</label><input class="input" id="name" name="name" value="<?= e($old['name'] ?? '') ?>" maxlength="120" required><?= $v->error($errors, 'name') ?></div>
    <div class="field"><label for="url">Link</label><input class="input" id="url" name="url" value="<?= e($old['url'] ?? '') ?>" maxlength="255" placeholder="https://" required><?= $v->error($errors, 'url') ?></div>
    <div class="field"><label for="tier">Tier</label><select class="select" id="tier" name="tier"><?php foreach ($tiers as $t): ?><option value="<?= e($t) ?>"<?= ($old['tier'] ?? '') === $t ? ' selected' : '' ?>><?= e($t) ?></option><?php endforeach; ?></select><?= $v->error($errors, 'tier') ?></div>
    <div><button class="btn btn-primary" type="submit">Add sponsor</button></div>
</form>
<?php if ($sponsors !== []): ?>
<div class="table-wrap"><table class="data"><thead><tr><th>Name</th><th>Link</th><th>Tier</th><th></th></tr></thead><tbody>
    <?php foreach ($sponsors as $s): ?>
        <tr><td><?= e($s['name']) ?><?= ($s['source'] ?? '') === 'github' ? ' <span class="badge">GitHub</span>' : '' ?></td><td><?= e($s['url']) ?></td><td><?= e($s['tier']) ?></td>
            <td class="cols-actions"><?php if (($s['source'] ?? '') === 'github'): ?><span class="muted">synced</span><?php else: ?><form class="inline-form" method="post" action="/admin/sponsors/<?= (int) $s['id'] ?>/delete" data-confirm="Remove this sponsor?"><?= $v->csrf() ?><button class="btn btn-danger btn-sm" type="submit">Remove</button></form><?php endif; ?></td></tr>
    <?php endforeach; ?>
</tbody></table></div>
<?php endif; ?>
