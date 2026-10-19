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
$backups ??= [];
$canManage = $this->can(P::MONGO_MANAGE);
$canInstall = $this->can(P::PACKAGES_MANAGE);
$canServices = $this->can(P::SERVICES_MANAGE);
$roles = ['readWrite' => 'Read & write', 'read' => 'Read only', 'dbAdmin' => 'DB admin', 'dbOwner' => 'Owner'];
?>
<?php $this->start(); ?>MongoDB<?php $this->end('title'); ?>
<?php $this->start(); ?>Databases and users on the MongoDB server<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if ($canManage && $status !== null && $status['installed'] && $status['running']): ?>
        <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-admin"><i class="bi bi-shield-lock me-1" aria-hidden="true"></i>Admin password</button>
        <?php if ($status['auth']): ?>
            <form class="d-inline" method="post" action="<?= e($this->url('mongo.auth')) ?>" data-wcp-busy data-wcp-confirm="Turn OFF MongoDB authentication? Anyone who can reach the server will be able to connect without a password. MongoDB restarts.">
                <?= $this->csrfField() ?><input type="hidden" name="enable" value="0">
                <button class="btn btn-sm btn-outline-warning"><i class="bi bi-unlock me-1" aria-hidden="true"></i>Disable auth</button>
            </form>
        <?php else: ?>
            <form class="d-inline" method="post" action="<?= e($this->url('mongo.auth')) ?>" data-wcp-busy data-wcp-confirm="Turn ON MongoDB authentication? Set an admin password first, or this is refused. MongoDB restarts and clients must then authenticate.">
                <?= $this->csrfField() ?><input type="hidden" name="enable" value="1">
                <button class="btn btn-sm btn-outline-secondary"><i class="bi bi-shield-check me-1" aria-hidden="true"></i>Enable auth</button>
            </form>
        <?php endif; ?>
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
        <div class="d-flex gap-2">
            <form method="post" action="<?= e($this->url('mongo.service')) ?>" data-wcp-busy data-wcp-confirm="Restart the MongoDB service?">
                <?= $this->csrfField() ?><input type="hidden" name="op" value="restart">
                <button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-clockwise me-1" aria-hidden="true"></i>Restart</button>
            </form>
            <form method="post" action="<?= e($this->url('mongo.service')) ?>" data-wcp-busy data-wcp-confirm="Stop the MongoDB service? Applications using it will lose their connection.">
                <?= $this->csrfField() ?><input type="hidden" name="op" value="stop">
                <button class="btn btn-sm btn-outline-danger"><i class="bi bi-stop-fill me-1" aria-hidden="true"></i>Stop</button>
            </form>
        </div>
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
        <?php
        // Count backup archives per database so each row can show its status.
        $backupCounts = [];
        foreach ($backups as $b) {
            $backupCounts[(string) $b['db']] = ($backupCounts[(string) $b['db']] ?? 0) + 1;
        }
        ?>
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Name</th><th>Size</th><th>Backup</th><th class="text-end">Action</th></tr></thead>
                <tbody>
                <?php foreach ($overview['databases'] as $d): $dn = (string) ($d['name'] ?? ''); $bc = $backupCounts[$dn] ?? 0; ?>
                    <tr>
                        <td class="font-monospace"><?= e($dn) ?></td>
                        <td class="small text-body-secondary"><?= e(format_bytes((int) ($d['size'] ?? 0))) ?></td>
                        <td class="small">
                            <?php if ($bc > 0): ?>
                                <span class="text-success-emphasis"><i class="bi bi-check-circle me-1"></i><?= (int) $bc ?> backup<?= $bc === 1 ? '' : 's' ?></span>
                            <?php elseif ($canManage): ?>
                                <form class="d-inline" method="post" action="<?= e($this->url('mongo.backup')) ?>" data-wcp-busy data-wcp-confirm="Back up “<?= e($dn) ?>” now?">
                                    <?= $this->csrfField() ?><input type="hidden" name="db" value="<?= e($dn) ?>">
                                    <button class="btn btn-sm btn-link p-0 text-decoration-none" title="Back up now"><i class="bi bi-download me-1"></i>Back up</button>
                                </form>
                            <?php else: ?>
                                <span class="text-body-secondary">None</span>
                            <?php endif; ?>
                        </td>
                        <td class="text-end text-nowrap">
                            <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('mongo.browser')) ?>?db=<?= e(rawurlencode($dn)) ?>" title="Browse collections and documents"><i class="bi bi-table me-1"></i>Browse</a>
                            <?php if ($canManage): ?>
                            <form class="d-inline" method="post" action="<?= e($this->url('mongo.database_drop')) ?>" data-wcp-confirm="Drop the database “<?= e($dn) ?>” and everything in it? This cannot be undone.">
                                <?= $this->csrfField() ?>
                                <input type="hidden" name="name" value="<?= e($dn) ?>">
                                <input type="hidden" name="confirm" value="<?= e($dn) ?>">
                                <button class="btn btn-sm btn-outline-danger" title="Drop"><i class="bi bi-trash"></i></button>
                            </form>
                            <?php endif; ?>
                        </td>
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

