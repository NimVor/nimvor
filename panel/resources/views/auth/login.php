<?php /** @var \NimVor\View\View $this */ ?>
<div class="card nim-auth-card">
    <div class="card-body p-4">
        <h1 class="h5 mb-1">Sign in</h1>
        <p class="text-body-secondary small mb-4">Server control panel</p>
        <?= $this->partial('partials/flash') ?>
        <form method="post" action="<?= e($this->url('login.submit')) ?>" data-nim-busy novalidate>
            <?= $this->csrfField() ?>
            <div class="mb-3">
                <label class="form-label" for="username">Username or email</label>
                <input class="form-control" id="username" name="username" value="<?= e($username ?? '') ?>" autocomplete="username" required autofocus maxlength="190">
            </div>
            <div class="mb-4">
                <label class="form-label" for="password">Password</label>
                <input class="form-control" id="password" name="password" type="password" autocomplete="current-password" required>
            </div>
            <button class="btn btn-primary w-100" type="submit">
                <span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Sign in
            </button>
        </form>
        <div class="text-center mt-3"><a class="small" href="<?= e($this->url('password.forgot')) ?>">Forgot your password?</a></div>
    </div>
</div>
