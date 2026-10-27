<?php /** @var \NimVor\View\View $this */ ?>
<div class="card nim-auth-card">
    <div class="card-body p-4">
        <h1 class="h5 mb-3">Choose a new password</h1>
        <?php if (!$valid): ?>
            <p class="small">This link has expired or was already used.</p>
            <a class="btn btn-outline-primary w-100" href="<?= e($this->url('password.forgot')) ?>">Ask for a new link</a>
        <?php else: ?>
            <form method="post" action="<?= e($this->url('password.reset', ['token' => $token])) ?>" data-nim-busy novalidate>
                <?= $this->csrfField() ?>
                <?= $this->partial('partials/field', ['name' => 'password', 'label' => 'New password', 'type' => 'password', 'value' => '', 'help' => 'At least 12 characters.', 'attrs' => ['autocomplete' => 'new-password', 'required' => 'required', 'autofocus' => 'autofocus']]) ?>
                <?= $this->partial('partials/field', ['name' => 'password_confirmation', 'label' => 'Confirm new password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'new-password', 'required' => 'required']]) ?>
                <button class="btn btn-primary w-100" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Change password</button>
            </form>
        <?php endif; ?>
    </div>
</div>
