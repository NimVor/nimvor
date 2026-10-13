<?php
/**
 * Server → MongoDB: databases and users.
 *
 * @var \WhaCP\View\View $this
 * @var array{installed: bool, running: bool, version: string, auth: bool}|null $status
 * @var array{databases: list<array<string, mixed>>, users: list<array<string, mixed>>} $overview
 */
use WhaCP\Security\Permissions as P;

$status ??= null;
$overview ??= ['databases' => [], 'users' => []];
$canManage = $this->can(P::SERVER_MANAGE);
$roles = ['readWrite' => 'Read & write', 'read' => 'Read only', 'dbAdmin' => 'DB admin', 'dbOwner' => 'Owner'];
?>
<?php $this->start(); ?>MongoDB<?php $this->end('title'); ?>
<?php $this->start(); ?>Databases and users on the MongoDB server<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if ($canManage && $status !== null && $status['installed'] && $status['running']): ?>
        <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-user"><i class="bi bi-person-plus me-1" aria-hidden="true"></i>Add user</button>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-db"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Add database</button>
    <?php endif; ?>
<?php $this->end('actions'); ?>

<?php if ($status === null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError ?? null]) ?>
    <?php return; ?>
<?php endif; ?>

<?php if (!$status['installed']): ?>
    <div class="card"><div class="wcp-empty-state">
        <i class="bi bi-database" aria-hidden="true"></i>
        <div class="fw-medium">MongoDB is not installed</div>
        <div class="small mb-3">Install the <code>mongodb-org</code> packages under Packages, then manage its databases and users here.</div>
        <?php if ($this->can(P::PACKAGES_MANAGE) && $this->hasRoute('packages')): ?><a class="btn btn-primary" href="<?= e($this->url('packages')) ?>">Open Packages</a><?php endif; ?>
    </div></div>
    <?php return; ?>
<?php endif; ?>

<div class="wcp-infobar mb-3">
    <i class="bi bi-database-check" aria-hidden="true"></i>
    <span>MongoDB <?= e($status['version'] !== '' ? $status['version'] : '(version unknown)') ?> · <?= $status['running'] ? 'running' : 'stopped' ?><?= $status['auth'] ? ' · authentication on' : '' ?></span>
</div>

<?php if (!$status['running']): ?>
    <div class="card"><div class="card-body small text-body-secondary">The MongoDB service is not running. Start it under Services, then reload this page.</div></div>
    <?php return; ?>
<?php endif; ?>

<div class="card mb-3">
    <div class="card-header">Databases</div>
    <?php if ($overview['databases'] === []): ?>
        <div class="card-body small text-body-secondary">No databases yet.</div>
    <?php else: ?>
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Name</th><th>Size</th><?php if ($canManage): ?><th class="text-end">Action</th><?php endif; ?></tr></thead>
                <tbody>
                <?php foreach ($overview['databases'] as $d): ?>
                    <tr>
                        <td class="font-monospace"><?= e((string) ($d['name'] ?? '')) ?></td>
                        <td class="small text-body-secondary"><?= e(format_bytes((int) ($d['size'] ?? 0))) ?></td>
                        <?php if ($canManage): ?>
                        <td class="text-end">
                            <form method="post" action="<?= e($this->url('mongo.database_drop')) ?>" data-wcp-confirm="Drop the database “<?= e((string) ($d['name'] ?? '')) ?>” and everything in it? This cannot be undone.">
                                <?= $this->csrfField() ?>
                                <input type="hidden" name="name" value="<?= e((string) ($d['name'] ?? '')) ?>">
                                <input type="hidden" name="confirm" value="<?= e((string) ($d['name'] ?? '')) ?>">
                                <button class="btn btn-sm btn-outline-danger" title="Drop"><i class="bi bi-trash"></i></button>
                            </form>
                        </td>
                        <?php endif; ?>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>

