<?php
/** @var \WhaCP\View\View $this */
use WhaCP\Security\Permissions as P;
use WhaCP\Services\DatabaseService;

$canManage = $this->can(P::DATABASES_MANAGE);
$returnTo = $returnTo ?? '/databases';
$errors ??= [];
$old ??= [];
// Both forms can report a user name error (a new database creates its
// user); only the user form sends "username", so it tells them apart.
$fromUserForm = array_key_exists('username', $old);
$dbError = $fromUserForm ? null : ($errors['name'] ?? $errors['username'] ?? null);
$userError = $fromUserForm ? ($errors['username'] ?? $errors['host'] ?? null) : null;
$openDb = $dbError !== null;
$openUser = $userError !== null;
// Databases each user may use, from the grants listed with the databases.
$userDbs = [];
foreach ($managed as $d) {
    foreach ($d['grants'] as $g) {
        $userDbs[(int) $g['user_id']][] = ['name' => (string) $d['name'], 'privileges' => (string) $g['privileges']];
    }
}
// "Connect from": this server, a specific address or network, any host.
$hostFields = function (string $idp, bool $withLocal, string $col = 'col-md-4') use ($old): string {
    ob_start(); ?>
    <div class="<?= e($col) ?>">
        <label class="form-label small" for="<?= e($idp) ?>-hc">Connect from</label>
        <select class="form-select form-select-sm" id="<?= e($idp) ?>-hc" name="host_choice" data-wcp-toggle-kind>
            <?php if ($withLocal): ?><option value="localhost">This server only</option><?php endif; ?>
            <option value="ip"<?= ($old['host_choice'] ?? '') === 'ip' ? ' selected' : '' ?>>A specific IP address or network</option>
            <option value="any"<?= ($old['host_choice'] ?? '') === 'any' ? ' selected' : '' ?>>Any host (%)</option>
        </select>
    </div>
    <div class="<?= e($col) ?>" data-wcp-kind="ip">
        <label class="form-label small" for="<?= e($idp) ?>-ip">IP address or network</label>
        <input class="form-control form-control-sm font-monospace" id="<?= e($idp) ?>-ip" name="host_ip" value="<?= e((string) ($old['host_ip'] ?? '')) ?>" placeholder="203.0.113.5 or 203.0.113.%">
    </div>
    <?php return (string) ob_get_clean();
};
$remoteNote = 'Remote connections also need the database server to accept them (its bind-address) and port 3306 open in the firewall' . ($this->can(P::SERVER_MANAGE) ? '.' : '; ask your hosting provider if they do not work.');
// "10.11.6-MariaDB-0ubuntu…" → "MariaDB 10.11.6"; otherwise "MySQL 8.0.36".
$server = null;
if (!empty($live['version'])) {
    $v = (string) $live['version'];
    $server = (stripos($v, 'mariadb') !== false ? 'MariaDB' : 'MySQL') . (preg_match('/^[0-9]+(\.[0-9]+){1,2}/', $v, $m) ? ' ' . $m[0] : '');
}
$tab = $openUser ? 'users' : 'dbs';
?>
<?= $this->partial('partials/secret', ['secret' => $secret]) ?>
<div class="wcp-infobar">
    <i class="bi bi-info-circle-fill" aria-hidden="true"></i>
    <span>Host <code>localhost</code><?php if (!empty($serverIp)): ?> <span class="wcp-muted">(on this server)</span> · <code><?= e($serverIp) ?></code> <span class="wcp-muted">(remote)</span><?php endif; ?> · port <code>3306</code></span>
    <span class="small wcp-muted">Use the database name, user and password in your application's settings (e.g. <code>wp-config.php</code> or <code>.env</code>).</span>
</div>
<?php if ($live === null): ?>
    <div class="alert alert-warning small"><i class="bi bi-plug me-1"></i>The database server could not be queried (is MariaDB/MySQL running and reachable as root over its socket?). Showing WhaCP records only.</div>
<?php endif; ?>

