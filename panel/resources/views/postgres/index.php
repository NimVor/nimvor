<?php
/**
 * PostgreSQL: databases, users and access for the signed-in account; the server's own controls for those who
 * may manage it.
 *
 * @var \NimVor\View\View $this
 * @var array{installed: bool, running: bool, managed: bool, version: string, port: int}|null $status
 * @var array<string, mixed>|null $live
 * @var list<array<string, mixed>> $databases
 * @var list<array<string, mixed>> $roles
 * @var list<array<string, mixed>> $unmanaged
 * @var list<array<string, mixed>> $sites
 * @var string $prefix
 * @var bool $canServer
 * @var bool $isAdmin
 */
use NimVor\Security\Permissions as P;
use NimVor\Services\PostgresService;

$status ??= null;
$databases ??= [];
$roles ??= [];
$unmanaged ??= [];
$sites ??= [];
$servers ??= [];
$remoteServers ??= [];
$sslModes ??= [];
$isAdmin ??= false;
$hasRemote = $remoteServers !== [];
$canManage = $this->can(P::DATABASES_MANAGE);
$levels = PostgresService::LEVELS;
?>
<?php $this->start(); ?>PostgreSQL<?php $this->end('title'); ?>
<?php $this->start(); ?>Databases, users and access<?php $this->end('subtitle'); ?>

<?php if ($status === null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError ?? null]) ?>
    <?php return; ?>
<?php endif; ?>

<?php if (!$status['installed']): ?>
    <div class="card"><div class="card-body">
        <div class="fw-medium mb-1"><i class="bi bi-database me-1" aria-hidden="true"></i>PostgreSQL is not installed on this server</div>
        <?php if ($canServer): ?>
            <p class="small text-body-secondary mb-3">NimVor installs PostgreSQL from the operating system's packages, starts it and creates one management role that can create databases and users but is not a superuser. No existing database is touched.</p>
            <form method="post" action="<?= e($this->url('postgres.install')) ?>" data-nim-busy data-nim-confirm="Install PostgreSQL on this server?">
                <?= $this->csrfField() ?>
                <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Install PostgreSQL</button>
            </form>
        <?php else: ?>
            <p class="small text-body-secondary mb-0">Ask your hosting provider to enable PostgreSQL if your application needs it.</p>
        <?php endif; ?>
    </div></div>
    <?php if (!$hasRemote) { return; } ?>
<?php endif; ?>

<?php if ($canServer && $status['installed']): ?>
    <div class="card mb-3"><div class="card-body d-flex flex-wrap align-items-center gap-3">
        <div>
            <span class="badge <?= $status['running'] ? 'text-bg-success' : 'text-bg-secondary' ?>"><?= $status['running'] ? 'Running' : 'Stopped' ?></span>
            <span class="ms-2 small">PostgreSQL <?= e($status['version']) ?></span>
            <?php if ($live !== null): ?>
                <span class="ms-2 small text-body-secondary"><?= (int) ($live['active_connections'] ?? 0) ?> of <?= (int) ($live['max_connections'] ?? 0) ?> connections in use</span>
            <?php endif; ?>
        </div>
        <span class="me-auto"></span>
        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.monitor')) ?>"><i class="bi bi-activity me-1"></i>Health</a>
        <?php foreach (['start' => 'Start', 'restart' => 'Restart', 'reload' => 'Reload', 'stop' => 'Stop'] as $op => $label): ?>
            <form method="post" action="<?= e($this->url('postgres.service')) ?>" class="d-inline" data-nim-busy<?= $op === 'stop' || $op === 'restart' ? ' data-nim-confirm="' . e($label . ' PostgreSQL? Connections are dropped.') . '"' : '' ?>>
                <?= $this->csrfField() ?><input type="hidden" name="op" value="<?= e($op) ?>">
                <button class="btn btn-sm btn-outline-secondary"><?= e($label) ?></button>
            </form>
        <?php endforeach; ?>
    </div></div>
<?php endif; ?>

<?php if (!$status['running']): ?>
    <div class="card mb-3"><div class="card-body small text-body-secondary">PostgreSQL is installed but not running.<?= $canServer ? ' Start it above.' : ' Ask your hosting provider to start it.' ?></div></div>
    <?php if (!$hasRemote) { return; } ?>
<?php endif; ?>

