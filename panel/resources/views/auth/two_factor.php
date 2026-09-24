<?php /** @var \WhaCP\View\View $this */ ?>
<div class="card wcp-auth-card">
    <div class="card-body p-4">
        <h1 class="h5 mb-1">Two-factor sign-in</h1>
        <p class="text-body-secondary small mb-4">Enter the 6-digit code from your authenticator app, or one of your recovery codes.</p>
        <?= $this->partial('partials/flash') ?>
        <form method="post" action="<?= e($this->url('login.two_factor.submit')) ?>" data-wcp-busy novalidate>
            <?= $this->csrfField() ?>
            <div class="mb-4">
                <label class="form-label" for="code">Code</label>
                <input class="form-control form-control-lg text-center font-monospace" id="code" name="code" inputmode="numeric" autocomplete="one-time-code" required autofocus maxlength="40" spellcheck="false">
            </div>
            <button class="btn btn-primary w-100" type="submit">
                <span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Verify
            </button>
        </form>
        <form method="post" action="<?= e($this->url('login.two_factor.submit')) ?>" class="text-center mt-3">
            <?= $this->csrfField() ?><input type="hidden" name="cancel" value="1">
            <button class="btn btn-link btn-sm text-body-secondary">Cancel and sign in again</button>
        </form>
    </div>
</div>
