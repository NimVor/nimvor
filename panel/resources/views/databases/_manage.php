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
?>
<?= $this->partial('partials/secret', ['secret' => $secret]) ?>
<?php if ($live === null): ?>
    <div class="alert alert-warning small"><i class="bi bi-plug me-1"></i>The database server could not be queried (is MariaDB/MySQL running and reachable as root over its socket?). Showing WhaCP records only.</div>
<?php endif; ?>

<div class="card mb-3" id="databases">
    <div class="card-header wcp-toolbar">
        <h2 class="wcp-toolbar-title"><i class="bi bi-database me-1"></i>Databases <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($managed) ?></span></h2>
        <?php if (count($managed) > 5): ?><input class="form-control form-control-sm wcp-filter" type="search" placeholder="Filter databases" aria-label="Filter databases" data-wcp-filter="#db-table"><?php endif; ?>
        <?php if ($canManage): ?>
            <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="collapse" data-bs-target="#new-database" aria-expanded="<?= $openDb ? 'true' : 'false' ?>" aria-controls="new-database"><i class="bi bi-plus-lg me-1"></i>Create database</button>
        <?php endif; ?>
    </div>
    <?php if ($canManage): ?>
        <div class="collapse wcp-form-panel<?= $openDb ? ' show' : '' ?>" id="new-database" data-wcp-form-panel>
            <form class="p-3" method="post" action="<?= e($this->url('databases.store')) ?>" data-wcp-busy novalidate>
                <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
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
    <?php endif; ?>
    <?php if ($managed === []): ?>
        <div class="wcp-empty-state">
            <i class="bi bi-database" aria-hidden="true"></i>
            <div class="fw-medium">No databases yet</div>
            <div class="small">Most applications, like WordPress, need a database.<?= $canManage ? ' Use “Create database” to add one.' : '' ?></div>
        </div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table align-middle mb-0 wcp-table" id="db-table">
                <thead><tr><th>Database</th><th>Users</th><th class="text-end">Size</th><th>Status</th><th class="text-end">Actions</th></tr></thead>
                <tbody>
                <?php foreach ($managed as $d): $sz = $sizes[$d['name']] ?? null; ?>
                    <tr>
                        <td><span class="fw-semibold"><?= e($d['name']) ?></span><?php if (!isset($site) && !empty($d['website'])): ?><div class="small text-body-secondary"><?= e($d['website']) ?></div><?php endif; ?></td>
                        <td class="small">
                            <?php foreach ($d['grants'] as $g): ?>
                                <div><code><?= e($g['username']) ?></code> <span class="text-body-secondary"><?= e(DatabaseService::PRIVILEGES[$g['privileges']] ?? $g['privileges']) ?></span></div>
                            <?php endforeach; ?>
                            <?php if ($d['grants'] === []): ?><span class="text-body-secondary">No user</span><?php endif; ?>
                            <?php if ($canManage && $dbUsers !== []): ?>
                                <details class="mt-1">
                                    <summary class="small text-body-secondary">Change access</summary>
                                    <form method="post" action="<?= e($this->url('databases.grant', ['id' => (int) $d['id']])) ?>" class="d-flex flex-wrap gap-1 mt-1" data-wcp-busy>
                                        <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                                        <select class="form-select form-select-sm w-auto" name="user_id" aria-label="User"><?php foreach ($dbUsers as $u): ?><option value="<?= (int) $u['id'] ?>"><?= e($u['username'] . '@' . $u['host']) ?></option><?php endforeach; ?></select>
                                        <select class="form-select form-select-sm w-auto" name="privileges" aria-label="Privileges"><?php foreach (DatabaseService::PRIVILEGES as $k => $l): ?><option value="<?= e($k) ?>"><?= e($l) ?></option><?php endforeach; ?><option value="none">No access</option></select>
                                        <button class="btn btn-sm btn-outline-secondary" type="submit">Set</button>
                                    </form>
                                </details>
                            <?php endif; ?>
                        </td>
                        <td class="text-end small text-nowrap"><?= $sz !== null ? e(format_bytes((int) $sz['size_bytes'])) . '<div class="text-body-secondary">' . (int) $sz['tables'] . ' tables</div>' : '<span class="text-body-secondary">—</span>' ?></td>
                        <td class="small"><?php if ($live === null): ?><span class="text-body-secondary">unknown</span><?php elseif ($sz !== null): ?><span class="wcp-state is-ok">OK</span><?php else: ?><span class="wcp-state is-error" title="WhaCP has a record of this database, but the database server does not list it.">Missing</span><?php endif; ?></td>
                        <td class="text-end text-nowrap">
                            <?php if ($canManage): ?>
                                <a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('databases.browser', ['id' => (int) $d['id']])) ?>" title="Browse tables and run SQL"><i class="bi bi-table"></i> Browse</a>
                                <form method="post" action="<?= e($this->url('databases.dump', ['id' => (int) $d['id']])) ?>" class="d-inline"><?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><button class="btn btn-sm btn-outline-secondary" title="Dump to the backup directory" aria-label="Dump <?= e($d['name']) ?>"><i class="bi bi-download"></i></button></form>
                                <form method="post" action="<?= e($this->url('databases.drop', ['id' => (int) $d['id']])) ?>" class="d-inline" data-wcp-confirm="Delete the database <?= e($d['name']) ?> and all of its data? This cannot be undone." data-wcp-confirm-word="<?= e($d['name']) ?>">
                                    <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><input type="hidden" name="confirm" value="<?= e($d['name']) ?>">
                                    <button class="btn btn-sm btn-outline-danger" title="Delete" aria-label="Delete <?= e($d['name']) ?>"><i class="bi bi-trash"></i></button>
                                </form>
                            <?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                <tr class="d-none" data-wcp-filter-empty><td colspan="5" class="text-body-secondary small">No database matches the filter.</td></tr>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>

