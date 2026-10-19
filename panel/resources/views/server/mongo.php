<?php
/**
 * Server → MongoDB: databases and users.
 *
 * @var \WhaCP\View\View $this
 * @var array{installed: bool, running: bool, version: string, auth: bool}|null $status
 * @var array{databases: list<array<string, mixed>>, users: list<array<string, mixed>>} $overview
 * @var array{kind: string, name: string, url: string, version: string, supported: bool, enabled: bool, note: string}|null $repository
 */
use WhaCP\Security\Permissions as P;

$status ??= null;
$overview ??= ['databases' => [], 'users' => [], 'databases_error' => '', 'users_error' => ''];
$repository ??= null;
$overviewError ??= null;
$canManage = $this->can(P::SERVER_MANAGE);
$canInstall = $this->can(P::PACKAGES_MANAGE);
$canServices = $this->can(P::SERVICES_MANAGE);
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
    <?php $repoEnabled = $repository !== null && $repository['enabled']; $repoSupported = $repository !== null && $repository['supported']; ?>
    <div class="card mb-3">
        <div class="card-header d-flex align-items-center gap-2"><i class="bi bi-box-seam" aria-hidden="true"></i>Install MongoDB</div>
        <div class="card-body">
            <p class="small text-body-secondary mb-3">MongoDB is not in the base operating-system repositories, so WhaCP installs it from MongoDB Inc.'s own signed repository. First add the repository, then install MongoDB — the server is enabled and started for you. Both steps run in the background.</p>

            <ol class="list-unstyled mb-0">
                <li class="d-flex align-items-start gap-3 py-2 border-top">
                    <i class="bi <?= $repoEnabled ? 'bi-check-circle-fill text-success' : 'bi-1-circle text-body-secondary' ?> fs-5" aria-hidden="true"></i>
                    <div class="flex-grow-1">
                        <div class="fw-medium">MongoDB <?= e($repository['version'] ?? '8.0') ?> repository</div>
                        <?php if ($repository === null): ?>
                            <div class="small text-body-secondary">The repository status could not be read from the agent.</div>
                        <?php elseif ($repoEnabled): ?>
                            <div class="small text-success-emphasis">Enabled (<?= e($repository['name']) ?>).</div>
                        <?php elseif (!$repoSupported): ?>
                            <div class="small text-body-secondary"><?= e($repository['note'] !== '' ? $repository['note'] : 'Not available on this operating system.') ?></div>
                        <?php else: ?>
                            <div class="small text-body-secondary">The signing key is fetched over TLS from MongoDB's official key server and trusted for this repository only.</div>
                        <?php endif; ?>
                    </div>
                    <?php if ($canInstall && $repoSupported && !$repoEnabled): ?>
                        <form method="post" action="<?= e($this->url('mongo.repository_enable')) ?>" data-wcp-busy data-wcp-confirm="Add MongoDB Inc.'s signed repository to this server?">
                            <?= $this->csrfField() ?>
                            <button class="btn btn-sm btn-outline-primary text-nowrap"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Add repository</button>
                        </form>
                    <?php endif; ?>
                </li>
                <li class="d-flex align-items-start gap-3 py-2 border-top">
                    <i class="bi bi-2-circle text-body-secondary fs-5" aria-hidden="true"></i>
                    <div class="flex-grow-1">
                        <div class="fw-medium">Install the server</div>
                        <div class="small text-body-secondary">Installs <code>mongodb-org</code> (server, shell and tools), then enables and starts the <code>mongod</code> service.</div>
                    </div>
                    <?php if ($canInstall && $repoEnabled): ?>
                        <form method="post" action="<?= e($this->url('mongo.install')) ?>" data-wcp-busy data-wcp-confirm="Install MongoDB <?= e($repository['version'] ?? '8.0') ?> now?">
                            <?= $this->csrfField() ?>
                            <button class="btn btn-sm btn-primary text-nowrap"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Install MongoDB</button>
                        </form>
                    <?php elseif (!$canInstall): ?>
                        <span class="small text-body-secondary text-nowrap">Needs package permission</span>
                    <?php else: ?>
                        <span class="small text-body-secondary text-nowrap">Add the repository first</span>
                    <?php endif; ?>
                </li>
            </ol>
        </div>
    </div>
    <?php return; ?>
<?php endif; ?>

