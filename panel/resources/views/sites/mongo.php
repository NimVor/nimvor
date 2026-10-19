<?php
/**
 * Site → MongoDB: a customer's own prefixed databases and users.
 *
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var array{installed: bool, running: bool, version: string}|null $status
 * @var array{databases: list<array<string, mixed>>, users: list<array<string, mixed>>} $overview
 * @var string $prefix
 */
$id = (int) $site['id'];
$status ??= null;
$overview ??= ['databases' => [], 'users' => []];
$roles = ['readWrite' => 'Read & write', 'read' => 'Read only', 'dbAdmin' => 'DB admin', 'dbOwner' => 'Owner'];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'mongo']) ?>

<?php if ($status === null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError ?? null]) ?>
    <?php return; ?>
<?php endif; ?>

<?php if (!$status['installed']): ?>
    <div class="card"><div class="wcp-empty-state">
        <i class="bi bi-database" aria-hidden="true"></i>
        <div class="fw-medium">MongoDB is not available on this server</div>
        <div class="small">Ask your hosting provider to enable MongoDB if your application needs it.</div>
    </div></div>
    <?php return; ?>
<?php endif; ?>

<p class="small text-body-secondary">Your MongoDB databases and users for <strong><?= e($site['domain']) ?></strong>. Names are prefixed with <code><?= e($prefix) ?></code> so they stay yours. Connect from your app at <code>mongodb://127.0.0.1:27017</code>.</p>

<div class="d-flex flex-wrap gap-2 mb-3">
    <?php if ($status['running']): ?>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-db"><i class="bi bi-plus-lg me-1"></i>Add database</button>
        <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-user"><i class="bi bi-person-plus me-1"></i>Add user</button>
    <?php endif; ?>
</div>

<?php if (!$status['running']): ?>
    <div class="card"><div class="card-body small text-body-secondary">MongoDB is installed but not running. Ask your hosting provider to start it.</div></div>
    <?php return; ?>
<?php endif; ?>

<div class="card mb-3">
    <div class="card-header">Databases</div>
    <?php if ($overview['databases'] === []): ?>
        <div class="card-body small text-body-secondary">No databases yet.</div>
    <?php else: ?>
        <div class="wcp-dtable table-responsive"><table class="table align-middle mb-0">
            <thead><tr><th>Name</th><th>Size</th><th class="text-end">Action</th></tr></thead>
            <tbody>
            <?php foreach ($overview['databases'] as $d): $dn = (string) ($d['name'] ?? ''); ?>
                <tr>
                    <td class="font-monospace"><?= e($dn) ?></td>
                    <td class="small text-body-secondary"><?= e(format_bytes((int) ($d['size'] ?? 0))) ?></td>
                    <td class="text-end text-nowrap">
                        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.mongo.browser', ['id' => $id])) ?>?db=<?= e(rawurlencode($dn)) ?>" title="Browse collections and documents"><i class="bi bi-table me-1"></i>Browse</a>
                        <form class="d-inline" method="post" action="<?= e($this->url('sites.mongo.database_drop', ['id' => $id])) ?>" data-wcp-confirm="Drop the database “<?= e($dn) ?>” and everything in it?">
                            <?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e($dn) ?>">
                            <button class="btn btn-sm btn-outline-danger" title="Drop"><i class="bi bi-trash"></i></button>
                        </form>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    <?php endif; ?>
</div>

