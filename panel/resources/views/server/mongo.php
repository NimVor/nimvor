<?php
/**
 * Server → MongoDB: databases and users, admin only. Laid out as tabs
 * (Databases, Users, Backups, Configuration, Log); only the active tab's data
 * is loaded, and every change is a modal so the page stays uncluttered as the
 * number of databases grows.
 *
 * @var \WhaCP\View\View $this
 * @var array{installed: bool, running: bool, version: string, auth: bool}|null $status
 * @var array{databases: list<array<string, mixed>>, users: list<array<string, mixed>>} $overview
 * @var array{kind: string, name: string, url: string, version: string, supported: bool, enabled: bool, note: string}|null $repository
 * @var string $section
 */
use WhaCP\Security\Permissions as P;

$status ??= null;
$overview ??= ['databases' => [], 'users' => [], 'databases_error' => '', 'users_error' => ''];
$repository ??= null;
$overviewError ??= null;
$backups ??= [];
$section ??= 'databases';
$canManage = $this->can(P::MONGO_MANAGE);
$canInstall = $this->can(P::PACKAGES_MANAGE);
$canServices = $this->can(P::SERVICES_MANAGE);
$roles = ['readWrite' => 'Read & write', 'read' => 'Read only', 'dbAdmin' => 'DB admin', 'dbOwner' => 'Owner'];
$base = $this->url('mongo');
?>
<?php $this->start(); ?>MongoDB<?php $this->end('title'); ?>
<?php $this->start(); ?>Databases and users on the MongoDB server<?php $this->end('subtitle'); ?>

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

<ul class="nav nav-tabs wcp-tabs mb-3">
    <?php foreach ([
        'databases' => ['Databases', 'hdd-stack', count($overview['databases'])],
        'users' => ['Users', 'people', count($overview['users'])],
        'backups' => ['Backups', 'archive', null],
        'remotes' => ['Remote servers', 'hdd-network', null],
        'config' => ['Configuration', 'sliders2', null],
        'log' => ['Log', 'journal-text', null],
    ] as $key => [$label, $icon, $count]): ?>
        <li class="nav-item"><a class="nav-link<?= $section === $key ? ' active' : '' ?>" href="<?= e($base . '?section=' . $key) ?>"><i class="bi bi-<?= e($icon) ?> me-1" aria-hidden="true"></i><?= e($label) ?><?php if ($count !== null): ?> <span class="badge rounded-pill text-bg-light border ms-1"><?= (int) $count ?></span><?php endif; ?></a></li>
    <?php endforeach; ?>
</ul>

