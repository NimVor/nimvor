<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $sponsors */
/** @var list<string> $tiers */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Sponsors');
?>
<div class="app-top"><div><h1>Sponsors</h1><p class="muted mb-0">Shown on the public Sponsors page.</p></div></div>
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
        <tr><td><?= e($s['name']) ?></td><td><?= e($s['url']) ?></td><td><?= e($s['tier']) ?></td>
            <td class="cols-actions"><form class="inline-form" method="post" action="/admin/sponsors/<?= (int) $s['id'] ?>/delete" data-confirm="Remove this sponsor?"><?= $v->csrf() ?><button class="btn btn-danger btn-sm" type="submit">Remove</button></form></td></tr>
    <?php endforeach; ?>
</tbody></table></div>
<?php endif; ?>