<div class="wcp-infobar mb-3">
    <i class="bi bi-database-check" aria-hidden="true"></i>
    <span class="me-auto">MongoDB <?= e($status['version'] !== '' ? $status['version'] : '(version unknown)') ?> · <?= $status['running'] ? 'running' : 'stopped' ?><?= $status['auth'] ? ' · authentication on' : '' ?></span>
    <?php if ($canServices && $status['running']): ?>
        <form method="post" action="<?= e($this->url('mongo.service')) ?>" data-wcp-busy data-wcp-confirm="Restart the MongoDB service?">
            <?= $this->csrfField() ?><input type="hidden" name="op" value="restart">
            <button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-clockwise me-1" aria-hidden="true"></i>Restart</button>
        </form>
    <?php endif; ?>
</div>

<?php if (!$status['running']): ?>
    <div class="card"><div class="card-body d-flex flex-wrap align-items-center gap-3">
        <span class="small text-body-secondary me-auto">The MongoDB service (<code>mongod</code>) is not running.</span>
        <?php if ($canServices): ?>
            <form method="post" action="<?= e($this->url('mongo.service')) ?>" data-wcp-busy>
                <?= $this->csrfField() ?><input type="hidden" name="op" value="start">
                <button class="btn btn-sm btn-primary"><i class="bi bi-play-fill me-1" aria-hidden="true"></i>Start MongoDB</button>
            </form>
        <?php endif; ?>
    </div></div>
    <?php return; ?>
<?php endif; ?>

<?php if ($overviewError !== null): ?>
    <div class="alert alert-warning d-flex align-items-start gap-2" role="alert">
        <i class="bi bi-exclamation-triangle-fill mt-1" aria-hidden="true"></i>
        <div>MongoDB is running, but its databases and users could not be read: <?= e($overviewError->getMessage()) ?></div>
    </div>
<?php endif; ?>
<?php foreach (['databases_error' => 'databases', 'users_error' => 'users'] as $key => $what): if (($overview[$key] ?? '') !== ''): ?>
    <div class="alert alert-warning small" role="alert">The <?= e($what) ?> could not be listed: <?= e((string) $overview[$key]) ?></div>
<?php endif; endforeach; ?>

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

<?php if ($status !== null && !empty($status['running'])): ?>
<div class="card mt-3" id="mongo-browser"
     data-browse-url="<?= e($this->url('mongo.browse')) ?>"
     data-can-delete="<?= $canManage ? '1' : '0' ?>">
    <div class="card-header"><i class="bi bi-table me-1" aria-hidden="true"></i>Database browser</div>
    <div class="card-body">
        <div class="row g-2 align-items-end">
            <div class="col-sm-4">
                <label class="form-label small mb-1" for="mb-db">Database</label>
                <select class="form-select form-select-sm" id="mb-db" data-mb="db">
                    <option value="">Choose a database…</option>
                    <?php foreach ($overview['databases'] as $d): ?><option value="<?= e((string) ($d['name'] ?? '')) ?>"><?= e((string) ($d['name'] ?? '')) ?></option><?php endforeach; ?>
                </select>
            </div>
            <div class="col-sm-4">
                <label class="form-label small mb-1" for="mb-coll">Collection</label>
                <select class="form-select form-select-sm" id="mb-coll" data-mb="collection" disabled></select>
            </div>
            <div class="col-sm-4">
                <label class="form-label small mb-1" for="mb-filter">Filter (JSON)</label>
                <input class="form-control form-control-sm font-monospace" id="mb-filter" data-mb="filter" placeholder='{ "field": "value" }' autocomplete="off">
            </div>
        </div>
        <div class="d-flex flex-wrap align-items-center gap-2 mt-2">
            <button class="btn btn-sm btn-primary" type="button" data-mb-run><i class="bi bi-search me-1" aria-hidden="true"></i>Run</button>
            <div class="btn-group">
                <button class="btn btn-sm btn-outline-secondary" type="button" data-mb-prev disabled>Prev</button>
                <button class="btn btn-sm btn-outline-secondary" type="button" data-mb-next disabled>Next</button>
            </div>
            <span class="small text-body-secondary" data-mb-status></span>
        </div>
        <div class="mt-3" data-mb-results></div>
    </div>
</div>
<script src="<?= e($this->asset('assets/js/mongobrowser.js')) ?>"></script>
<?php endif; ?>

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
<?php endif; ?>
