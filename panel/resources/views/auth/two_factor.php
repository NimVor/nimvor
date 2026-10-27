<?php /** @var \WhaCP\View\View $this */ ?>
<div class="card wcp-auth-card">
    <div class="card-body p-4">
        <h1 class="h5 mb-1">Two-factor sign-in</h1>
        <?php $methods ??= ['totp' => true, 'key' => false]; ?>
        <?= $this->partial('partials/flash') ?>
        <?php if (!empty($methods['key'])): ?>
            <div id="wcp-key-login" data-options="<?= e($this->url('login.key.options')) ?>" data-verify="<?= e($this->url('login.key')) ?>">
                <p class="text-body-secondary small mb-3">Use your security key or passkey.</p>
                <button type="button" class="btn btn-primary w-100 mb-2" data-wcp-key-login><i class="bi bi-usb-drive me-1"></i>Use security key</button>
                <div class="small text-danger mb-2 d-none" data-wcp-key-error role="alert"></div>
            </div>
            <?php if (!empty($methods['totp'])): ?><div class="text-center small text-body-secondary my-3">or enter a code</div><?php endif; ?>
        <?php endif; ?>
        <?php if (!empty($methods['totp'])): ?>
        <p class="text-body-secondary small mb-4">Enter the 6-digit code from your authenticator app, or one of your recovery codes.</p>
        <form method="post" action="<?= e($this->url('login.two_factor.submit')) ?>" data-wcp-busy novalidate>
            <?= $this->csrfField() ?>
            <div class="mb-4">
                <label class="form-label" for="code">Code</label>
                <input class="form-control form-control-lg text-center font-monospace" id="code" name="code" inputmode="numeric" autocomplete="one-time-code" required<?= empty($methods['key']) ? ' autofocus' : '' ?> maxlength="40" spellcheck="false">
            </div>
            <button class="btn btn-primary w-100" type="submit">
                <span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Verify
            </button>
        </form>
        <?php endif; ?>
        <form method="post" action="<?= e($this->url('login.two_factor.submit')) ?>" class="text-center mt-3">
            <?= $this->csrfField() ?><input type="hidden" name="cancel" value="1">
            <button class="btn btn-link btn-sm text-body-secondary">Cancel and sign in again</button>
        </form>
    </div>
</div>
<script src="<?= e($this->asset('assets/js/webauthn.js')) ?>" defer></script>
