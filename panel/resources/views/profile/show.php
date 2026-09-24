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

<?php if ($this->can(\WhaCP\Security\Permissions::API_TOKENS)): ?>
<div class="card mt-3">
    <div class="card-header"><i class="bi bi-braces me-1"></i>API tokens</div>
    <div class="card-body">
        <?php if (!empty($newToken)): ?>
            <div class="alert alert-warning">
                <div class="fw-semibold mb-1">Copy your new token now — it is not shown again:</div>
                <div class="d-flex gap-2 align-items-center"><code class="user-select-all text-break" data-wcp-new-token><?= e($newToken) ?></code>
                    <button type="button" class="btn btn-sm btn-outline-secondary" data-wcp-copy="[data-wcp-new-token]"><i class="bi bi-clipboard"></i></button></div>
            </div>
        <?php endif; ?>
        <p class="small text-body-secondary">Tokens act with your permissions. Send them as <code>Authorization: Bearer whacp_…</code> to <code>/api/v1/…</code>; the reference is at <a href="<?= e($this->url('api.openapi')) ?>">/api/v1/openapi.yaml</a>.</p>
        <?php if ($tokens !== []): ?>
            <div class="table-responsive"><table class="table table-sm align-middle">
                <thead><tr><th>Name</th><th>Access</th><th>Expires</th><th>Last used</th><th></th></tr></thead>
                <tbody>
                <?php foreach ($tokens as $t): ?>
                    <tr>
                        <td><?= e($t['name']) ?><div class="small text-body-secondary"><code><?= e($t['prefix']) ?>…</code></div></td>
                        <td><?= $t['ability'] === 'write' ? 'read & write' : 'read-only' ?></td>
                        <td class="small"><?= $t['expires_at'] === null ? 'never' : e(substr((string) $t['expires_at'], 0, 10)) ?></td>
                        <td class="small"><?= $t['last_used_at'] === null ? 'never' : e(time_ago($t['last_used_at'])) . ' from ' . e((string) $t['last_used_ip']) ?></td>
                        <td class="text-end"><form method="post" action="<?= e($this->url('tokens.revoke', ['id' => (int) $t['id']])) ?>" data-wcp-confirm="Revoke the token <?= e($t['name']) ?>? Tools using it stop working."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Revoke</button></form></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table></div>
        <?php endif; ?>
        <form class="row g-2 align-items-end" method="post" action="<?= e($this->url('tokens.store')) ?>">
            <?= $this->csrfField() ?>
            <div class="col-md-4"><label class="form-label small" for="t-name">Name</label><input class="form-control form-control-sm<?= isset($errors['name']) ? ' is-invalid' : '' ?>" id="t-name" name="name" maxlength="60" placeholder="deploy script" required><?php if (isset($errors['name'])): ?><div class="invalid-feedback"><?= e($errors['name']) ?></div><?php endif; ?></div>
            <div class="col-md-3"><label class="form-label small" for="t-ab">Access</label><select class="form-select form-select-sm" id="t-ab" name="ability"><option value="read">Read-only</option><option value="write">Read and write</option></select></div>
            <div class="col-md-3"><label class="form-label small" for="t-exp">Expires</label><select class="form-select form-select-sm" id="t-exp" name="expires_days"><option value="30">in 30 days</option><option value="90" selected>in 90 days</option><option value="365">in 1 year</option><option value="0">never</option></select></div>
            <div class="col-md-2"><button class="btn btn-sm btn-primary w-100">Create token</button></div>
        </form>
    </div>
</div>
<?php endif; ?>