<div class="card">
    <div class="card-header">Users</div>
    <?php if ($overview['users'] === []): ?>
        <div class="card-body small text-body-secondary">No users yet.</div>
    <?php else: ?>
        <div class="wcp-dtable table-responsive"><table class="table align-middle mb-0">
            <thead><tr><th>User</th><th>Database</th><th>Roles</th><th class="text-end">Action</th></tr></thead>
            <tbody>
            <?php foreach ($overview['users'] as $u): ?>
                <tr>
                    <td class="font-monospace"><?= e((string) ($u['user'] ?? '')) ?></td>
                    <td class="small font-monospace"><?= e((string) ($u['db'] ?? '')) ?></td>
                    <td class="small text-body-secondary"><?= e(implode(', ', array_map('strval', (array) ($u['roles'] ?? [])))) ?></td>
                    <td class="text-end text-nowrap">
                        <button class="btn btn-sm btn-outline-secondary" title="Change password" data-bs-toggle="modal" data-bs-target="#mongo-pw" data-user="<?= e((string) ($u['user'] ?? '')) ?>" data-db="<?= e((string) ($u['db'] ?? '')) ?>"><i class="bi bi-key"></i></button>
                        <form class="d-inline" method="post" action="<?= e($this->url('sites.mongo.user_drop', ['id' => $id])) ?>" data-wcp-confirm="Drop the user “<?= e((string) ($u['user'] ?? '')) ?>”?">
                            <?= $this->csrfField() ?><input type="hidden" name="user" value="<?= e((string) ($u['user'] ?? '')) ?>"><input type="hidden" name="db" value="<?= e((string) ($u['db'] ?? '')) ?>">
                            <button class="btn btn-sm btn-outline-danger" title="Drop"><i class="bi bi-trash"></i></button>
                        </form>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    <?php endif; ?>
</div>

<?= $this->modalStart('mongo-db', 'Add database') ?>
    <form method="post" action="<?= e($this->url('sites.mongo.database_create', ['id' => $id])) ?>" data-wcp-busy>
        <div class="p-3"><?= $this->csrfField() ?>
            <label class="form-label" for="mdb-name">Name</label>
            <div class="input-group"><span class="input-group-text font-monospace"><?= e($prefix) ?></span><input class="form-control" id="mdb-name" name="name" pattern="[A-Za-z0-9_][A-Za-z0-9_-]*" maxlength="48" required placeholder="shop"></div>
            <div class="form-text">Letters, digits, _ and -. The name is prefixed with <code><?= e($prefix) ?></code>.</div>
            <button class="btn btn-primary btn-sm mt-3" type="submit">Create</button>
        </div>
    </form>
<?= $this->modalEnd() ?>

<?= $this->modalStart('mongo-user', 'Add user') ?>
    <form method="post" action="<?= e($this->url('sites.mongo.user_create', ['id' => $id])) ?>" data-wcp-busy>
        <div class="p-3"><?= $this->csrfField() ?>
            <div class="row g-2">
                <div class="col-6"><label class="form-label" for="mu-user">User</label><div class="input-group"><span class="input-group-text font-monospace"><?= e($prefix) ?></span><input class="form-control" id="mu-user" name="user" maxlength="48" required></div></div>
                <div class="col-6"><label class="form-label" for="mu-db">Database</label>
                    <select class="form-select" id="mu-db" name="db" required>
                        <?php foreach ($overview['databases'] as $d): ?><option value="<?= e((string) ($d['name'] ?? '')) ?>"><?= e((string) ($d['name'] ?? '')) ?></option><?php endforeach; ?>
                    </select>
                </div>
            </div>
            <div class="row g-2 mt-1">
                <div class="col-6"><label class="form-label" for="mu-pw">Password</label><input class="form-control" id="mu-pw" name="password" type="password" minlength="8" maxlength="256" required></div>
                <div class="col-6"><label class="form-label" for="mu-role">Role</label><select class="form-select" id="mu-role" name="role"><?php foreach ($roles as $k => $label): ?><option value="<?= e($k) ?>"<?= $k === 'readWrite' ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?></select></div>
            </div>
            <div class="form-text mt-1">The user is created in the chosen database with that role. Its name is prefixed with <code><?= e($prefix) ?></code>.</div>
            <button class="btn btn-primary btn-sm mt-3" type="submit">Create user</button>
        </div>
    </form>
<?= $this->modalEnd() ?>

<?= $this->modalStart('mongo-pw', 'Change password') ?>
    <form method="post" action="<?= e($this->url('sites.mongo.user_password', ['id' => $id])) ?>" data-wcp-busy>
        <div class="p-3"><?= $this->csrfField() ?>
            <input type="hidden" name="user" id="mpw-user"><input type="hidden" name="db" id="mpw-db">
            <label class="form-label" for="mpw-pw">New password</label>
            <input class="form-control" id="mpw-pw" name="password" type="password" minlength="8" maxlength="256" required>
            <button class="btn btn-primary btn-sm mt-3" type="submit">Change password</button>
        </div>
    </form>
<?= $this->modalEnd() ?>