<?php if ($status && ($status['installed'] ?? false) && ($status['running'] ?? false)): ?>
<div class="card mt-3">
    <div class="card-header"><i class="bi bi-archive me-1" aria-hidden="true"></i>Backups</div>
    <div class="card-body">
        <p class="small text-body-secondary">Backups are gzip <code>mongodump</code> archives kept on this server. Backing up or restoring a large database runs in the background.</p>
        <?php if ($canManage && $overview['databases'] !== []): ?>
        <form method="post" action="<?= e($this->url('mongo.backup')) ?>" class="row g-2 align-items-end mb-3" data-wcp-busy>
            <?= $this->csrfField() ?>
            <div class="col-sm-6">
                <label class="form-label small mb-1" for="mongo-backup-db">Back up a database</label>
                <select class="form-select form-select-sm" id="mongo-backup-db" name="db" required>
                    <?php foreach ($overview['databases'] as $d): $dn = (string) ($d['name'] ?? ''); ?>
                        <option value="<?= e($dn) ?>"><?= e($dn) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
            <div class="col-sm-auto"><button class="btn btn-sm btn-primary"><i class="bi bi-download me-1"></i>Back up now</button></div>
        </form>
        <?php endif; ?>
        <?php if ($backups === []): ?>
            <div class="small text-body-secondary">No backups yet.</div>
        <?php else: ?>
            <div class="wcp-dtable table-responsive">
                <table class="table align-middle mb-0">
                    <thead><tr><th>Archive</th><th>Database</th><th>Size</th><th>Created (UTC)</th><?php if ($canManage): ?><th class="text-end">Action</th><?php endif; ?></tr></thead>
                    <tbody>
                    <?php foreach ($backups as $b): $bf = (string) $b['file']; $bdb = (string) $b['db']; ?>
                        <tr>
                            <td class="font-monospace small text-truncate" style="max-width:20rem"><?= e($bf) ?></td>
                            <td class="small font-monospace"><?= e($bdb) ?></td>
                            <td class="small text-body-secondary"><?= e(format_bytes((int) $b['size'])) ?></td>
                            <td class="small text-body-secondary"><?= e(((string) $b['created']) !== '' ? str_replace(['T', 'Z'], [' ', ''], substr((string) $b['created'], 0, 19)) : '—') ?></td>
                            <?php if ($canManage): ?>
                            <td class="text-end text-nowrap">
                                <form class="d-inline" method="post" action="<?= e($this->url('mongo.restore')) ?>" data-wcp-busy data-wcp-confirm="Restore “<?= e($bdb) ?>” from this backup? Documents sharing an _id with the archive are overwritten.">
                                    <?= $this->csrfField() ?>
                                    <input type="hidden" name="file" value="<?= e($bf) ?>">
                                    <input type="hidden" name="db" value="<?= e($bdb) ?>">
                                    <input type="hidden" name="drop" value="0">
                                    <button class="btn btn-sm btn-outline-secondary" title="Restore"><i class="bi bi-arrow-counterclockwise me-1"></i>Restore</button>
                                </form>
                                <form class="d-inline" method="post" action="<?= e($this->url('mongo.backup_delete')) ?>" data-wcp-confirm="Delete this backup archive? This cannot be undone.">
                                    <?= $this->csrfField() ?>
                                    <input type="hidden" name="file" value="<?= e($bf) ?>">
                                    <button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button>
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
</div>
<?php endif; ?>

<?php if (($config ?? null) !== null): ?>
<div class="card mt-3">
    <div class="card-header"><i class="bi bi-sliders2 me-1" aria-hidden="true"></i>Configuration</div>
    <div class="card-body">
        <?php if ($canManage): ?>
        <form method="post" action="<?= e($this->url('mongo.config')) ?>" data-wcp-busy data-wcp-confirm="Save the MongoDB configuration and restart the service? The previous configuration is restored automatically if it fails to start.">
            <?= $this->csrfField() ?>
            <div class="row g-3">
                <div class="col-sm-3">
                    <label class="form-label small mb-1" for="cfg-port">Port</label>
                    <input class="form-control form-control-sm" type="number" id="cfg-port" name="port" min="1" max="65535" value="<?= e((string) $config['port']) ?>">
                </div>
                <div class="col-sm-5">
                    <label class="form-label small mb-1" for="cfg-bind">Bind IP</label>
                    <input class="form-control form-control-sm font-monospace" id="cfg-bind" name="bind_ip" value="<?= e($config['bind_ip']) ?>" placeholder="127.0.0.1">
                    <div class="form-text">Comma-separated. Keep <code>127.0.0.1</code> unless remote access is intended.</div>
                </div>
                <div class="col-sm-4">
                    <label class="form-label small mb-1 d-block">Authentication</label>
                    <div class="form-check form-switch">
                        <input class="form-check-input" type="checkbox" role="switch" id="cfg-auth" name="authorization" value="1"<?= $config['authorization'] ? ' checked' : '' ?>>
                        <label class="form-check-label small" for="cfg-auth">Require authentication (<code>security.authorization</code>)</label>
                    </div>
                </div>
            </div>
            <button class="btn btn-sm btn-primary mt-3" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save &amp; restart</button>
        </form>
        <?php else: ?>
        <dl class="row small mb-0">
            <dt class="col-sm-3">Port</dt><dd class="col-sm-9 font-monospace"><?= e((string) $config['port']) ?></dd>
            <dt class="col-sm-3">Bind IP</dt><dd class="col-sm-9 font-monospace"><?= e($config['bind_ip']) ?></dd>
            <dt class="col-sm-3">Authentication</dt><dd class="col-sm-9"><?= $config['authorization'] ? 'Required' : 'Disabled' ?></dd>
        </dl>
        <?php endif; ?>
        <dl class="row small mt-3 mb-0 text-body-secondary">
            <dt class="col-sm-3">Data path</dt><dd class="col-sm-9 font-monospace"><?= e($config['db_path'] !== '' ? $config['db_path'] : '—') ?></dd>
            <dt class="col-sm-3">Config file</dt><dd class="col-sm-9 font-monospace"><?= e($config['path']) ?></dd>
        </dl>
        <?php if (trim($config['raw']) !== ''): ?>
        <details class="mt-2">
            <summary class="small">View configuration file</summary>
            <pre class="small border rounded p-2 mt-2 mb-0 wcp-mongo-json"><?= e($config['raw']) ?></pre>
        </details>
        <?php endif; ?>
    </div>
