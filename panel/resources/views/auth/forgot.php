<?php /** @var \NimVor\View\View $this */ ?>
<div class="card nim-auth-card">
    <div class="card-body p-4">
        <h1 class="h5 mb-1">Reset your password</h1>
        <?php if ($available): ?>
            <p class="text-body-secondary small mb-4">Enter your username or email. We will send a link to the email address of the account.</p>
            <form method="post" action="<?= e($this->url('password.send')) ?>" data-nim-busy novalidate>
                <?= $this->csrfField() ?>
                <div class="mb-4">
                    <label class="form-label" for="identifier">Username or email</label>
                    <input class="form-control" id="identifier" name="identifier" autocomplete="username" required autofocus maxlength="190">
                </div>
                <button class="btn btn-primary w-100" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Send reset link</button>
            </form>
        <?php else: ?>
            <p class="small mt-3">Password reset by email is not set up on this panel. Ask an administrator to reset your password, or run <code>bin/nimvor user:password &lt;username&gt;</code> on the server.</p>
        <?php endif; ?>
        <div class="text-center mt-3"><a class="small" href="<?= e($this->url('login')) ?>">Back to sign in</a></div>
    </div>
</div>
