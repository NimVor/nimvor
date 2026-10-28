<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/site');
$v->set('title', 'Create an account');
?>
<section class="auth-wrap">
    <form class="card form auth-card" method="post" action="/register" novalidate data-once>
        <?= $v->csrf() ?>
        <div><h1 class="h-card mb-0">Create an account</h1><p class="muted mb-0">Track build-service requests and your messages.</p></div>
        <?php if (!empty($errors)): ?><div class="alert alert-error mb-0" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
        <div class="field"><label for="name">Your name</label><input class="input" id="name" name="name" value="<?= e($old['name'] ?? '') ?>" autocomplete="name" maxlength="120" required><?= $v->error($errors, 'name') ?></div>
        <div class="field"><label for="email">Email</label><input class="input" id="email" type="email" name="email" value="<?= e($old['email'] ?? '') ?>" autocomplete="email" maxlength="190" required><?= $v->error($errors, 'email') ?></div>
        <div class="field"><label for="password">Password</label><input class="input" id="password" type="password" name="password" autocomplete="new-password" maxlength="200" required><div class="hint">At least 12 characters.</div><?= $v->error($errors, 'password') ?></div>
        <div class="field"><label for="password_confirm">Repeat the password</label><input class="input" id="password_confirm" type="password" name="password_confirm" autocomplete="new-password" maxlength="200" required><?= $v->error($errors, 'password_confirm') ?></div>
        <button class="btn btn-primary" type="submit">Create account</button>
        <p class="muted mb-0">Already registered? <a href="/login">Log in</a>. By registering you accept the <a href="/support/privacy">privacy policy</a>.</p>
    </form>
</section>
