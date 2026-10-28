<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/site');
$v->set('title', 'Log in');
?>
<section class="auth-wrap">
    <form class="card form auth-card" method="post" action="/login" novalidate data-once>
        <?= $v->csrf() ?>
        <input type="hidden" name="next" value="<?= e($next ?? '') ?>">
        <div><h1 class="h-card mb-0">Log in</h1><p class="muted mb-0">Clients and administrators sign in here.</p></div>
        <?php if (!empty($errors['form'])): ?><div class="alert alert-error mb-0" role="alert"><?= e($errors['form']) ?></div><?php endif; ?>
        <div class="field"><label for="email">Email</label><input class="input" id="email" type="email" name="email" value="<?= e($old['email'] ?? '') ?>" autocomplete="username" maxlength="190" required autofocus></div>
        <div class="field"><label for="password">Password</label><input class="input" id="password" type="password" name="password" autocomplete="current-password" maxlength="200" required></div>
        <button class="btn btn-primary" type="submit">Log in</button>
        <p class="muted mb-0">No account? <a href="/register">Create one</a>.</p>
    </form>
</section>
