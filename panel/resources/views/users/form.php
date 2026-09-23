<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $editUser */
$isNew = $editUser === null;
$val = static fn (string $k, mixed $default = '') => $old[$k] ?? ($editUser[$k] ?? $default);
$roleId = (int) $val('role_id', 0);
$active = $isNew ? ($old['is_active'] ?? '1') === '1' : (isset($old['is_active']) ? (bool) $old['is_active'] : (int) $editUser['is_active'] === 1);
?>
<?php $this->start(); ?><?= $isNew ? 'New panel user' : 'Edit ' . e($editUser['username']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('users')) ?>"><i class="bi bi-arrow-left"></i> Users</a><?php $this->end('actions'); ?>

<div class="row">
    <div class="col-lg-7 col-xl-6">
        <div class="card">
            <div class="card-body">
                <form method="post" action="<?= e($isNew ? $this->url('users.store') : $this->url('users.update', ['id' => (int) $editUser['id']])) ?>" data-wcp-busy novalidate>
                    <?= $this->csrfField() ?>
                    <?php if ($isNew): ?>
                        <?= $this->partial('partials/field', ['name' => 'username', 'label' => 'Username', 'type' => 'text', 'value' => (string) $val('username'), 'help' => '3–32 characters: lowercase letters, digits, dot, dash, underscore.', 'attrs' => ['required' => 'required', 'autocomplete' => 'off', 'maxlength' => '32']]) ?>
                    <?php else: ?>
                        <div class="mb-3"><label class="form-label">Username</label><input class="form-control" value="<?= e($editUser['username']) ?>" disabled></div>
                    <?php endif; ?>
                    <?= $this->partial('partials/field', ['name' => 'email', 'label' => 'Email', 'type' => 'email', 'value' => (string) $val('email'), 'attrs' => ['required' => 'required', 'maxlength' => '190']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'display_name', 'label' => 'Display name', 'type' => 'text', 'value' => (string) $val('display_name'), 'help' => 'Optional.', 'attrs' => ['maxlength' => '100']]) ?>

                    <div class="mb-3">
                        <label class="form-label" for="f-role_id">Role</label>
                        <select class="form-select<?= isset($errors['role_id']) ? ' is-invalid' : '' ?>" id="f-role_id" name="role_id">
                            <?php foreach ($roles as $r): ?>
                                <option value="<?= (int) $r['id'] ?>"<?= $roleId === (int) $r['id'] || ($roleId === 0 && $r['name'] === 'viewer') ? ' selected' : '' ?>><?= e($r['label']) ?> — <?= e($r['description']) ?></option>
                            <?php endforeach; ?>
                        </select>
                        <?php if (isset($errors['role_id'])): ?><div class="invalid-feedback"><?= e($errors['role_id']) ?></div><?php endif; ?>
                    </div>

                    <div class="form-check form-switch mb-3">
                        <input type="hidden" name="is_active" value="0">
                        <input class="form-check-input<?= isset($errors['is_active']) ? ' is-invalid' : '' ?>" type="checkbox" role="switch" id="f-is_active" name="is_active" value="1"<?= $active ? ' checked' : '' ?>>
                        <label class="form-check-label" for="f-is_active">Account enabled</label>
                        <?php if (isset($errors['is_active'])): ?><div class="invalid-feedback"><?= e($errors['is_active']) ?></div><?php endif; ?>
                    </div>

                    <hr>
                    <p class="small text-body-secondary"><?= $isNew ? 'Choose a password of at least 12 characters.' : 'Leave empty to keep the current password.' ?></p>
                    <?= $this->partial('partials/field', ['name' => 'password', 'label' => $isNew ? 'Password' : 'New password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'new-password'] + ($isNew ? ['required' => 'required'] : [])]) ?>
                    <?= $this->partial('partials/field', ['name' => 'password_confirmation', 'label' => 'Confirm password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'new-password']]) ?>

                    <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $isNew ? 'Create user' : 'Save changes' ?></button>
                </form>
            </div>
        </div>
    </div>
</div>
