<?php /** @var \WhaCP\View\View $this */ ?>
<?php $this->start(); ?>Your profile<?php $this->end('title'); ?>

<div class="row g-3">
    <div class="col-lg-5">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-person me-1"></i>Account</div>
            <div class="card-body">
                <dl class="wcp-dl">
                    <dt>Username</dt><dd><?= e($profile['username']) ?></dd>
                    <dt>Email</dt><dd><?= e($profile['email']) ?></dd>
                    <dt>Role</dt><dd><?= e($profile['role_label']) ?></dd>
                    <dt>Last sign-in</dt><dd><?= e(time_ago($profile['last_login_at'])) ?></dd>
                    <dt>Password changed</dt><dd><?= e(time_ago($profile['password_changed_at'])) ?></dd>
                </dl>
            </div>
        </div>
        <div class="card">
            <div class="card-header"><i class="bi bi-key me-1"></i>Your permissions</div>
            <div class="card-body">
                <?php foreach ($permissions as $p): ?><span class="badge text-bg-light border me-1 mb-1"><?= e($p) ?></span><?php endforeach; ?>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card">
            <div class="card-header"><i class="bi bi-shield-lock me-1"></i>Change password</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('profile.password')) ?>" data-wcp-busy novalidate>
                    <?= $this->csrfField() ?>
                    <?= $this->partial('partials/field', ['name' => 'current_password', 'label' => 'Current password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'current-password', 'required' => 'required']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'password', 'label' => 'New password', 'type' => 'password', 'value' => '', 'help' => 'At least 12 characters.', 'attrs' => ['autocomplete' => 'new-password', 'required' => 'required']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'password_confirmation', 'label' => 'Confirm new password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'new-password', 'required' => 'required']]) ?>
                    <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Change password</button>
                </form>
            </div>
        </div>
    </div>
</div>