<?php if (!$status['managed']): ?>
    <div class="card"><div class="card-body">
        <div class="fw-medium mb-1">NimVor does not manage this PostgreSQL server yet</div>
        <?php if ($canServer): ?>
            <p class="small text-body-secondary">An existing server was found. Adopting it creates one role, <code>nimvor_admin</code> (no superuser rights), through the <code>postgres</code> account. No configuration file is changed and no existing database or user is touched.</p>
            <form method="post" action="<?= e($this->url('postgres.adopt')) ?>" data-nim-busy data-nim-confirm="Let NimVor manage the existing PostgreSQL server?">
                <?= $this->csrfField() ?>
                <div class="form-check mb-3">
                    <input class="form-check-input" type="checkbox" name="harden" value="1" id="pg-harden">
                    <label class="form-check-label small" for="pg-harden">Also stop database users from connecting to the <code>postgres</code> and <code>template1</code> databases (recommended for hosting; other applications that rely on that would stop working).</label>
                </div>
                <button class="btn btn-primary">Adopt this server</button>
            </form>
        <?php else: ?>
            <p class="small text-body-secondary mb-0">Ask your hosting provider to set up PostgreSQL management.</p>
        <?php endif; ?>
    </div></div>
    <?php if (!$hasRemote) { return; } ?>
<?php endif; ?>

<?php if ($live === null && $status['installed'] && $status['running'] && $status['managed']): ?>
    <div class="alert alert-warning small">PostgreSQL is running but did not answer NimVor. The lists below show NimVor's records; sizes are not available.</div>
<?php endif; ?>

<p class="small text-body-secondary">
    <?php if ($prefix !== ''): ?>Names you create start with <code><?= e($prefix) ?></code> so they stay yours. <?php endif; ?>
    <?php if ($status['installed']): ?>Connect from your application to <code>127.0.0.1:<?= (int) $status['port'] ?></code> (or the server named in the list) with the database user and password.<?php endif; ?> Each database can only be entered by its owner and by users you give access to.
</p>

<div class="card mb-3">
    <div class="card-header d-flex align-items-center gap-2">
        <span class="fw-medium"><i class="bi bi-hdd-stack me-1" aria-hidden="true"></i>Databases <span class="badge rounded-pill text-bg-light border ms-1"><?= count($databases) ?></span></span>
        <span class="me-auto"></span>
        <?php if ($canManage): ?><button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#pg-db"><i class="bi bi-plus-lg me-1"></i>Add database</button><?php endif; ?>
    </div>
    <?php if ($databases === []): ?>
        <div class="card-body small text-body-secondary">No PostgreSQL databases yet.</div>
    <?php else: ?>
        <div class="nim-dtable table-responsive"><table class="table align-middle mb-0">
            <thead><tr><th>Database</th><?php if ($hasRemote): ?><th>Server</th><?php endif; ?><th>Owner</th><th>Website</th><th>Access</th><th class="text-end">Size</th><th class="text-end">Action</th></tr></thead>
            <tbody>
            <?php foreach ($databases as $d): ?>
                <tr>
                    <td class="font-monospace"><?= e((string) $d['name']) ?></td>
                    <?php if ($hasRemote): ?><td class="small"><?= e((string) $d['server_name']) ?></td><?php endif; ?>
                    <td class="small font-monospace"><?= e((string) ($d['owner_role'] ?? '')) ?></td>
                    <td class="small"><?= e((string) ($d['website'] ?? '')) ?></td>
                    <td class="small text-body-secondary">
                        <?php foreach ($d['grants'] as $g): ?><div><span class="font-monospace"><?= e((string) $g['name']) ?></span> · <?= e($levels[$g['level']] ?? (string) $g['level']) ?></div><?php endforeach; ?>
                    </td>
                    <td class="text-end small text-body-secondary"><?= $live !== null && !$d['remote'] ? e(format_bytes((int) $d['size_bytes'])) : '' ?></td>
                    <td class="text-end text-nowrap">
                        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.browse') . '?db=' . (int) $d['id']) ?>" title="Browse tables and rows"><i class="bi bi-table"></i></a>
                        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.sql') . '?db=' . (int) $d['id']) ?>" title="SQL editor"><i class="bi bi-terminal"></i></a>
                        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.backups') . '?db=' . (int) $d['id']) ?>" title="Backups"><i class="bi bi-cloud-arrow-down"></i></a>
                        <?php if ($canManage): ?>
                            <button class="btn btn-sm btn-outline-secondary" type="button" title="Give a user access" data-bs-toggle="modal" data-bs-target="#pg-grant" data-database="<?= (int) $d['id'] ?>" data-name="<?= e((string) $d['name']) ?>"><i class="bi bi-person-check"></i></button>
                            <form class="d-inline" method="post" action="<?= e($this->url('postgres.database_drop')) ?>" data-nim-confirm="Delete the database “<?= e((string) $d['name']) ?>” and everything in it? This cannot be undone.">
                                <?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $d['id'] ?>"><input type="hidden" name="confirm" value="<?= e((string) $d['name']) ?>">
                                <button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button>
                            </form>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    <?php endif; ?>