<?php if ($section === 'databases'): ?>
    <?php
    $backupCounts = [];
    foreach ($backups as $b) {
        $backupCounts[(string) $b['db']] = ($backupCounts[(string) $b['db']] ?? 0) + 1;
    }
    // The first user defined on each database (a database may have several;
    // the Users tab manages them all). Used for the per-row user/password.
    $usersByDb = [];
    foreach ($overview['users'] as $u) {
        $udb = (string) ($u['db'] ?? '');
        if ($udb !== '' && !isset($usersByDb[$udb])) {
            $usersByDb[$udb] = (string) ($u['user'] ?? '');
        }
    }
    $notes = $notes ?? [];
    ?>
    <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
        <div class="input-group input-group-sm" style="max-width:20rem">
            <span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span>
            <input class="form-control" type="search" placeholder="Filter databases" aria-label="Filter databases" data-wcp-filter="#mongo-dbs">
        </div>
        <span class="me-auto"></span>
        <?php if ($canManage): ?>
            <a class="btn btn-sm btn-outline-secondary" href="<?= e($base . '?section=remotes') ?>" title="Connect to another MongoDB server"><i class="bi bi-hdd-network me-1" aria-hidden="true"></i>Remote DB</a>
            <a class="btn btn-sm btn-outline-secondary" href="<?= e($base . '?section=remotes') ?>" title="Copy a database from another server"><i class="bi bi-cloud-download me-1" aria-hidden="true"></i>Get DB from server</a>
            <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-admin" title="Set the MongoDB administrator password"><i class="bi bi-shield-lock me-1" aria-hidden="true"></i>Root password</button>
            <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-config" title="Authentication and connection settings"><i class="bi bi-shield-check me-1" aria-hidden="true"></i>Security auth</button>
            <button class="btn btn-sm btn-outline-secondary" type="button" data-wcp-check-all="#mongo-dbs"><i class="bi bi-check2-square me-1" aria-hidden="true"></i>All</button>
            <button class="btn btn-sm btn-outline-secondary" type="button" data-wcp-check-none="#mongo-dbs">None</button>
            <form id="mongo-dbs-bulk" method="post" action="<?= e($this->url('mongo.database_bulk_drop')) ?>" data-wcp-busy data-wcp-confirm="Drop every selected database and all their data? This cannot be undone.">
                <?= $this->csrfField() ?>
                <button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash me-1" aria-hidden="true"></i>Delete selected</button>
            </form>
            <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-db"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Add database</button>
        <?php endif; ?>
    </div>
    <div class="card">
        <?php if ($overview['databases'] === []): ?>
            <div class="card-body small text-body-secondary">No databases yet.</div>
        <?php else: ?>
            <div class="wcp-dtable table-responsive">
                <table class="table align-middle mb-0" id="mongo-dbs">
                    <thead><tr><?php if ($canManage): ?><th style="width:2.5rem"></th><?php endif; ?><th>Name</th><th>User</th><th>Size</th><th>Backup</th><th>Position</th><th>Note</th><th class="text-end">Action</th></tr></thead>
                    <tbody>
                    <?php foreach ($overview['databases'] as $d): $dn = (string) ($d['name'] ?? ''); $bc = $backupCounts[$dn] ?? 0; $du = $usersByDb[$dn] ?? ''; $note = (string) ($notes[$dn] ?? ''); ?>
                        <tr>
                            <?php if ($canManage): ?><td><input class="form-check-input" type="checkbox" name="names[]" value="<?= e($dn) ?>" form="mongo-dbs-bulk" aria-label="Select <?= e($dn) ?>"></td><?php endif; ?>
                            <td class="font-monospace"><?= e($dn) ?></td>
                            <td class="small font-monospace"><?= $du !== '' ? e($du) : '<span class="text-body-secondary">—</span>' ?></td>
                            <td class="small text-body-secondary"><?= e(format_bytes((int) ($d['size'] ?? 0))) ?></td>
                            <td class="small">
                                <?php if ($bc > 0): ?>
                                    <a class="text-success-emphasis text-decoration-none" href="<?= e($base . '?section=backups') ?>" title="<?= (int) $bc ?> backup(s)"><i class="bi bi-check-circle me-1"></i><?= (int) $bc ?></a>
                                <?php elseif ($canManage): ?>
                                    <form class="d-inline" method="post" action="<?= e($this->url('mongo.backup')) ?>" data-wcp-busy data-wcp-confirm="Back up “<?= e($dn) ?>” now?">
                                        <?= $this->csrfField() ?><input type="hidden" name="db" value="<?= e($dn) ?>">
                                        <button class="btn btn-sm btn-link p-0 text-decoration-none text-warning-emphasis" title="No backup yet — back up now"><i class="bi bi-dash-circle me-1"></i>Back up</button>
                                    </form>
                                <?php else: ?>
                                    <span class="text-body-secondary">None</span>
                                <?php endif; ?>
                            </td>
                            <td class="small"><span class="badge rounded-pill text-bg-light border"><i class="bi bi-hdd me-1" aria-hidden="true"></i>Localhost</span></td>
                            <td class="small">
                                <?php if ($canManage): ?>
                                    <button class="btn btn-sm btn-link p-0 text-decoration-none text-truncate d-inline-block" style="max-width:12rem" type="button" data-bs-toggle="modal" data-bs-target="#mongo-note" data-db="<?= e($dn) ?>" data-note="<?= e($note) ?>" title="Edit note"><?= $note !== '' ? e($note) : '<span class="text-body-secondary"><i class="bi bi-pencil me-1"></i>Add note</span>' ?></button>
                                <?php else: ?>
                                    <span class="text-body-secondary"><?= $note !== '' ? e($note) : '—' ?></span>
                                <?php endif; ?>
                            </td>
                            <td class="text-end text-nowrap">
                                <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('mongo.browser')) ?>?db=<?= e(rawurlencode($dn)) ?>" title="Browse collections and documents"><i class="bi bi-table me-1"></i>Browse</a>
                                <?php if ($canManage): ?>
                                <?php if ($du !== ''): ?>
                                <button class="btn btn-sm btn-outline-secondary" type="button" title="Change the user's password" data-bs-toggle="modal" data-bs-target="#mongo-pw" data-user="<?= e($du) ?>" data-db="<?= e($dn) ?>"><i class="bi bi-key"></i></button>
                                <?php endif; ?>
                                <form class="d-inline" method="post" action="<?= e($this->url('mongo.database_drop')) ?>" data-wcp-confirm="Drop the database “<?= e($dn) ?>” and everything in it? This cannot be undone.">
                                    <?= $this->csrfField() ?>
                                    <input type="hidden" name="name" value="<?= e($dn) ?>"><input type="hidden" name="confirm" value="<?= e($dn) ?>">
                                    <button class="btn btn-sm btn-outline-danger" title="Drop"><i class="bi bi-trash"></i></button>
                                </form>
                                <?php endif; ?>
                            </td>
                        </tr>
                    <?php endforeach; ?>
                    <tr data-wcp-filter-empty class="d-none"><td colspan="<?= $canManage ? 8 : 7 ?>" class="small text-body-secondary">No databases match the filter.</td></tr>
                    </tbody>
                </table>
            </div>
        <?php endif; ?>
    </div>
