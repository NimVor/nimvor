<?php /** @var \NimVor\View\View $this */ ?>
<div class="card nim-auth-card">
    <div class="card-body p-4">
        <h1 class="h5 mb-1">Verify this IP address</h1>
        <?= $this->partial('partials/flash') ?>
        <p class="text-body-secondary small mb-4">You are signing in from an IP address that has not been verified for your account. We sent a 6-digit code to your email address; it is valid for 15 minutes.</p>
        <form method="post" action="<?= e($this->url('login.verify_ip.submit')) ?>" data-nim-busy novalidate>
            <?= $this->csrfField() ?>
            <div class="mb-4">
                <label class="form-label" for="code">Code</label>
                <input class="form-control form-control-lg text-center font-monospace" id="code" name="code" inputmode="numeric" autocomplete="one-time-code" required autofocus maxlength="20" spellcheck="false">
            </div>
            <button class="btn btn-primary w-100" type="submit">
                <span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Verify
            </button>
        </form>
        <form method="post" action="<?= e($this->url('login.verify_ip.submit')) ?>" class="text-center mt-3">
            <?= $this->csrfField() ?><input type="hidden" name="cancel" value="1">
            <button class="btn btn-link btn-sm text-body-secondary">Cancel and sign in again</button>
        </form>
    </div>
</div>
