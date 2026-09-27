<?php /** @var \WhaCP\View\View $this */ ?>
<?php $this->start(); ?>Panel users<?php $this->end('title'); ?>
<?php $this->start(); ?>People who can sign in to WhaCP<?php $this->end('subtitle'); ?>
<?php if ($this->can(\WhaCP\Security\Permissions::USERS_MANAGE)): ?>
    <?php $this->start(); ?><?php if ($this->hasRoute('accounts.create')): ?><a class="btn btn-sm btn-primary" href="<?= e($this->url('accounts.create')) ?>"><i class="bi bi-person-plus-fill me-1"></i>Create account</a> <?php endif; ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('users.create')) ?>"><i class="bi bi-person-plus me-1"></i>New user</a><?php $this->end('actions'); ?>
<?php endif; ?>

<div class="card mb-3">
    <div class="table-responsive">
        <table class="table table-hover align-middle mb-0 wcp-table">
            <thead><tr><th>User</th><th>Role</th><th>Plan</th><th>Status</th><th>Last sign-in</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($users as $u): ?>
                <tr>
                    <td>
                        <span class="fw-semibold"><?= e($u['username']) ?></span>
                        <?php if ((int) $u['id'] === $currentUserId): ?><span class="badge text-bg-light border ms-1">you</span><?php endif; ?>
                        <div class="small text-body-secondary"><?= e($u['display_name'] !== '' ? $u['display_name'] . ' · ' : '') ?><?= e($u['email']) ?></div>
                    </td>
                    <td><?= e($u['role_label']) ?></td>
                    <td class="small"><?= e($u['plan_name'] ?? '—') ?></td>
                    <td><?= !empty($u['suspended']) ? $this->statusBadge('error', 'suspended') : ((int) $u['is_active'] === 1 ? $this->statusBadge('active') : $this->statusBadge('inactive', 'disabled')) ?></td>
                    <td class="small"><?= e(time_ago($u['last_login_at'])) ?><?php if (!empty($u['last_login_ip'])): ?><div class="text-body-secondary"><?= e($u['last_login_ip']) ?></div><?php endif; ?></td>
                    <td class="text-end text-nowrap">
                        <?php if ($this->can(\WhaCP\Security\Permissions::USERS_MANAGE)): ?>
                            <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('users.edit', ['id' => (int) $u['id']])) ?>">Edit</a>
                            <?php if ((int) $u['id'] !== $currentUserId): ?>
                                <form method="post" action="<?= e($this->url('users.delete', ['id' => (int) $u['id']])) ?>" class="d-inline"
                                      data-wcp-confirm="Delete the panel user &quot;<?= e($u['username']) ?>&quot;? They will no longer be able to sign in. This cannot be undone."
                                      data-wcp-confirm-word="<?= e($u['username']) ?>">
                                    <?= $this->csrfField() ?>
                                    <button class="btn btn-sm btn-outline-danger" type="submit">Delete</button>
                                </form>
                            <?php endif; ?>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>

<div class="card">
    <div class="card-header d-flex justify-content-between align-items-center"><span><i class="bi bi-person-badge me-1"></i>Roles</span>
        <?php if ($this->can(\WhaCP\Security\Permissions::ROLES_MANAGE)): ?><a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('roles.create')) ?>"><i class="bi bi-plus"></i> New role</a><?php endif; ?></div>
    <div class="table-responsive">
        <table class="table mb-0 wcp-table">
            <thead><tr><th>Role</th><th>Description</th><th class="text-end">Users</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($roles as $r): ?>
                <tr>
                    <td class="fw-semibold"><?= e($r['label']) ?><?php if ((int) $r['is_system'] === 1): ?> <span class="badge text-bg-light border">built-in</span><?php endif; ?></td>
                    <td class="small"><?= e($r['description']) ?></td>
                    <td class="text-end"><?= (int) $r['user_count'] ?></td>
                    <td class="text-end text-nowrap">
                        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('roles.edit', ['id' => (int) $r['id']])) ?>"><?= (int) $r['is_system'] === 1 ? 'View' : 'Edit' ?></a>
                        <?php if ((int) $r['is_system'] === 0 && $this->can(\WhaCP\Security\Permissions::ROLES_MANAGE)): ?>
                            <form method="post" action="<?= e($this->url('roles.delete', ['id' => (int) $r['id']])) ?>" class="d-inline" data-wcp-confirm="Delete the role &quot;<?= e($r['label']) ?>&quot;?">
                                <?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" type="submit">Delete</button>
                            </form>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