</div>

<div class="card mb-3">
    <div class="card-header d-flex align-items-center gap-2">
        <span class="fw-medium"><i class="bi bi-people me-1" aria-hidden="true"></i>Database users <span class="badge rounded-pill text-bg-light border ms-1"><?= count($roles) ?></span></span>
        <span class="me-auto"></span>
        <?php if ($canManage): ?><button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#pg-user"><i class="bi bi-person-plus me-1"></i>Add user</button><?php endif; ?>
    </div>
    <?php if ($roles === []): ?>
        <div class="card-body small text-body-secondary">No database users yet.</div>
    <?php else: ?>
        <div class="nim-dtable table-responsive"><table class="table align-middle mb-0">
            <thead><tr><th>User</th><?php if ($hasRemote): ?><th>Server</th><?php endif; ?><th>Website</th><th class="text-end">Action</th></tr></thead>
            <tbody>
            <?php foreach ($roles as $r): ?>
                <tr>
                    <td class="font-monospace"><?= e((string) $r['name']) ?></td>
                    <?php if ($hasRemote): ?><td class="small"><?= e((string) $r['server_name']) ?></td><?php endif; ?>
                    <td class="small"><?= e((string) ($r['website'] ?? '')) ?></td>
                    <td class="text-end text-nowrap">
                        <?php if ($canManage): ?>
                            <form class="d-inline" method="post" action="<?= e($this->url('postgres.user_password')) ?>" data-nim-confirm="Set a new password for “<?= e((string) $r['name']) ?>”? Applications using the old one stop connecting.">
                                <?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $r['id'] ?>">
                                <button class="btn btn-sm btn-outline-secondary" title="New password"><i class="bi bi-key"></i></button>
                            </form>
                            <form class="d-inline" method="post" action="<?= e($this->url('postgres.user_drop')) ?>" data-nim-confirm="Delete the database user “<?= e((string) $r['name']) ?>”?">
                                <?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $r['id'] ?>">
                                <button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button>
                            </form>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    <?php endif; ?>
</div>

<?php if ($canServer): ?>
    <div class="card mb-3">
        <div class="card-header d-flex align-items-center gap-2">
            <span class="fw-medium"><i class="bi bi-hdd-network me-1" aria-hidden="true"></i>PostgreSQL servers <span class="badge rounded-pill text-bg-light border ms-1"><?= count($servers) ?></span></span>
            <span class="me-auto"></span>
            <button class="btn btn-sm btn-outline-primary" type="button" data-bs-toggle="modal" data-bs-target="#pg-server"><i class="bi bi-plus-lg me-1"></i>Register a server</button>
        </div>
        <div class="nim-dtable table-responsive"><table class="table align-middle mb-0 small">
            <thead><tr><th>Name</th><th>Address</th><th>Version</th><th>Status</th><th class="text-end">Databases</th><th class="text-end">Action</th></tr></thead>
            <tbody>
            <?php foreach ($servers as $sv): $remote = (string) $sv['connection_type'] === 'remote'; ?>
                <tr>
                    <td><?= e((string) $sv['name']) ?></td>
                    <td class="font-monospace"><?= $remote ? e((string) $sv['host']) . ':' . (int) $sv['port'] : '127.0.0.1 (this server)' ?></td>
                    <td><?= e((string) $sv['version']) ?></td>
                    <td><span class="badge <?= (string) $sv['status'] === 'active' ? 'text-bg-success' : 'text-bg-warning' ?>"><?= e((string) $sv['status']) ?></span></td>
                    <td class="text-end"><?= (int) $sv['database_count'] ?></td>
                    <td class="text-end text-nowrap">
                        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.monitor') . '?server=' . (int) $sv['id']) ?>" title="Health"><i class="bi bi-activity"></i></a>
                        <?php if ($remote): ?>
                            <form class="d-inline" method="post" action="<?= e($this->url('postgres.server_test')) ?>" data-nim-busy><?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $sv['id'] ?>"><button class="btn btn-sm btn-outline-secondary" title="Test the connection"><i class="bi bi-plug"></i></button></form>
                            <form class="d-inline" method="post" action="<?= e($this->url('postgres.server_remove')) ?>" data-nim-confirm="Forget the server “<?= e((string) $sv['name']) ?>”? Nothing on it is changed.">
                                <?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $sv['id'] ?>"><button class="btn btn-sm btn-outline-danger" title="Remove from NimVor"><i class="bi bi-trash"></i></button>
                            </form>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
        <div class="card-footer small text-body-secondary">A registered server is reached over TLS with a PostgreSQL user that can create databases and users but is not a superuser. Its password is stored encrypted and never shown again.</div>
    </div>