<?php
// Databases each user may use, from the grants listed with the databases.
$userDbs = [];
foreach ($managed as $d) {
    foreach ($d['grants'] as $g) {
        $userDbs[(int) $g['user_id']][] = ['name' => (string) $d['name'], 'privileges' => (string) $g['privileges']];
    }
}
// "Connect from": this server, a specific address or network, any host.
$hostFields = function (string $idp, bool $withLocal) use ($old): string {
    ob_start(); ?>
    <div class="col-md-4">
        <label class="form-label small" for="<?= e($idp) ?>-hc">Connect from</label>
        <select class="form-select form-select-sm" id="<?= e($idp) ?>-hc" name="host_choice" data-wcp-toggle-kind>
            <?php if ($withLocal): ?><option value="localhost">This server only</option><?php endif; ?>
            <option value="ip"<?= ($old['host_choice'] ?? '') === 'ip' ? ' selected' : '' ?>>A specific IP address or network</option>
            <option value="any"<?= ($old['host_choice'] ?? '') === 'any' ? ' selected' : '' ?>>Any host (%)</option>
        </select>
    </div>
    <div class="col-md-4" data-wcp-kind="ip">
        <label class="form-label small" for="<?= e($idp) ?>-ip">IP address or network</label>
        <input class="form-control form-control-sm font-monospace" id="<?= e($idp) ?>-ip" name="host_ip" value="<?= e((string) ($old['host_ip'] ?? '')) ?>" placeholder="203.0.113.5 or 203.0.113.%">
    </div>
    <?php return (string) ob_get_clean();
};
$remoteNote = 'Remote connections also need the database server to accept them (its bind-address) and port 3306 open in the firewall' . ($this->can(\WhaCP\Security\Permissions::SERVER_MANAGE) ? '.' : '; ask your hosting provider if they do not work.');
?>
<div class="row g-3">
    <div class="col-xl-8">
        <div class="card" id="db-users">
            <div class="card-header wcp-toolbar">
                <h2 class="wcp-toolbar-title"><i class="bi bi-person-lock me-1"></i>Database users <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($dbUsers) ?></span></h2>
                <?php if ($canManage): ?>
                    <button class="btn btn-sm btn-outline-primary" type="button" data-bs-toggle="collapse" data-bs-target="#new-db-user" aria-expanded="<?= $openUser ? 'true' : 'false' ?>" aria-controls="new-db-user"><i class="bi bi-person-plus me-1"></i>Create user</button>
                <?php endif; ?>
            </div>
            <?php if ($canManage): ?>
                <div class="collapse wcp-form-panel<?= $openUser ? ' show' : '' ?>" id="new-db-user" data-wcp-form-panel>
                    <form class="p-3" method="post" action="<?= e($this->url('dbusers.store')) ?>" data-wcp-busy novalidate>
                        <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
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
            <?php if ($dbUsers === []): ?>
                <div class="wcp-empty-state"><i class="bi bi-person-lock" aria-hidden="true"></i><div class="fw-medium">No database users yet</div><div class="small">Creating a database can create its user for you.</div></div>
            <?php else: ?>
                <div class="table-responsive">
                    <table class="table align-middle mb-0 wcp-table" id="db-users-table">
                        <thead><tr><th>User</th><th>Connects from</th><th>Databases</th><th>Password</th><th class="text-end">Actions</th></tr></thead>
                        <tbody>
                        <?php foreach ($dbUsers as $u): $uid = (int) $u['id']; $mine = $userDbs[$uid] ?? []; $free = array_values(array_filter($managed, static fn (array $d): bool => !in_array((string) $d['name'], array_column($mine, 'name'), true))); ?>
                            <tr data-wcp-db-user="<?= e($u['username'] . '@' . $u['host']) ?>">
                                <td class="fw-semibold"><?= e($u['username']) ?></td>
                                <td class="small"><?= $u['host'] === 'localhost' ? 'This server' : ($u['host'] === '%' ? '<span class="wcp-state is-warn">Any host</span>' : '<code>' . e($u['host']) . '</code>') ?></td>
                                <td class="small">
                                    <?php foreach ($mine as $g): ?><div><code><?= e($g['name']) ?></code> <span class="text-body-secondary"><?= e(DatabaseService::PRIVILEGES[$g['privileges']] ?? $g['privileges']) ?></span></div><?php endforeach; ?>
                                    <?php if ($mine === []): ?><span class="text-body-secondary">None</span><?php endif; ?>
                                    <?php if ($canManage && $free !== []): ?>
                                        <details class="mt-1" data-wcp-assign>
                                            <summary class="small">Assign to database</summary>
                                            <form method="post" action="<?= e($this->url('dbusers.assign', ['id' => $uid])) ?>" class="d-flex flex-wrap gap-1 mt-1" data-wcp-busy>
                                                <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                                                <select class="form-select form-select-sm w-auto" name="database_id" aria-label="Database"><?php foreach ($free as $d): ?><option value="<?= (int) $d['id'] ?>"><?= e($d['name']) ?></option><?php endforeach; ?></select>
                                                <select class="form-select form-select-sm w-auto" name="privileges" aria-label="Privileges"><?php foreach (DatabaseService::PRIVILEGES as $k => $l): ?><option value="<?= e($k) ?>"><?= e($l) ?></option><?php endforeach; ?></select>
                                                <button class="btn btn-sm btn-outline-primary" type="submit">Assign</button>
                                            </form>
                                        </details>
                                    <?php endif; ?>
                                </td>
                                <td class="small"><?= $u['stored'] ? 'stored encrypted' : '<span class="text-body-secondary">not stored</span>' ?></td>
                                <td class="text-end text-nowrap">
                                    <?php if ($canManage): ?>
                                        <details class="d-inline-block text-start" data-wcp-remote>
                                            <summary class="btn btn-sm btn-outline-secondary" title="Allow connections from another host" aria-label="Remote access for <?= e($u['username']) ?>"><i class="bi bi-globe"></i></summary>
                                            <form class="wcp-popover-form card card-body mt-1 position-absolute end-0 shadow-sm" method="post" action="<?= e($this->url('dbusers.host', ['id' => $uid])) ?>" data-wcp-busy>
                                                <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                                                <div class="fw-medium small mb-2">Remote access for <?= e($u['username']) ?></div>
                                                <div class="row g-2"><?= str_replace(['col-md-4'], ['col-12'], $hostFields('rh' . $uid, false)) ?></div>
                                                <p class="small text-body-secondary my-2">Creates a separate login with a new password (shown once) and the same database access.</p>
                                                <input type="hidden" name="store_password" value="<?= $u['stored'] ? '1' : '0' ?>">
                                                <button class="btn btn-sm btn-primary" type="submit">Allow</button>
                                            </form>
                                        </details>
                                        <?php if ($u['stored']): ?>
                                            <form method="post" action="<?= e($this->url('dbusers.reveal', ['id' => $uid])) ?>" class="d-inline"><?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><button class="btn btn-sm btn-outline-secondary" title="Show password" aria-label="Show password of <?= e($u['username']) ?>"><i class="bi bi-eye"></i></button></form>
                                        <?php endif; ?>
                                        <form method="post" action="<?= e($this->url('dbusers.password', ['id' => $uid])) ?>" class="d-inline" data-wcp-confirm="Generate a new password for <?= e($u['username']) ?>? Applications using the old password will stop working until updated.">
                                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><input type="hidden" name="store_password" value="<?= $u['stored'] ? '1' : '0' ?>">
                                            <button class="btn btn-sm btn-outline-secondary" title="New password" aria-label="New password for <?= e($u['username']) ?>"><i class="bi bi-arrow-repeat"></i></button>
                                        </form>
                                        <form method="post" action="<?= e($this->url('dbusers.delete', ['id' => $uid])) ?>" class="d-inline" data-wcp-confirm="Remove the database user <?= e($u['username'] . '@' . $u['host']) ?>?">
                                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><button class="btn btn-sm btn-outline-danger" title="Delete" aria-label="Delete user <?= e($u['username']) ?>"><i class="bi bi-trash"></i></button>
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
    </div>
    <div class="col-xl-4">
        <div class="card">
            <div class="card-header"><i class="bi bi-plug me-1"></i>Connect your application</div>
            <div class="card-body small">
                <dl class="wcp-dl mb-2">
                    <dt>Host</dt><dd><code>localhost</code> (on this server)<?php if (!empty($serverIp)): ?><br><code><?= e($serverIp) ?></code> (remote)<?php endif; ?></dd>
                    <dt>Port</dt><dd><code>3306</code></dd>
                    <?php if (!empty($live['version'])): ?><dt>Server</dt><dd><?= e($live['version']) ?></dd><?php endif; ?>
                </dl>
                <p class="text-body-secondary">Use the database name, user name and password above in your application's settings (e.g. <code>wp-config.php</code> or <code>.env</code>).</p>
                <p class="text-body-secondary mb-0"><i class="bi bi-globe me-1"></i>To connect from your computer or another server, give the user remote access (<i class="bi bi-globe"></i>). <?= e($remoteNote) ?></p>
            </div>
        </div>
    </div>
</div>