<?php endif; ?>

<?php if ($section === 'users'): ?>
    <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
        <div class="input-group input-group-sm" style="max-width:22rem">
            <span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span>
            <input class="form-control" type="search" placeholder="Filter users" aria-label="Filter users" data-wcp-filter="#mongo-users">
        </div>
        <span class="me-auto"></span>
        <?php if ($canManage): ?>
            <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-user"><i class="bi bi-person-plus me-1" aria-hidden="true"></i>Add user</button>
        <?php endif; ?>
    </div>
    <div class="card">
        <?php if ($overview['users'] === []): ?>
            <div class="card-body small text-body-secondary">No users yet.</div>
        <?php else: ?>
            <div class="wcp-dtable table-responsive">
                <table class="table align-middle mb-0" id="mongo-users">
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
                                    <?= $this->csrfField() ?><input type="hidden" name="user" value="<?= e((string) ($u['user'] ?? '')) ?>"><input type="hidden" name="db" value="<?= e((string) ($u['db'] ?? '')) ?>">
                                    <button class="btn btn-sm btn-outline-danger" title="Drop"><i class="bi bi-trash"></i></button>
                                </form>
                            </td>
                            <?php endif; ?>
                        </tr>
                    <?php endforeach; ?>
                    <tr data-wcp-filter-empty class="d-none"><td colspan="<?= $canManage ? 4 : 3 ?>" class="small text-body-secondary">No users match the filter.</td></tr>
                    </tbody>
                </table>
            </div>
        <?php endif; ?>
    </div>
<?php endif; ?>

