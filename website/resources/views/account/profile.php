<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/app');
$v->share('area', 'account');
$v->set('title', 'Profile and password');
?>
<div class="app-top"><div><h1>Profile and password</h1><p class="muted mb-0">Signed in as <?= e($user['email']) ?>.</p></div></div>
<form class="panel form" method="post" action="/account/profile" novalidate data-once>
    <?= $v->csrf() ?>
    <?php if (!empty($errors)): ?><div class="alert alert-error mb-0" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
    <div class="field"><label for="name">Name</label><input class="input" id="name" name="name" value="<?= e($old['name'] ?? $user['name']) ?>" maxlength="120" required><?= $v->error($errors, 'name') ?></div>
    <h2 class="mb-0">Change password</h2>
    <p class="muted mb-0">Leave these empty to keep your password.</p>
    <div class="field"><label for="current_password">Current password</label><input class="input" id="current_password" type="password" name="current_password" autocomplete="current-password" maxlength="200"><?= $v->error($errors, 'current_password') ?></div>
    <div class="field"><label for="new_password">New password</label><input class="input" id="new_password" type="password" name="new_password" autocomplete="new-password" maxlength="200"><div class="hint">At least 12 characters.</div><?= $v->error($errors, 'new_password') ?></div>
    <div class="field"><label for="new_password_confirm">Repeat the new password</label><input class="input" id="new_password_confirm" type="password" name="new_password_confirm" autocomplete="new-password" maxlength="200"><?= $v->error($errors, 'new_password_confirm') ?></div>
    <div><button class="btn btn-primary" type="submit">Save</button></div>
</form>