<?php endif; ?>

<?php if ($unmanaged !== []): ?>
    <div class="card mb-3">
        <div class="card-header fw-medium"><i class="bi bi-binoculars me-1" aria-hidden="true"></i>Other databases on this server <span class="small text-body-secondary fw-normal">(not managed by NimVor, left untouched)</span></div>
        <div class="nim-dtable table-responsive"><table class="table align-middle mb-0">
            <thead><tr><th>Database</th><th>Owner</th><th class="text-end">Size</th></tr></thead>
            <tbody>
            <?php foreach ($unmanaged as $d): ?>
                <tr><td class="font-monospace"><?= e((string) $d['name']) ?></td><td class="small font-monospace"><?= e((string) ($d['owner'] ?? '')) ?></td><td class="text-end small text-body-secondary"><?= e(format_bytes((int) ($d['size_bytes'] ?? 0))) ?></td></tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    </div>
<?php endif; ?>

<?php if ($canServer): ?>
<div class="modal fade" id="pg-server" tabindex="-1" aria-labelledby="pg-server-t" aria-hidden="true"><div class="modal-dialog"><form class="modal-content" method="post" action="<?= e($this->url('postgres.server_create')) ?>" data-nim-busy autocomplete="off">
    <?= $this->csrfField() ?>
    <div class="modal-header"><h2 class="modal-title fs-5" id="pg-server-t">Register a PostgreSQL server</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
    <div class="modal-body">
        <label class="form-label" for="pgs-name">Name</label>
        <input class="form-control mb-3" id="pgs-name" name="name" required maxlength="60">
        <div class="row g-2 mb-3">
            <div class="col-8"><label class="form-label" for="pgs-host">Host name or IP address</label><input class="form-control font-monospace" id="pgs-host" name="host" required maxlength="253"></div>
            <div class="col-4"><label class="form-label" for="pgs-port">Port</label><input class="form-control" id="pgs-port" name="port" type="number" min="1" max="65535" value="5432" required></div>
        </div>
        <div class="row g-2 mb-3">
            <div class="col-6"><label class="form-label" for="pgs-user">Management user</label><input class="form-control font-monospace" id="pgs-user" name="username" required maxlength="63" autocomplete="off"></div>
            <div class="col-6"><label class="form-label" for="pgs-pass">Password</label><input class="form-control" id="pgs-pass" name="password" type="password" required autocomplete="new-password"></div>
        </div>
        <label class="form-label" for="pgs-ssl">Encryption</label>
        <select class="form-select mb-3" id="pgs-ssl" name="ssl_mode"><?php foreach ($sslModes as $m => $label): ?><option value="<?= e($m) ?>"<?= $m === 'verify-full' ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?></select>
        <label class="form-label" for="pgs-ca">CA certificate (PEM, optional unless the mode checks it against a CA)</label>
        <textarea class="form-control font-monospace small" id="pgs-ca" name="ca_pem" rows="4" spellcheck="false"></textarea>
        <div class="form-text mt-2">The user needs the CREATEDB and CREATEROLE rights and must not be a superuser. NimVor tests the connection before it keeps anything.</div>
    </div>
    <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary">Test and register</button></div>
</form></div></div>
<?php endif; ?>