</div>
<?php endif; ?>

<?php if (($log ?? null) !== null): ?>
<div class="card mt-3">
    <div class="card-header d-flex align-items-center">
        <span class="me-auto"><i class="bi bi-journal-text me-1" aria-hidden="true"></i>Log</span>
        <span class="small text-body-secondary font-monospace"><?= e($log['path']) ?></span>
    </div>
    <div class="card-body">
        <?php if (trim($log['lines']) === ''): ?>
            <div class="small text-body-secondary">The log is empty.</div>
        <?php else: ?>
            <pre class="small border rounded p-2 mb-0 wcp-mongo-json"><?= e($log['lines']) ?></pre>
        <?php endif; ?>
        <a class="btn btn-sm btn-outline-secondary mt-2" href="<?= e($this->url('mongo')) ?>"><i class="bi bi-arrow-clockwise me-1" aria-hidden="true"></i>Refresh</a>
    </div>
</div>
<?php endif; ?>

<?php if ($canManage): ?>
<?= $this->modalStart('mongo-admin', 'MongoDB admin password') ?>
    <form method="post" action="<?= e($this->url('mongo.admin_password')) ?>" data-wcp-busy>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <p class="small text-body-secondary">Sets the password of WhaCP's MongoDB administrator (<code>whacpAdmin</code>, in the <code>admin</code> database). WhaCP uses this account to manage the server once authentication is on. Set it <strong>before</strong> enabling authentication.</p>
            <label class="form-label" for="madm-pw">New admin password</label>
            <input class="form-control font-monospace" id="madm-pw" name="password" type="text" minlength="8" maxlength="256" required autocomplete="off" spellcheck="false">
            <div class="form-text">8 to 256 characters. Store it somewhere safe — WhaCP keeps only the server-side connection string, not a copy it shows you later.</div>
            <button class="btn btn-primary btn-sm mt-3" type="submit">Save admin password</button>
        </div>
    </form>
<?= $this->modalEnd() ?>

<?= $this->modalStart('mongo-db', 'Add database') ?>
    <form method="post" action="<?= e($this->url('mongo.database_create')) ?>" data-wcp-busy>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <label class="form-label" for="mdb-name">Name</label>
            <input class="form-control" id="mdb-name" name="name" pattern="[A-Za-z0-9_][A-Za-z0-9_-]*" maxlength="63" required placeholder="shop">
            <div class="form-text">Letters, digits, _ and -. The database exists once it has a collection; WhaCP adds an empty one.</div>
            <hr class="my-3">
            <div class="form-check form-switch mb-2">
                <input class="form-check-input" type="checkbox" role="switch" id="mdb-withuser" data-mongo-dbuser-toggle>
                <label class="form-check-label" for="mdb-withuser">Also create a user for this database</label>
            </div>
            <div class="row g-2 d-none" data-mongo-dbuser-fields>
                <div class="col-6"><label class="form-label" for="mdb-user">User</label><input class="form-control" id="mdb-user" name="user" maxlength="63" autocomplete="off"></div>
                <div class="col-6">
                    <label class="form-label" for="mdb-role">Role</label>
                    <select class="form-select" id="mdb-role" name="role"><?php foreach ($roles as $k => $label): ?><option value="<?= e($k) ?>"<?= $k === 'readWrite' ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?></select>
                </div>
                <div class="col-12"><label class="form-label" for="mdb-pw">Password</label><input class="form-control font-monospace" id="mdb-pw" name="password" type="text" minlength="8" maxlength="256" autocomplete="off"></div>
                <div class="col-12 form-text">The user is created in the new database with that role.</div>
            </div>
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