<?php if ($section === 'backups'): ?>
    <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
        <div class="input-group input-group-sm" style="max-width:22rem">
            <span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span>
            <input class="form-control" type="search" placeholder="Filter backups" aria-label="Filter backups" data-wcp-filter="#mongo-backups">
        </div>
        <span class="me-auto"></span>
        <?php if ($canManage && $overview['databases'] !== []): ?>
            <form method="post" action="<?= e($this->url('mongo.backup')) ?>" class="d-flex gap-2" data-wcp-busy>
                <?= $this->csrfField() ?>
                <select class="form-select form-select-sm" name="db" required aria-label="Database to back up" style="max-width:16rem">
                    <?php foreach ($overview['databases'] as $d): $dn = (string) ($d['name'] ?? ''); ?><option value="<?= e($dn) ?>"><?= e($dn) ?></option><?php endforeach; ?>
                </select>
                <button class="btn btn-sm btn-primary text-nowrap"><i class="bi bi-download me-1" aria-hidden="true"></i>Back up now</button>
            </form>
        <?php endif; ?>
    </div>
    <div class="card">
        <?php if ($backups === []): ?>
            <div class="card-body small text-body-secondary">No backups yet. Backups are gzip <code>mongodump</code> archives kept on this server; large ones run in the background.</div>
        <?php else: ?>
            <div class="wcp-dtable table-responsive">
                <table class="table align-middle mb-0" id="mongo-backups">
                    <thead><tr><th>Database</th><th>Archive</th><th>Size</th><th>Created (UTC)</th><?php if ($canManage): ?><th class="text-end">Action</th><?php endif; ?></tr></thead>
                    <tbody>
                    <?php foreach ($backups as $b): $bf = (string) $b['file']; $bdb = (string) $b['db']; ?>
                        <tr>
                            <td class="small font-monospace"><?= e($bdb) ?></td>
                            <td class="font-monospace small text-truncate" style="max-width:18rem"><?= e($bf) ?></td>
                            <td class="small text-body-secondary"><?= e(format_bytes((int) $b['size'])) ?></td>
                            <td class="small text-body-secondary"><?= e(((string) $b['created']) !== '' ? str_replace(['T', 'Z'], [' ', ''], substr((string) $b['created'], 0, 19)) : '—') ?></td>
                            <?php if ($canManage): ?>
                            <td class="text-end text-nowrap">
                                <form class="d-inline" method="post" action="<?= e($this->url('mongo.restore')) ?>" data-wcp-busy data-wcp-confirm="Restore “<?= e($bdb) ?>” from this backup? Documents sharing an _id with the archive are overwritten.">
                                    <?= $this->csrfField() ?><input type="hidden" name="file" value="<?= e($bf) ?>"><input type="hidden" name="db" value="<?= e($bdb) ?>"><input type="hidden" name="drop" value="0">
                                    <button class="btn btn-sm btn-outline-secondary" title="Restore"><i class="bi bi-arrow-counterclockwise me-1"></i>Restore</button>
                                </form>
                                <form class="d-inline" method="post" action="<?= e($this->url('mongo.backup_delete')) ?>" data-wcp-confirm="Delete this backup archive? This cannot be undone.">
                                    <?= $this->csrfField() ?><input type="hidden" name="file" value="<?= e($bf) ?>">
                                    <button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button>
                                </form>
                            </td>
                            <?php endif; ?>
                        </tr>
                    <?php endforeach; ?>
                    <tr data-wcp-filter-empty class="d-none"><td colspan="<?= $canManage ? 5 : 4 ?>" class="small text-body-secondary">No backups match the filter.</td></tr>
                    </tbody>
                </table>
            </div>
        <?php endif; ?>
    </div>
<?php endif; ?>

<?php if ($section === 'remotes'): $remotes = $remotes ?? []; ?>
    <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
        <div class="input-group input-group-sm" style="max-width:22rem">
            <span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span>
            <input class="form-control" type="search" placeholder="Filter servers" aria-label="Filter servers" data-wcp-filter="#mongo-remotes">
        </div>
        <span class="me-auto"></span>
        <?php if ($canManage): ?>
            <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-remote"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Add remote server</button>
        <?php endif; ?>
    </div>
    <p class="small text-body-secondary">Saved connections to other MongoDB servers. Use <strong>Get DB</strong> to copy one of a remote server's databases onto this server. Passwords are stored encrypted and are never shown again.</p>
    <div class="card">
        <?php if ($remotes === []): ?>
            <div class="card-body small text-body-secondary">No remote servers yet. Add one to copy a database from another MongoDB server.</div>
        <?php else: ?>
            <div class="wcp-dtable table-responsive">
                <table class="table align-middle mb-0" id="mongo-remotes">
                    <thead><tr><th>Name</th><th>Server</th><th>User</th><th>TLS</th><?php if ($canManage): ?><th class="text-end">Action</th><?php endif; ?></tr></thead>
                    <tbody>
                    <?php foreach ($remotes as $rm): ?>
                        <tr>
                            <td class="fw-medium"><?= e((string) $rm['name']) ?></td>
                            <td class="small font-monospace"><?= e((string) $rm['host']) ?>:<?= (int) $rm['port'] ?></td>
                            <td class="small font-monospace"><?= ((string) $rm['username']) !== '' ? e((string) $rm['username']) : '<span class="text-body-secondary">—</span>' ?></td>
                            <td class="small"><?= $rm['tls'] ? '<span class="badge text-bg-success-subtle text-success-emphasis border border-success-subtle">TLS</span>' : '<span class="text-body-secondary">off</span>' ?></td>
                            <?php if ($canManage): ?>
                            <td class="text-end text-nowrap">
                                <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-getdb" data-id="<?= (int) $rm['id'] ?>" data-name="<?= e((string) $rm['name']) ?>"><i class="bi bi-cloud-download me-1"></i>Get DB</button>
                                <form class="d-inline" method="post" action="<?= e($this->url('mongo.remote_test')) ?>" data-wcp-busy>
                                    <?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $rm['id'] ?>">
                                    <button class="btn btn-sm btn-outline-secondary" title="Test the connection"><i class="bi bi-plug"></i></button>
                                </form>
                                <form class="d-inline" method="post" action="<?= e($this->url('mongo.remote_delete')) ?>" data-wcp-confirm="Remove the saved connection “<?= e((string) $rm['name']) ?>”?">
                                    <?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $rm['id'] ?>">
                                    <button class="btn btn-sm btn-outline-danger" title="Remove"><i class="bi bi-trash"></i></button>
                                </form>
                            </td>
                            <?php endif; ?>
                        </tr>
                    <?php endforeach; ?>
                    <tr data-wcp-filter-empty class="d-none"><td colspan="<?= $canManage ? 5 : 4 ?>" class="small text-body-secondary">No servers match the filter.</td></tr>
                    </tbody>
                </table>
            </div>
        <?php endif; ?>
    </div>
