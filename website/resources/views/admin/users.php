<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $users */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Users');
?>
<div class="app-top"><div><h1>Users</h1><p class="muted mb-0">New administrators are created on the server with <code>bin/site user:create</code>.</p></div><a class="btn btn-outline btn-sm" href="/admin/reports/users.csv">Download CSV</a></div>
<div class="table-wrap"><table class="data"><thead><tr><th>Name</th><th>Email</th><th>Role</th><th>Status</th><th>Joined</th><th>Last sign-in</th><th></th></tr></thead><tbody>
    <?php foreach ($users as $u): ?>
        <tr>
            <td><?= e($u['name']) ?></td><td><?= e($u['email']) ?></td>
            <td><span class="badge <?= $u['role'] === 'admin' ? 'badge-new' : '' ?>"><?= e($u['role']) ?></span></td>
            <td><span class="badge <?= (int) $u['is_active'] === 1 ? 'badge-ok' : 'badge-off' ?>"><?= (int) $u['is_active'] === 1 ? 'active' : 'deactivated' ?></span></td>
            <td><?= e(substr((string) $u['created_at'], 0, 10)) ?></td><td><?= e($u['last_login_at'] === null ? 'never' : substr((string) $u['last_login_at'], 0, 16)) ?></td>
            <td class="cols-actions">
                <form class="inline-form" method="post" action="/admin/users/<?= (int) $u['id'] ?>/active"<?= (int) $u['is_active'] === 1 ? ' data-confirm="Deactivate ' . e($u['email']) . '? They will be signed out and cannot log in."' : '' ?>>
                    <?= $v->csrf() ?><input type="hidden" name="active" value="<?= (int) $u['is_active'] === 1 ? '0' : '1' ?>">
                    <button class="btn btn-sm <?= (int) $u['is_active'] === 1 ? 'btn-danger' : 'btn-outline' ?>" type="submit"><?= (int) $u['is_active'] === 1 ? 'Deactivate' : 'Activate' ?></button>
                </form>
            </td>
        </tr>
    <?php endforeach; ?>
</tbody></table></div>
