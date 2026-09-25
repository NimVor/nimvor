<?php
/** @var \WhaCP\View\View $this */
use WhaCP\Security\Permissions as P;
use WhaCP\Services\DatabaseService;

$canManage = $this->can(P::DATABASES_MANAGE);
$returnTo = $returnTo ?? '/databases';
?>
<?= $this->partial('partials/secret', ['secret' => $secret]) ?>
<?php if ($live === null): ?>
    <div class="alert alert-warning small"><i class="bi bi-plug me-1"></i>The database server could not be queried (is MariaDB/MySQL running and reachable as root over its socket?). Showing WhaCP records only.</div>
<?php endif; ?>

<div class="row g-3">
    <div class="col-xl-8">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-database me-1"></i>Databases</div>
            <?php if ($managed === []): ?>
                <div class="card-body text-body-secondary small">No databases yet.</div>
            <?php else: ?>
                <div class="table-responsive">
                    <table class="table align-middle mb-0 wcp-table">
                        <thead><tr><th>Name</th><th>Website</th><th class="text-end">Size</th><th>Access</th><th></th></tr></thead>
                        <tbody>
                        <?php foreach ($managed as $d): $sz = $sizes[$d['name']] ?? null; ?>
                            <tr>
                                <td class="fw-semibold"><?= e($d['name']) ?></td>
                                <td class="small"><?= e($d['website'] ?? '—') ?></td>
                                <td class="text-end small"><?= $sz !== null ? e(format_bytes((int) $sz['size_bytes'])) . ' · ' . (int) $sz['tables'] . ' tables' : '<span class="text-body-secondary">—</span>' ?></td>
                                <td class="small">
                                    <?php foreach ($d['grants'] as $g): ?>
                                        <div><code><?= e($g['username']) ?></code> <span class="text-body-secondary"><?= e(DatabaseService::PRIVILEGES[$g['privileges']] ?? $g['privileges']) ?></span></div>
                                    <?php endforeach; ?>
                                    <?php if ($canManage && $dbUsers !== []): ?>
                                        <form method="post" action="<?= e($this->url('databases.grant', ['id' => (int) $d['id']])) ?>" class="d-flex gap-1 mt-1" data-wcp-busy>
                                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                                            <select class="form-select form-select-sm" name="user_id" aria-label="User"><?php foreach ($dbUsers as $u): ?><option value="<?= (int) $u['id'] ?>"><?= e($u['username'] . '@' . $u['host']) ?></option><?php endforeach; ?></select>
                                            <select class="form-select form-select-sm" name="privileges" aria-label="Privileges"><?php foreach (DatabaseService::PRIVILEGES as $k => $l): ?><option value="<?= e($k) ?>"><?= e($l) ?></option><?php endforeach; ?><option value="none">No access</option></select>
                                            <button class="btn btn-sm btn-outline-secondary" type="submit">Set</button>
                                        </form>
                                    <?php endif; ?>
                                </td>
                                <td class="text-end text-nowrap">
                                    <?php if ($canManage): ?>
                                        <a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('databases.browser', ['id' => (int) $d['id']])) ?>" title="Browse tables and run SQL"><i class="bi bi-table"></i> Browse</a>
                                        <form method="post" action="<?= e($this->url('databases.dump', ['id' => (int) $d['id']])) ?>" class="d-inline"><?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><button class="btn btn-sm btn-outline-secondary" title="Dump to the backup directory"><i class="bi bi-download"></i></button></form>
                                        <form method="post" action="<?= e($this->url('databases.drop', ['id' => (int) $d['id']])) ?>" class="d-inline" data-wcp-confirm="Delete the database <?= e($d['name']) ?> and all of its data? This cannot be undone." data-wcp-confirm-word="<?= e($d['name']) ?>">
                                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><input type="hidden" name="confirm" value="<?= e($d['name']) ?>">
                                            <button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button>
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

        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-person-lock me-1"></i>Database users</div>
            <?php if ($dbUsers === []): ?>
                <div class="card-body text-body-secondary small">No database users yet.</div>
            <?php else: ?>
                <div class="table-responsive">
                    <table class="table align-middle mb-0 wcp-table">
                        <thead><tr><th>User</th><th>Host</th><th>Password</th><th></th></tr></thead>
                        <tbody>
                        <?php foreach ($dbUsers as $u): ?>
                            <tr>
                                <td class="fw-semibold"><?= e($u['username']) ?></td>
                                <td><code><?= e($u['host']) ?></code></td>
                                <td class="small"><?= $u['stored'] ? 'stored encrypted' : '<span class="text-body-secondary">not stored</span>' ?></td>
                                <td class="text-end text-nowrap">
                                    <?php if ($canManage): ?>
                                        <?php if ($u['stored']): ?>
                                            <form method="post" action="<?= e($this->url('dbusers.reveal', ['id' => (int) $u['id']])) ?>" class="d-inline"><?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><button class="btn btn-sm btn-outline-secondary" title="Show password"><i class="bi bi-eye"></i></button></form>
                                        <?php endif; ?>
                                        <form method="post" action="<?= e($this->url('dbusers.password', ['id' => (int) $u['id']])) ?>" class="d-inline" data-wcp-confirm="Generate a new password for <?= e($u['username']) ?>? Applications using the old password will stop working until updated.">
                                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><input type="hidden" name="store_password" value="<?= $u['stored'] ? '1' : '0' ?>">
                                            <button class="btn btn-sm btn-outline-secondary" title="New password"><i class="bi bi-arrow-repeat"></i></button>
                                        </form>
                                        <form method="post" action="<?= e($this->url('dbusers.delete', ['id' => (int) $u['id']])) ?>" class="d-inline" data-wcp-confirm="Remove the database user <?= e($u['username']) ?>?">
                                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>"><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button>
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
        <?php if ($canManage): ?>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-plus-circle me-1"></i>New database</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('databases.store')) ?>" data-wcp-busy novalidate>
                        <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                        <label class="form-label small" for="db-name">Name</label>
                        <div class="input-group input-group-sm mb-2">
                            <?php if ($prefix !== ''): ?><span class="input-group-text"><?= e($prefix) ?></span><?php endif; ?>
                            <input class="form-control<?= isset($errors['name']) ? ' is-invalid' : '' ?>" id="db-name" name="name" value="<?= e($old['name'] ?? '') ?>" maxlength="48" required>
                        </div>
                        <?php if (isset($errors['name'])): ?><div class="text-danger small mb-2"><?= e($errors['name']) ?></div><?php endif; ?>
                        <?php if (isset($site)): ?>
                            <input type="hidden" name="website_id" value="<?= (int) $site['id'] ?>">
                        <?php else: ?>
                            <label class="form-label small" for="db-site">Website (optional)</label>
                            <select class="form-select form-select-sm mb-2" id="db-site" name="website_id"><option value="">—</option><?php foreach ($websites as $w): ?><option value="<?= (int) $w['id'] ?>"><?= e($w['domain']) ?></option><?php endforeach; ?></select>
                        <?php endif; ?>
                        <div class="form-check form-switch small"><input type="hidden" name="create_user" value="0"><input class="form-check-input" type="checkbox" name="create_user" value="1" id="db-cu" checked><label class="form-check-label" for="db-cu">Create a user with full access</label></div>
                        <div class="form-check form-switch small mb-3"><input type="hidden" name="store_password" value="0"><input class="form-check-input" type="checkbox" name="store_password" value="1" id="db-sp"><label class="form-check-label" for="db-sp">Keep the password (encrypted) so it can be shown again</label></div>
                        <button class="btn btn-sm btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none" aria-hidden="true"></span> Create database</button>
                    </form>
                </div>
            </div>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-person-plus me-1"></i>New database user</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('dbusers.store')) ?>" data-wcp-busy novalidate>
                        <?= $this->csrfField() ?><input type="hidden" name="return_to" value="<?= e($returnTo) ?>">
                        <div class="input-group input-group-sm mb-2">
                            <?php if ($prefix !== ''): ?><span class="input-group-text"><?= e($prefix) ?></span><?php endif; ?>
                            <input class="form-control<?= isset($errors['username']) ? ' is-invalid' : '' ?>" name="username" placeholder="username" maxlength="24" required aria-label="Username">
                        </div>
                        <?php if (isset($errors['username'])): ?><div class="text-danger small mb-2"><?= e($errors['username']) ?></div><?php endif; ?>
                        <?php if ($prefix === ''): ?>
                            <input class="form-control form-control-sm mb-2" name="host" value="localhost" aria-label="Host" title="localhost, an IP address, a network or %">
                        <?php endif; ?>
                        <div class="form-check form-switch small mb-3"><input type="hidden" name="store_password" value="0"><input class="form-check-input" type="checkbox" name="store_password" value="1" id="u-sp"><label class="form-check-label" for="u-sp">Keep the password (encrypted)</label></div>
                        <button class="btn btn-sm btn-outline-primary" type="submit">Create user</button>
                    </form>
                </div>
            </div>
        <?php endif; ?>
        <div class="card">
            <div class="card-header"><i class="bi bi-plug me-1"></i>Connection</div>
            <div class="card-body small">
                <dl class="wcp-dl mb-0">
                    <dt>Host</dt><dd><code>localhost</code></dd>
                    <dt>Port</dt><dd><code>3306</code></dd>
                    <?php if (!empty($live['version'])): ?><dt>Server</dt><dd><?= e($live['version']) ?></dd><?php endif; ?>
                </dl>
            </div>
        </div>
    </div>
</div>
