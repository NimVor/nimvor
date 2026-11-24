<?php
/**
 * @var \NimVorSite\Core\View $v
 * @var array<string, string> $social key => label
 * @var array<string, string> $saved
 * @var string $trustpilotDefault
 * @var array<string, string> $errors
 * @var array<string, mixed> $old
 */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Settings');
?>
<div class="app-top"><div><h1>Settings</h1><p class="muted mb-0">Links shown in the footer of every public page.</p></div></div>
<form class="panel form" method="post" action="/admin/settings" novalidate data-once>
    <?= $v->csrf() ?>
    <?php if (!empty($errors)): ?><div class="alert alert-error mb-0" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
    <h2 class="mb-0">NimVor on social media</h2>
    <p class="muted mb-0">An icon appears in the footer for each address you fill in; an empty one is not shown.</p>
    <?php foreach ($social as $key => $label): $f = 'social_' . $key; ?>
        <div class="field"><label for="<?= e($f) ?>"><?= e($label) ?></label><input class="input" id="<?= e($f) ?>" name="<?= e($f) ?>" value="<?= e($old[$f] ?? $saved[$f] ?? '') ?>" maxlength="250" placeholder="https://" inputmode="url" autocomplete="off"><?= $v->error($errors, $f) ?></div>
    <?php endforeach; ?>
    <h2 class="mb-0">Reviews</h2>
    <div class="field"><label for="trustpilot">Trustpilot page</label><input class="input" id="trustpilot" name="trustpilot" value="<?= e($old['trustpilot'] ?? $saved['trustpilot'] ?? '') ?>" maxlength="250" placeholder="<?= e($trustpilotDefault) ?>" inputmode="url" autocomplete="off">
        <div class="hint">Where &ldquo;Review us on Trustpilot&rdquo; leads. Leave empty to use <?= e($trustpilotDefault) ?>.</div><?= $v->error($errors, 'trustpilot') ?></div>
    <div><button class="btn btn-primary" type="submit">Save settings</button></div>
</form>
