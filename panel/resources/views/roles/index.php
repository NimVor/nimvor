<?php
/**
 * @var \NimVor\View\View $this
 * @var list<array<string, mixed>> $roles
 */
?>
<?php $this->start(); ?>Roles<?php $this->end('title'); ?>
<?php $this->start(); ?>What each kind of user may do<?php $this->end('subtitle'); ?>
<div class="card">
    <div class="card-header d-flex justify-content-between align-items-center"><span><i class="bi bi-person-badge me-1"></i>Roles</span>
        <?php if ($this->can(\NimVor\Security\Permissions::ROLES_MANAGE)): ?><a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('roles.create')) ?>"><i class="bi bi-plus"></i> New role</a><?php endif; ?></div>
    <div class="table-responsive">
        <table class="table mb-0 nim-table">
            <thead><tr><th>Role</th><th>Description</th><th class="text-end">Users</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($roles as $r): ?>
                <tr>
                    <td class="fw-semibold"><?= e($r['label']) ?><?php if ((int) $r['is_system'] === 1): ?> <span class="badge text-bg-light border">built-in</span><?php endif; ?><?php if (!empty($r['customized'])): ?> <span class="badge bg-warning-subtle text-warning-emphasis border">changed</span><?php endif; ?></td>
                    <td class="small"><?= e($r['description']) ?></td>
                    <td class="text-end"><?= (int) $r['user_count'] ?></td>
                    <td class="text-end text-nowrap">
                        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('roles.edit', ['id' => (int) $r['id']])) ?>"><?= $r['name'] === 'admin' || !$this->can(\NimVor\Security\Permissions::ROLES_MANAGE) ? 'View' : 'Edit' ?></a>
                        <?php if ((int) $r['is_system'] === 0 && $this->can(\NimVor\Security\Permissions::ROLES_MANAGE)): ?>
                            <form method="post" action="<?= e($this->url('roles.delete', ['id' => (int) $r['id']])) ?>" class="d-inline" data-nim-confirm="Delete the role &quot;<?= e($r['label']) ?>&quot;?">
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