<div class="card">
    <div class="card-header">Users</div>
    <?php if ($overview['users'] === []): ?>
        <div class="card-body small text-body-secondary">No users yet.</div>
    <?php else: ?>
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>User</th><th>Database</th><th>Roles</th><?php if ($canManage): ?><th class="text-end">Action</th><?php endif; ?></tr></thead>
                <tbody>
                <?php foreach ($overview['users'] as $u): ?>
                    <tr>
                        <td class="font-monospace"><?= e((string) ($u['user'] ?? '')) ?></td>
                        <td class="small font-monospace"><?= e((string) ($u['db'] ?? '')) ?></td>
                        <td class="small text-body-secondary"><?= e(implode(', ', array_map('strval', (array) ($u['roles'] ?? [])))) ?></td>
                        <?php if ($canManage): ?>
                        <td class="text-end text-nowrap">
                            <button class="btn btn-sm btn-outline-secondary" title="Change password" data-bs-toggle="modal" data-bs-target="#mongo-pw" data-user="<?= e((string) ($u['user'] ?? '')) ?>" data-db="<?= e((string) ($u['db'] ?? '')) ?>"><i class="bi bi-key"></i></button>
                            <form class="d-inline" method="post" action="<?= e($this->url('mongo.user_drop')) ?>" data-wcp-confirm="Drop the user “<?= e((string) ($u['user'] ?? '')) ?>”?">
                                <?= $this->csrfField() ?>
                                <input type="hidden" name="user" value="<?= e((string) ($u['user'] ?? '')) ?>">
                                <input type="hidden" name="db" value="<?= e((string) ($u['db'] ?? '')) ?>">
                                <button class="btn btn-sm btn-outline-danger" title="Drop"><i class="bi bi-trash"></i></button>
                            </form>
                        </td>
                        <?php endif; ?>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>

<?php if ($canManage): ?>
<?= $this->modalStart('mongo-db', 'Add database') ?>
    <form method="post" action="<?= e($this->url('mongo.database_create')) ?>" data-wcp-busy>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <label class="form-label" for="mdb-name">Name</label>
            <input class="form-control" id="mdb-name" name="name" pattern="[A-Za-z0-9_][A-Za-z0-9_-]*" maxlength="63" required placeholder="shop">
            <div class="form-text">Letters, digits, _ and -. The database exists once it has a collection; WhaCP adds an empty one.</div>
            <button class="btn btn-primary btn-sm mt-3" type="submit">Create</button>
        </div>
    </form>
<?= $this->modalEnd() ?>

<?= $this->modalStart('mongo-user', 'Add user') ?>
    <form method="post" action="<?= e($this->url('mongo.user_create')) ?>" data-wcp-busy>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <div class="row g-2">
                <div class="col-6"><label class="form-label" for="mu-user">User</label><input class="form-control" id="mu-user" name="user" maxlength="63" required></div>
                <div class="col-6"><label class="form-label" for="mu-db">Database</label><input class="form-control" id="mu-db" name="db" maxlength="63" required placeholder="shop"></div>
            </div>
            <div class="row g-2 mt-1">
                <div class="col-6"><label class="form-label" for="mu-pw">Password</label><input class="form-control" id="mu-pw" name="password" type="password" minlength="8" maxlength="256" required></div>
                <div class="col-6">
                    <label class="form-label" for="mu-role">Role</label>
                    <select class="form-select" id="mu-role" name="role">
                        <?php foreach ($roles as $k => $label): ?><option value="<?= e($k) ?>"<?= $k === 'readWrite' ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                    </select>
                </div>
            </div>
            <div class="form-text mt-1">The user is created in the chosen database with that role on it.</div>
            <button class="btn btn-primary btn-sm mt-3" type="submit">Create user</button>
        </div>
    </form>
<?= $this->modalEnd() ?>

<?= $this->modalStart('mongo-pw', 'Change password') ?>
    <form method="post" action="<?= e($this->url('mongo.user_password')) ?>" data-wcp-busy>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <input type="hidden" name="user" id="mpw-user">
            <input type="hidden" name="db" id="mpw-db">
            <label class="form-label" for="mpw-pw">New password</label>
            <input class="form-control" id="mpw-pw" name="password" type="password" minlength="8" maxlength="256" required>
            <button class="btn btn-primary btn-sm mt-3" type="submit">Change password</button>
        </div>
    </form>
<?= $this->modalEnd() ?>
<script>
document.getElementById('mongo-pw')?.addEventListener('show.bs.modal', function (e) {
    var b = e.relatedTarget; if (!b) return;
    this.querySelector('#mpw-user').value = b.getAttribute('data-user') || '';
    this.querySelector('#mpw-db').value = b.getAttribute('data-db') || '';
});
</script>
<?php endif; ?>