<?php endif; ?>

<?php if ($section === 'config'): $config = $config ?? null; ?>
    <?php if ($config === null): ?>
        <div class="card"><div class="card-body small text-body-secondary">The configuration could not be read from the agent.</div></div>
    <?php else: ?>
    <div class="card">
        <div class="card-body">
            <div class="d-flex flex-wrap align-items-start gap-3">
                <dl class="row small mb-0 flex-grow-1" style="min-width:16rem">
                    <dt class="col-sm-4">Port</dt><dd class="col-sm-8 font-monospace"><?= e((string) $config['port']) ?></dd>
                    <dt class="col-sm-4">Bind IP</dt><dd class="col-sm-8 font-monospace"><?= e($config['bind_ip']) ?></dd>
                    <dt class="col-sm-4">Authentication</dt><dd class="col-sm-8"><?= $config['authorization'] ? '<span class="badge text-bg-success">Required</span>' : '<span class="badge text-bg-secondary">Disabled</span>' ?></dd>
                    <dt class="col-sm-4">Data path</dt><dd class="col-sm-8 font-monospace"><?= e($config['db_path'] !== '' ? $config['db_path'] : '—') ?></dd>
                    <dt class="col-sm-4">Config file</dt><dd class="col-sm-8 font-monospace"><?= e($config['path']) ?></dd>
                </dl>
                <?php if ($canManage): ?>
                <div class="d-flex flex-column gap-2">
                    <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-config"><i class="bi bi-sliders2 me-1" aria-hidden="true"></i>Edit settings</button>
                    <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-configfile"><i class="bi bi-file-earmark-code me-1" aria-hidden="true"></i>Edit config file</button>
                    <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#mongo-admin"><i class="bi bi-shield-lock me-1" aria-hidden="true"></i>Admin password</button>
                    <?php if ($status['auth']): ?>
                        <form method="post" action="<?= e($this->url('mongo.auth')) ?>" data-wcp-busy data-wcp-confirm="Turn OFF MongoDB authentication? Anyone who can reach the server will be able to connect without a password. MongoDB restarts.">
                            <?= $this->csrfField() ?><input type="hidden" name="enable" value="0">
                            <button class="btn btn-sm btn-outline-warning w-100"><i class="bi bi-unlock me-1" aria-hidden="true"></i>Disable auth</button>
                        </form>
                    <?php else: ?>
                        <form method="post" action="<?= e($this->url('mongo.auth')) ?>" data-wcp-busy data-wcp-confirm="Turn ON MongoDB authentication? Set an admin password first, or this is refused. MongoDB restarts and clients must then authenticate.">
                            <?= $this->csrfField() ?><input type="hidden" name="enable" value="1">
                            <button class="btn btn-sm btn-outline-secondary w-100"><i class="bi bi-shield-check me-1" aria-hidden="true"></i>Enable auth</button>
                        </form>
                    <?php endif; ?>
                </div>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <?php endif; ?>