<div class="wcp-panel" id="databases">
    <div class="wcp-actionbar">
        <?php if ($canManage): ?>
            <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="collapse" data-bs-target="#new-database" aria-expanded="<?= $openDb ? 'true' : 'false' ?>" aria-controls="new-database"><i class="bi bi-plus-lg me-1"></i>Add database</button>
            <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="collapse" data-bs-target="#new-db-user" aria-expanded="<?= $openUser ? 'true' : 'false' ?>" aria-controls="new-db-user"><i class="bi bi-person-plus me-1"></i>Add user</button>
            <?php if (!isset($site) && $this->hasRoute('databases.browser.server') && !empty($unmanaged)): ?><a class="btn btn-sm btn-outline-secondary" href="#other-databases"><i class="bi bi-binoculars me-1"></i>Other databases (<?= count($unmanaged) ?>)</a><?php endif; ?>
        <?php endif; ?>
        <?php if ($server !== null): ?><span class="wcp-svc" title="Database server"><i class="bi bi-database" aria-hidden="true"></i><?= e($server) ?> <i class="bi bi-play-fill" aria-hidden="true"></i></span><?php elseif ($live === null): ?><span class="wcp-svc" title="Database server"><i class="bi bi-database" aria-hidden="true"></i>Database server <i class="bi bi-stop-fill" aria-hidden="true"></i></span><?php endif; ?>
        <div class="wcp-actionbar-end">
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Search databases and users" aria-label="Search databases and users" data-wcp-dt-search-for="#db-list #db-users"><i class="bi bi-search" aria-hidden="true"></i></div>
        </div>
    </div>

    <?php if ($canManage): ?>
        <div class="collapse<?= $openDb ? ' show' : '' ?>" id="new-database" data-wcp-form-panel>
            <form class="wcp-form-panel border rounded-3 p-3 mb-3" method="post" action="<?= e($this->url('databases.store')) ?>" data-wcp-busy novalidate>
                <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                <div class="fw-semibold mb-2">Add database</div>
                <div class="row g-2 align-items-end">
                    <div class="col-md-5">
                        <label class="form-label small" for="db-name">Database name</label>
                        <div class="input-group input-group-sm">
                            <?php if ($prefix !== ''): ?><span class="input-group-text"><?= e($prefix) ?></span><?php endif; ?>
                            <input class="form-control<?= $dbError !== null ? ' is-invalid' : '' ?>" id="db-name" name="name" value="<?= e($old['name'] ?? '') ?>" maxlength="48" required>
                        </div>
                    </div>
                    <?php if (isset($site)): ?>
                        <input type="hidden" name="website_id" value="<?= (int) $site['id'] ?>">
                    <?php else: ?>
                        <div class="col-md-4">
                            <label class="form-label small" for="db-site">Website (optional)</label>
                            <select class="form-select form-select-sm" id="db-site" name="website_id"><option value="">—</option><?php foreach ($websites as $w): ?><option value="<?= (int) $w['id'] ?>"><?= e($w['domain']) ?></option><?php endforeach; ?></select>
                        </div>
                    <?php endif; ?>
                    <div class="col-md-auto"><button class="btn btn-sm btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none" aria-hidden="true"></span> Create database</button></div>
                </div>
                <?php if ($dbError !== null): ?><div class="text-danger small mt-1"><?= e((string) $dbError) ?></div><?php endif; ?>
                <div class="d-flex flex-wrap gap-3 mt-2">
                    <div class="form-check form-switch small"><input type="hidden" name="create_user" value="0"><input class="form-check-input" type="checkbox" name="create_user" value="1" id="db-cu" checked><label class="form-check-label" for="db-cu">Create a user with full access</label></div>
                    <div class="form-check form-switch small"><input type="hidden" name="store_password" value="0"><input class="form-check-input" type="checkbox" name="store_password" value="1" id="db-sp"><label class="form-check-label" for="db-sp">Keep the password (encrypted) so it can be shown again</label></div>
                </div>
            </form>
        </div>
        <div class="collapse<?= $openUser ? ' show' : '' ?>" id="new-db-user" data-wcp-form-panel>
            <form class="wcp-form-panel border rounded-3 p-3 mb-3" method="post" action="<?= e($this->url('dbusers.store')) ?>" data-wcp-busy novalidate>
                <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                <div class="fw-semibold mb-2">Add database user</div>
                <div class="row g-2 align-items-end">
                    <div class="col-md-4">
                        <label class="form-label small" for="dbu-name">User name</label>
                        <div class="input-group input-group-sm">
                            <?php if ($prefix !== ''): ?><span class="input-group-text"><?= e($prefix) ?></span><?php endif; ?>
                            <input class="form-control<?= $userError !== null ? ' is-invalid' : '' ?>" id="dbu-name" name="username" placeholder="username" maxlength="24" required>
                        </div>
                    </div>
                    <?= $hostFields('dbu', true) ?>
                </div>
                <?php if ($userError !== null): ?><div class="text-danger small mt-1"><?= e((string) $userError) ?></div><?php endif; ?>
                <div class="d-flex flex-wrap align-items-center gap-3 mt-2">
                    <div class="form-check form-switch small mb-0"><input type="hidden" name="store_password" value="0"><input class="form-check-input" type="checkbox" name="store_password" value="1" id="u-sp"><label class="form-check-label" for="u-sp">Keep the password (encrypted)</label></div>
                    <button class="btn btn-sm btn-primary ms-auto" type="submit">Create user</button>
                </div>
                <div class="form-text"><?= e($remoteNote) ?></div>
            </form>
        </div>
    <?php endif; ?>

    <div class="wcp-subtabs" role="tablist">
        <button class="<?= $tab === 'dbs' ? 'active' : '' ?>" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#db-list" aria-controls="db-list" aria-selected="<?= $tab === 'dbs' ? 'true' : 'false' ?>">Databases: <?= count($managed) ?></button>
        <button class="<?= $tab === 'users' ? 'active' : '' ?>" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#db-users" aria-controls="db-users" aria-selected="<?= $tab === 'users' ? 'true' : 'false' ?>">Users: <?= count($dbUsers) ?></button>
    </div>
    <div class="tab-content">
        <div class="tab-pane fade<?= $tab === 'dbs' ? ' show active' : '' ?>" id="db-list" role="tabpanel" data-wcp-dt data-wcp-db-pane>
                        <div class="wcp-dtable table-responsive">
                <table class="table align-middle" id="db-table">
                    <thead><tr><th>Database name</th><th>Username</th><th class="text-end">Size</th><?php if (!isset($site)): ?><th>Location</th><?php endif; ?><th>Status</th><th class="text-end">Operate</th></tr></thead>
                    <tbody>
                    <?php foreach ($managed as $d): $sz = $sizes[$d['name']] ?? null; $did = (int) $d['id']; ?>
                        <tr data-wcp-row>
                            <td class="fw-semibold"><?= e($d['name']) ?></td>
                            <td class="small">
                                <?php foreach ($d['grants'] as $g): ?>
                                    <div><?= e($g['username']) ?> <span class="wcp-muted"><?= e(DatabaseService::PRIVILEGES[$g['privileges']] ?? $g['privileges']) ?></span></div>
                                <?php endforeach; ?>
                                <?php if ($d['grants'] === []): ?><span class="wcp-warn">No user</span><?php endif; ?>
                            </td>
                            <td class="text-end small text-nowrap"><?= $sz !== null ? e(format_bytes((int) $sz['size_bytes'])) . '<div class="wcp-sub">' . (int) $sz['tables'] . ' tables</div>' : '<span class="wcp-muted">—</span>' ?></td>
                            <?php if (!isset($site)): ?><td class="small"><?php if (!empty($d['website']) && !empty($d['website_id']) && $this->can(P::WEBSITES_VIEW)): ?><a href="<?= e($this->url('sites.databases', ['id' => (int) $d['website_id']])) ?>"><?= e($d['website']) ?></a><?php elseif (!empty($d['website'])): ?><?= e($d['website']) ?><?php else: ?><span class="wcp-muted">Localhost</span><?php endif; ?></td><?php endif; ?>
                            <td class="small"><?php if ($live === null): ?><span class="wcp-muted">unknown</span><?php elseif ($sz !== null): ?><span class="wcp-state is-ok">OK</span><?php else: ?><span class="wcp-state is-error" title="WhaCP has a record of this database, but the database server does not list it.">Missing</span><?php endif; ?></td>
                            <td class="text-end">
                                <?php if ($canManage): ?>
                                    <span class="wcp-ops">
                                        <a class="wcp-op" href="<?= e($this->url('databases.browser', ['id' => $did])) ?>" title="Browse tables and run SQL">Browse</a>
                                        <?php if ($dbUsers !== []): ?>
                                            <details data-wcp-menu>
                                                <summary class="wcp-op" aria-label="Access to <?= e($d['name']) ?>">Permission</summary>
                                                <form method="post" action="<?= e($this->url('databases.grant', ['id' => $did])) ?>" class="wcp-popover card card-body shadow-sm" data-wcp-busy>
                                                    <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                                                    <div class="fw-medium small mb-2">Access to <?= e($d['name']) ?></div>
                                                    <label class="form-label small mb-1" for="g-u-<?= $did ?>">User</label>
                                                    <select class="form-select form-select-sm mb-2" id="g-u-<?= $did ?>" name="user_id"><?php foreach ($dbUsers as $u): ?><option value="<?= (int) $u['id'] ?>"><?= e($u['username'] . '@' . $u['host']) ?></option><?php endforeach; ?></select>
                                                    <label class="form-label small mb-1" for="g-p-<?= $did ?>">Privileges</label>
                                                    <select class="form-select form-select-sm mb-2" id="g-p-<?= $did ?>" name="privileges"><?php foreach (DatabaseService::PRIVILEGES as $k => $l): ?><option value="<?= e($k) ?>"><?= e($l) ?></option><?php endforeach; ?><option value="none">No access</option></select>
                                                    <button class="btn btn-sm btn-primary" type="submit">Set</button>
                                                </form>
                                            </details>
                                        <?php endif; ?>
                                        <?php if (!empty($d['website_id'])): ?>
                                            <details data-wcp-import data-wcp-menu>
                                                <summary class="wcp-op" title="Import a .sql file" aria-label="Import into <?= e($d['name']) ?>">Import</summary>
                                                <form class="wcp-popover wcp-popover-form card card-body shadow-sm" method="post" action="<?= e($this->url('databases.import', ['id' => $did])) ?>"
                                                      data-wcp-sql-import<?= $this->can(P::FILES_MANAGE) ? ' data-upload-url="' . e($this->url('sites.files.upload', ['id' => (int) $d['website_id']])) . '"' : '' ?>>
                                                    <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                                                    <input type="hidden" name="path" value=""><input type="hidden" name="uploaded" value="0">
                                                    <div class="fw-medium small mb-2">Import into <?= e($d['name']) ?></div>
                                                    <?php if ($this->can(P::FILES_MANAGE)): ?>
                                                        <label class="form-label small mb-1" for="imp-f-<?= $did ?>">Upload a .sql or .sql.gz file</label>
                                                        <input class="form-control form-control-sm mb-2" type="file" id="imp-f-<?= $did ?>" accept=".sql,.gz,application/sql,application/gzip" data-wcp-import-file>
                                                        <div class="progress mb-2 d-none" role="progressbar" aria-label="Upload progress" data-wcp-import-progress><div class="progress-bar"></div></div>
                                                        <div class="small text-body-secondary mb-1">or a file already in the website folder:</div>
                                                    <?php endif; ?>
                                                    <input class="form-control form-control-sm font-monospace mb-2" name="existing" placeholder="backups/dump.sql" aria-label="File in the website folder">
                                                    <p class="small text-body-secondary mb-2">The SQL adds to the database and may replace its tables. Consider a dump first.</p>
                                                    <div class="text-danger small mb-2 d-none" data-wcp-import-error></div>
                                                    <button class="btn btn-sm btn-primary" type="submit">Import</button>
                                                </form>
                                            </details>
                                        <?php endif; ?>
                                        <form method="post" action="<?= e($this->url('databases.dump', ['id' => $did])) ?>"><?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><button class="wcp-op" title="Dump to the backup directory" aria-label="Dump <?= e($d['name']) ?>">Dump</button></form>
                                        <form method="post" action="<?= e($this->url('databases.drop', ['id' => $did])) ?>" data-wcp-confirm="Delete the database <?= e($d['name']) ?> and all of its data? This cannot be undone." data-wcp-confirm-word="<?= e($d['name']) ?>">
                                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><input type="hidden" name="confirm" value="<?= e($d['name']) ?>">
                                            <button class="wcp-op is-danger" title="Delete" aria-label="Delete <?= e($d['name']) ?>">Delete</button>
                                        </form>
                                    </span>
                                <?php else: ?><span class="wcp-muted small">—</span><?php endif; ?>
                            </td>
                        </tr>
                    <?php endforeach; ?>
                    <?php if ($managed === []): ?>
                        <tr><td colspan="<?= isset($site) ? 5 : 6 ?>"><div class="wcp-empty-state">
                            <i class="bi bi-database" aria-hidden="true"></i>
                            <div class="fw-medium">No databases yet</div>
                            <div class="small">Most applications, like WordPress, need a database.<?= $canManage ? ' Use “Add database” to add one.' : '' ?></div>
                        </div></td></tr>
                    <?php else: ?>
                        <tr class="d-none" data-wcp-dt-empty><td colspan="<?= isset($site) ? 5 : 6 ?>" class="text-center py-4 wcp-muted">No database matches.</td></tr>
                    <?php endif; ?>
                    </tbody>
                </table>
            </div>
            <div class="wcp-pager" data-wcp-dt-pager></div>
        </div>

        <div class="tab-pane fade<?= $tab === 'users' ? ' show active' : '' ?>" id="db-users" role="tabpanel" data-wcp-dt data-wcp-db-pane>
                        <div class="wcp-dtable table-responsive">
                <table class="table align-middle" id="db-users-table">
                    <thead><tr><th>Username</th><th>Password</th><th>Connects from</th><th>Databases</th><th class="text-end">Operate</th></tr></thead>
                    <tbody>
                    <?php foreach ($dbUsers as $u): $uid = (int) $u['id']; $mine = $userDbs[$uid] ?? []; $free = array_values(array_filter($managed, static fn (array $d): bool => !in_array((string) $d['name'], array_column($mine, 'name'), true))); ?>
                        <tr data-wcp-row data-wcp-db-user="<?= e($u['username'] . '@' . $u['host']) ?>">
                            <td class="fw-semibold"><?= e($u['username']) ?></td>
                            <td class="small text-nowrap">
                                <?php if ($u['stored'] && $canManage): ?>
                                    <form method="post" action="<?= e($this->url('dbusers.reveal', ['id' => $uid])) ?>" class="d-inline"><?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">**********<button class="wcp-icon-op ms-1" title="Show password" aria-label="Show password of <?= e($u['username']) ?>"><i class="bi bi-eye"></i></button></form>
                                <?php elseif ($u['stored']): ?>**********
                                <?php else: ?><span class="wcp-muted" title="WhaCP did not keep this password. Set a new one to see it.">Not stored</span><?php endif; ?>
                            </td>
                            <td class="small"><?= $u['host'] === 'localhost' ? 'This server' : ($u['host'] === '%' ? '<span class="wcp-state is-warn">Any host</span>' : '<code>' . e($u['host']) . '</code>') ?></td>
                            <td class="small">
                                <?php foreach ($mine as $g): ?><div><?= e($g['name']) ?> <span class="wcp-muted"><?= e(DatabaseService::PRIVILEGES[$g['privileges']] ?? $g['privileges']) ?></span></div><?php endforeach; ?>
                                <?php if ($mine === []): ?><span class="wcp-muted">None</span><?php endif; ?>
                            </td>
                            <td class="text-end">
                                <?php if ($canManage): ?>
                                    <span class="wcp-ops">
                                        <?php if ($free !== []): ?>
                                            <details data-wcp-assign data-wcp-menu>
                                                <summary class="wcp-op">Assign to database</summary>
                                                <form method="post" action="<?= e($this->url('dbusers.assign', ['id' => $uid])) ?>" class="wcp-popover card card-body shadow-sm" data-wcp-busy>
                                                    <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                                                    <div class="fw-medium small mb-2">Assign <?= e($u['username']) ?> to a database</div>
                                                    <label class="form-label small mb-1" for="as-d-<?= $uid ?>">Database</label>
                                                    <select class="form-select form-select-sm mb-2" id="as-d-<?= $uid ?>" name="database_id"><?php foreach ($free as $d): ?><option value="<?= (int) $d['id'] ?>"><?= e($d['name']) ?></option><?php endforeach; ?></select>
                                                    <label class="form-label small mb-1" for="as-p-<?= $uid ?>">Privileges</label>
                                                    <select class="form-select form-select-sm mb-2" id="as-p-<?= $uid ?>" name="privileges"><?php foreach (DatabaseService::PRIVILEGES as $k => $l): ?><option value="<?= e($k) ?>"><?= e($l) ?></option><?php endforeach; ?></select>
                                                    <button class="btn btn-sm btn-primary" type="submit">Assign</button>
                                                </form>
                                            </details>
                                        <?php endif; ?>
                                        <details data-wcp-remote data-wcp-menu>
                                            <summary class="wcp-op" title="Allow connections from another host" aria-label="Remote access for <?= e($u['username']) ?>">Remote</summary>
                                            <form class="wcp-popover wcp-popover-form card card-body shadow-sm" method="post" action="<?= e($this->url('dbusers.host', ['id' => $uid])) ?>" data-wcp-busy>
                                                <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                                                <div class="fw-medium small mb-2">Remote access for <?= e($u['username']) ?></div>
                                                <div class="row g-2"><?= $hostFields('rh' . $uid, false, 'col-12') ?></div>
                                                <p class="small text-body-secondary my-2">Creates a separate login with a new password (shown once) and the same database access. <?= e($remoteNote) ?></p>
                                                <input type="hidden" name="store_password" value="<?= $u['stored'] ? '1' : '0' ?>">
                                                <button class="btn btn-sm btn-primary" type="submit">Allow</button>
                                            </form>
                                        </details>
                                        <form method="post" action="<?= e($this->url('dbusers.password', ['id' => $uid])) ?>" data-wcp-confirm="Generate a new password for <?= e($u['username']) ?>? Applications using the old password will stop working until updated.">
                                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><input type="hidden" name="store_password" value="<?= $u['stored'] ? '1' : '0' ?>">
                                            <button class="wcp-op" title="New password" aria-label="New password for <?= e($u['username']) ?>">Password</button>
                                        </form>
                                        <form method="post" action="<?= e($this->url('dbusers.delete', ['id' => $uid])) ?>" data-wcp-confirm="Remove the database user <?= e($u['username'] . '@' . $u['host']) ?>?">
                                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><button class="wcp-op is-danger" title="Delete" aria-label="Delete user <?= e($u['username']) ?>">Delete</button>
                                        </form>
                                    </span>
                                <?php else: ?><span class="wcp-muted small">—</span><?php endif; ?>
                            </td>
                        </tr>
                    <?php endforeach; ?>
                    <?php if ($dbUsers === []): ?>
                        <tr><td colspan="5"><div class="wcp-empty-state"><i class="bi bi-person-lock" aria-hidden="true"></i><div class="fw-medium">No database users yet</div><div class="small">Creating a database can create its user for you.</div></div></td></tr>
                    <?php else: ?>
                        <tr class="d-none" data-wcp-dt-empty><td colspan="5" class="text-center py-4 wcp-muted">No user matches.</td></tr>
                    <?php endif; ?>
                    </tbody>
                </table>
            </div>
            <div class="wcp-pager" data-wcp-dt-pager></div>
        </div>
    </div>
</div>