<?php if ($canManage): ?>
<div class="modal fade" id="pg-db" tabindex="-1" aria-labelledby="pg-db-t" aria-hidden="true"><div class="modal-dialog"><form class="modal-content" method="post" action="<?= e($this->url('postgres.database_create')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <div class="modal-header"><h2 class="modal-title fs-5" id="pg-db-t">Add a database</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
    <div class="modal-body">
        <?php if ($isAdmin && $hasRemote): ?>
            <label class="form-label" for="pg-db-server">Server</label>
            <select class="form-select mb-3" id="pg-db-server" name="pg_server_id"><option value="">This server (local)</option><?php foreach ($remoteServers as $rs): ?><option value="<?= (int) $rs['id'] ?>"><?= e((string) $rs['name']) ?></option><?php endforeach; ?></select>
        <?php endif; ?>
        <label class="form-label" for="pg-db-name">Database name</label>
        <div class="input-group mb-3"><?php if ($prefix !== ''): ?><span class="input-group-text font-monospace"><?= e($prefix) ?></span><?php endif; ?><input class="form-control font-monospace" id="pg-db-name" name="name" required maxlength="40" pattern="[a-z_][a-z0-9_]*" autocomplete="off"></div>
        <label class="form-label" for="pg-db-owner">Owner</label>
        <select class="form-select mb-2" id="pg-db-owner" name="owner_role_id">
            <option value="">A new database user with the same name</option>
            <?php foreach ($roles as $r): ?><option value="<?= (int) $r['id'] ?>"><?= e((string) $r['name']) ?></option><?php endforeach; ?>
        </select>
        <div class="form-text mb-3">A new user gets a generated password, shown once.</div>
        <?php if ($sites !== []): ?>
            <label class="form-label" for="pg-db-site">Website (optional)</label>
            <select class="form-select" id="pg-db-site" name="website_id"><option value="">None</option><?php foreach ($sites as $s): ?><option value="<?= (int) $s['id'] ?>"><?= e((string) $s['domain']) ?></option><?php endforeach; ?></select>
        <?php endif; ?>
    </div>
    <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary">Create</button></div>
</form></div></div>

<div class="modal fade" id="pg-user" tabindex="-1" aria-labelledby="pg-user-t" aria-hidden="true"><div class="modal-dialog"><form class="modal-content" method="post" action="<?= e($this->url('postgres.user_create')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <div class="modal-header"><h2 class="modal-title fs-5" id="pg-user-t">Add a database user</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
    <div class="modal-body">
        <?php if ($isAdmin && $hasRemote): ?>
            <label class="form-label" for="pg-user-server">Server</label>
            <select class="form-select mb-3" id="pg-user-server" name="pg_server_id"><option value="">This server (local)</option><?php foreach ($remoteServers as $rs): ?><option value="<?= (int) $rs['id'] ?>"><?= e((string) $rs['name']) ?></option><?php endforeach; ?></select>
        <?php endif; ?>
        <label class="form-label" for="pg-user-name">User name</label>
        <div class="input-group mb-3"><?php if ($prefix !== ''): ?><span class="input-group-text font-monospace"><?= e($prefix) ?></span><?php endif; ?><input class="form-control font-monospace" id="pg-user-name" name="username" required maxlength="40" pattern="[a-z_][a-z0-9_]*" autocomplete="off"></div>
        <?php if ($sites !== []): ?>
            <label class="form-label" for="pg-user-site">Website (optional)</label>
            <select class="form-select" id="pg-user-site" name="website_id"><option value="">None</option><?php foreach ($sites as $s): ?><option value="<?= (int) $s['id'] ?>"><?= e((string) $s['domain']) ?></option><?php endforeach; ?></select>
        <?php endif; ?>
        <div class="form-text mt-2">The password is generated and shown once. The user can do nothing until you give it access to a database.</div>
    </div>
    <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary">Create</button></div>
</form></div></div>

<div class="modal fade" id="pg-grant" tabindex="-1" aria-labelledby="pg-grant-t" aria-hidden="true"><div class="modal-dialog"><form class="modal-content" method="post" action="<?= e($this->url('postgres.grant')) ?>" data-nim-busy>
    <?= $this->csrfField() ?><input type="hidden" name="database_id" value="">
    <div class="modal-header"><h2 class="modal-title fs-5" id="pg-grant-t">Access to the database</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
    <div class="modal-body">
        <label class="form-label" for="pg-grant-role">Database user</label>
        <select class="form-select mb-3" id="pg-grant-role" name="role_id" required>
            <?php foreach ($roles as $r): ?><option value="<?= (int) $r['id'] ?>"><?= e((string) $r['name']) ?></option><?php endforeach; ?>
        </select>
        <label class="form-label" for="pg-grant-level">May</label>
        <select class="form-select" id="pg-grant-level" name="level" required>
            <option value="none">Nothing (remove access)</option>
            <?php foreach ($levels as $k => $label): ?><option value="<?= e($k) ?>"><?= e($label) ?></option><?php endforeach; ?>
        </select>
        <div class="form-text mt-2">Applies to the tables that exist and to the ones created later. The database owner always has full access.</div>
    </div>
    <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary">Save</button></div>
</form></div></div>
<?php endif; ?>