<?php endif; ?>

<?php if ($section === 'log'): $log = $log ?? null; ?>
    <div class="card">
        <div class="card-header d-flex align-items-center">
            <span class="me-auto"><i class="bi bi-journal-text me-1" aria-hidden="true"></i>Log</span>
            <?php if ($log !== null): ?><span class="small text-body-secondary font-monospace"><?= e($log['path']) ?></span><?php endif; ?>
        </div>
        <div class="card-body">
            <?php if ($log === null): ?>
                <div class="small text-body-secondary">The log could not be read from the agent.</div>
            <?php elseif (trim($log['lines']) === ''): ?>
                <div class="small text-body-secondary">The log is empty.</div>
            <?php else: ?>
                <pre class="small border rounded p-2 mb-0 wcp-mongo-json"><?= e($log['lines']) ?></pre>
            <?php endif; ?>
            <a class="btn btn-sm btn-outline-secondary mt-2" href="<?= e($base . '?section=log') ?>"><i class="bi bi-arrow-clockwise me-1" aria-hidden="true"></i>Refresh</a>
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

<?= $this->modalStart('mongo-config', 'MongoDB settings') ?>
    <form method="post" action="<?= e($this->url('mongo.config')) ?>" data-wcp-busy data-wcp-confirm="Save the MongoDB configuration and restart the service? The previous configuration is restored automatically if it fails to start.">
        <div class="p-3">
            <?= $this->csrfField() ?>
            <div class="row g-3">
                <div class="col-sm-4">
                    <label class="form-label small mb-1" for="cfg-port">Port</label>
                    <input class="form-control form-control-sm" type="number" id="cfg-port" name="port" min="1" max="65535" value="<?= e((string) (($config ?? [])['port'] ?? 27017)) ?>">
                </div>
                <div class="col-sm-8">
                    <label class="form-label small mb-1" for="cfg-bind">Bind IP</label>
                    <input class="form-control form-control-sm font-monospace" id="cfg-bind" name="bind_ip" value="<?= e((($config ?? [])['bind_ip'] ?? '127.0.0.1')) ?>" placeholder="127.0.0.1">
                    <div class="form-text">Comma-separated. Keep <code>127.0.0.1</code> unless remote access is intended.</div>
                </div>
                <div class="col-12">
                    <div class="form-check form-switch">
                        <input class="form-check-input" type="checkbox" role="switch" id="cfg-auth" name="authorization" value="1"<?= (($config ?? [])['authorization'] ?? false) ? ' checked' : '' ?>>
                        <label class="form-check-label small" for="cfg-auth">Require authentication (<code>security.authorization</code>)</label>
                    </div>
                </div>
            </div>
            <button class="btn btn-sm btn-primary mt-3" type="submit">Save &amp; restart</button>
        </div>
    </form>
<?= $this->modalEnd() ?>

<?= $this->modalStart('mongo-configfile', 'MongoDB configuration file') ?>
    <form method="post" action="<?= e($this->url('mongo.config_file')) ?>" data-wcp-busy data-wcp-confirm="Save the whole MongoDB configuration file and restart the service? If it fails to start, the previous file is restored automatically.">
        <div class="p-3">
            <?= $this->csrfField() ?>
            <textarea class="form-control font-monospace small" name="content" rows="18" spellcheck="false" aria-label="MongoDB configuration file"><?= e((($config ?? [])['raw'] ?? '')) ?></textarea>
            <div class="form-text">Editing the whole <code><?= e((($config ?? [])['path'] ?? '/etc/mongod.conf')) ?></code>. MongoDB has no offline check, so WhaCP restarts to apply it and restores the previous file if it will not start.</div>
            <button class="btn btn-outline-primary btn-sm mt-3" type="submit"><i class="bi bi-save me-1" aria-hidden="true"></i>Save &amp; restart</button>
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

<?= $this->modalStart('mongo-note', 'Database note', false, '') ?>
    <form method="post" action="<?= e($this->url('mongo.note')) ?>" data-wcp-busy>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <input type="hidden" name="name" id="mnote-db">
            <label class="form-label" for="mnote-text">Note for <code data-mongo-note-db></code></label>
            <textarea class="form-control" id="mnote-text" name="note" rows="3" maxlength="500" placeholder="A short reminder about this database (purpose, owner, …)"></textarea>
            <div class="form-text">Stored in WhaCP, visible to administrators only. Leave empty to clear.</div>
            <button class="btn btn-primary btn-sm mt-3" type="submit">Save note</button>
        </div>
    </form>
<?= $this->modalEnd() ?>

<?= $this->modalStart('mongo-remote', 'Add remote MongoDB server') ?>
    <form method="post" action="<?= e($this->url('mongo.remote_add')) ?>" data-wcp-busy>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <label class="form-label" for="mrm-name">Name</label>
            <input class="form-control mb-2" id="mrm-name" name="name" maxlength="64" required placeholder="Old production server">
            <div class="row g-2">
                <div class="col-8"><label class="form-label" for="mrm-host">Host</label><input class="form-control font-monospace" id="mrm-host" name="host" required placeholder="db.example.com"></div>
                <div class="col-4"><label class="form-label" for="mrm-port">Port</label><input class="form-control" id="mrm-port" name="port" type="number" min="1" max="65535" value="27017"></div>
            </div>
            <div class="row g-2 mt-1">
                <div class="col-6"><label class="form-label" for="mrm-user">User</label><input class="form-control font-monospace" id="mrm-user" name="username" autocomplete="off" placeholder="(optional)"></div>
                <div class="col-6"><label class="form-label" for="mrm-authdb">Auth database</label><input class="form-control font-monospace" id="mrm-authdb" name="auth_db" value="admin"></div>
            </div>
            <label class="form-label mt-2" for="mrm-pw">Password</label>
            <input class="form-control font-monospace" id="mrm-pw" name="password" type="password" autocomplete="off" placeholder="(optional)">
            <div class="form-check form-switch mt-2">
                <input class="form-check-input" type="checkbox" role="switch" id="mrm-tls" name="tls" value="1">
                <label class="form-check-label small" for="mrm-tls">Connect with TLS/SSL</label>
            </div>
            <div class="form-text">The connection is tested before it is saved. The password is stored encrypted and never shown again.</div>
            <button class="btn btn-primary btn-sm mt-3" type="submit">Test &amp; save</button>
        </div>
    </form>
<?= $this->modalEnd() ?>

<?= $this->modalStart('mongo-getdb', 'Get a database from a server') ?>
    <form method="post" action="<?= e($this->url('mongo.remote_pull')) ?>" data-wcp-busy data-mongo-getdb-form>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <input type="hidden" name="id" data-mongo-getdb-id>
            <p class="small text-body-secondary mb-2">Copying from <strong data-mongo-getdb-name></strong>. The selected database is dumped from the remote server and restored onto this one.</p>
            <label class="form-label" for="mgd-remote">Remote database</label>
            <select class="form-select mb-2" id="mgd-remote" name="remote_db" data-mongo-getdb-select required>
                <option value="">Loading…</option>
            </select>
            <label class="form-label" for="mgd-target">Save on this server as</label>
            <input class="form-control font-monospace mb-2" id="mgd-target" name="target_db" data-mongo-getdb-target pattern="[A-Za-z0-9_][A-Za-z0-9_-]*" maxlength="63" placeholder="(same name)">
            <div class="form-check mb-2">
                <input class="form-check-input" type="checkbox" id="mgd-drop" name="drop" value="1">
                <label class="form-check-label small" for="mgd-drop">Drop the local database first (replace it entirely)</label>
            </div>
            <div class="small text-danger d-none" data-mongo-getdb-error></div>
            <button class="btn btn-primary btn-sm mt-2" type="submit" data-mongo-getdb-ok><i class="bi bi-cloud-download me-1"></i>Copy database</button>
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

<?php if ($canManage): ?>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/mongoremotes.js')) ?>" defer data-mongo-remote-databases-url="<?= e($this->url('mongo.remote_databases')) ?>" data-csrf="<?= e($this->csrfToken()) ?>"></script><?php $this->end('scripts'); ?>
<?php endif; ?>
