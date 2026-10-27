<?php
/**
 * @var \NimVor\View\View $this
 * @var bool $admin
 * @var list<array<string, mixed>> $backups
 * @var list<array<string, mixed>> $serverBackups
 * @var list<array<string, mixed>> $schedules
 * @var list<array<string, mixed>> $storages
 * @var array<string, mixed>|null $keyInfo
 * @var list<array<string, mixed>> $accountFiles
 * @var list<array<string, mixed>> $accounts
 * @var bool $isAdminRole
 */
use NimVor\Services\BackupService;

$canManage = $this->can(\NimVor\Security\Permissions::BACKUPS_MANAGE);
$errorsAll = (array) ($errors ?? []);
$old ??= [];
// A refused storage form reopens its window: an edit (storage_id) or a new one.
$editFailed = isset($old['storage_id']) ? (int) $old['storage_id'] : 0;
$addFailed = $old !== [] && isset($old['type']) && $editFailed === 0;
$activeCount = count(array_filter($storages, static fn (array $s): bool => $s['active']));
$whereLabel = ['backups' => 'Made here', 'uploaded' => 'Uploaded', 'folder' => 'Server folder'];
?>
<?php $this->start(); ?>Backups<?php $this->end('title'); ?>
<?php $this->start(); ?>Website, server and account backups<?php $this->end('subtitle'); ?>

<?php if (!$admin): ?>
<div class="card">
    <div class="card-header"><i class="bi bi-globe2 me-1"></i>Website backups</div>
    <?= $this->partial('backups/_list', ['backups' => $backups, 'canManage' => $canManage, 'showSite' => true]) ?>
    <div class="card-footer small text-body-secondary">Create, restore and schedule website backups from each website's <strong>Backups</strong> tab.</div>
</div>
<?php else: ?>
<div class="nim-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#websites" aria-controls="websites" aria-selected="true"><i class="bi bi-globe2 me-1"></i>Website backups <span class="badge text-bg-light border"><?= count($backups) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#server" aria-controls="server" aria-selected="false"><i class="bi bi-hdd-rack me-1"></i>Server configuration <span class="badge text-bg-light border"><?= count($serverBackups) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#storages" aria-controls="storages" aria-selected="false"><i class="bi bi-cloud me-1"></i>Remote storage <span class="badge text-bg-light border"><?= $activeCount ?>/<?= count($storages) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#accounts" aria-controls="accounts" aria-selected="false"><i class="bi bi-person-vcard me-1"></i>Account backups <span class="badge text-bg-light border"><?= count($accountFiles) ?></span></button>
</div>
<div class="tab-content">

<div class="tab-pane fade show active" id="websites" role="tabpanel">
    <div class="card">
        <?= $this->partial('backups/_list', ['backups' => $backups, 'canManage' => $canManage, 'showSite' => true]) ?>
        <div class="card-footer small text-body-secondary">Create, restore and schedule website backups from each website's <strong>Backups</strong> tab. A whole account (all its websites, databases, DNS zones and cron jobs) is backed up under <strong>Account backups</strong>.</div>
    </div>
</div>

<div class="tab-pane fade" id="server" role="tabpanel">
    <?php if ($canManage): ?>
    <div class="nim-actionbar mb-3 px-0">
        <div class="small text-body-secondary">Nginx, PHP, MySQL, Redis, certificates, SSH/firewall and NimVor configuration for disaster recovery. Always encrypted: it contains private keys.</div>
        <div class="nim-actionbar-end">
            <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-toggle="modal" data-bs-target="#server-schedule"><i class="bi bi-calendar-check me-1"></i>Backup schedule</button>
            <button type="button" class="btn btn-sm btn-primary" data-bs-toggle="modal" data-bs-target="#server-backup"><i class="bi bi-plus-lg me-1"></i>Back up the server configuration</button>
        </div>
    </div>
    <?php endif; ?>
    <div class="card mb-3">
        <div class="card-header"><i class="bi bi-hdd-rack me-1"></i>Server configuration backups</div>
        <?= $this->partial('backups/_list', ['backups' => $serverBackups, 'canManage' => $canManage]) ?>
    </div>
    <div class="row g-3">
        <div class="col-lg-6">
            <div class="card h-100">
                <div class="card-header d-flex align-items-center"><i class="bi bi-calendar-check me-1"></i>Schedules
                    <?php if ($canManage): ?><button type="button" class="btn btn-sm btn-link ms-auto p-0" data-bs-toggle="modal" data-bs-target="#server-schedule">Add</button><?php endif; ?></div>
                <div class="card-body small">
                    <?php if ($schedules === []): ?><span class="text-body-secondary">No automatic backup. Add a schedule to back up the server configuration or every website every day or week.</span><?php endif; ?>
                    <?= $this->partial('backups/_schedules', ['schedules' => $schedules, 'deleteRoute' => 'backups.schedules.delete', 'routeParams' => []]) ?>
                </div>
            </div>
        </div>
        <div class="col-lg-6">
            <div class="card h-100" id="key">
                <div class="card-header"><i class="bi bi-key me-1"></i>Encryption key</div>
                <div class="card-body small">
                    <?php if ($keyInfo === null): ?>
                        <span class="text-body-secondary">Agent not reachable.</span>
                    <?php elseif (empty($keyInfo['exists'])): ?>
                        No key yet: it is created with the first encrypted backup at <code><?= e($keyInfo['path']) ?></code>. On a new server, import the key of the old one first to restore its backups.
                    <?php else: ?>
                        Key ID <code><?= e($keyInfo['key_id']) ?></code> in <code><?= e($keyInfo['path']) ?></code>.
                    <?php endif; ?>
                    <div class="alert alert-warning mt-2 mb-2 p-2"><i class="bi bi-exclamation-triangle me-1"></i>Encrypted backups cannot be restored without this key, for example on a new server after a disk failure. Download it, protected by a passphrase, and keep the file and the passphrase in separate safe places.</div>
                    <?php if ($isAdminRole): ?>
                    <div class="d-flex flex-wrap gap-2">
                        <?php if (!empty($keyInfo['exists'])): ?><button type="button" class="btn btn-sm btn-outline-primary" data-bs-toggle="modal" data-bs-target="#key-download"><i class="bi bi-download me-1"></i>Download the key</button><?php endif; ?>
                        <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-toggle="modal" data-bs-target="#key-import"><i class="bi bi-upload me-1"></i>Import a key</button>
                    </div>
                    <?php else: ?>
                    <div class="text-body-secondary">Only administrators can download or import the key.</div>
                    <?php endif; ?>
                </div>
            </div>
        </div>
    </div>
</div>

<div class="tab-pane fade" id="storages" role="tabpanel">
    <?php if ($canManage): ?>
    <div class="nim-actionbar mb-3 px-0">
        <div class="small text-body-secondary">Keep copies off this server. Several storages can be connected; test one and turn it on to offer it for new backups and schedules.</div>
        <div class="nim-actionbar-end"><button type="button" class="btn btn-sm btn-primary" data-bs-toggle="modal" data-bs-target="#storage-add"><i class="bi bi-plus-lg me-1"></i>Add storage</button></div>
    </div>
    <?php endif; ?>
    <div class="nim-panel">
        <div class="nim-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Storage</th><th>Where</th><th>Last test</th><th>State</th><th class="text-end">Operate</th></tr></thead>
                <tbody>
                <?php foreach ($storages as $s): $sid = (int) $s['id']; ?>
                    <tr data-storage="<?= e($s['name']) ?>">
                        <td><strong><?= e($s['name']) ?></strong><div class="small text-body-secondary"><?= e(BackupService::STORAGE_TYPES[$s['kind']] ?? $s['kind']) ?></div></td>
                        <td class="small text-break"><?= e($s['where']) ?><?= $s['prefix'] !== '' ? '<div class="text-body-secondary">' . e($s['prefix']) . '/</div>' : '' ?></td>
                        <td class="small"><?php if ($s['tested_at'] === null): ?><span class="nim-muted">never</span><?php elseif ($s['test_ok']): ?><span class="nim-state is-ok">Passed</span><div class="nim-muted"><?= e(substr((string) $s['tested_at'], 0, 16)) ?></div><?php else: ?><span class="nim-state is-error">Failed</span><div class="nim-muted"><?= e(substr((string) $s['tested_at'], 0, 16)) ?></div><div class="text-danger-emphasis text-break"><?= e($s['test_error']) ?></div><?php endif; ?></td>
                        <td><?= $s['active'] ? '<span class="nim-state is-ok">On</span>' : '<span class="nim-state is-off">Off</span>' ?></td>
                        <td class="text-end text-nowrap">
                            <?php if ($canManage): ?>
                                <form class="d-inline" method="post" action="<?= e($this->url('backups.storages.test', ['storage' => $sid])) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Test</button></form>
                                <form class="d-inline" method="post" action="<?= e($this->url('backups.storages.active', ['storage' => $sid])) ?>"><?= $this->csrfField() ?><input type="hidden" name="active" value="<?= $s['active'] ? '0' : '1' ?>"><button class="btn btn-sm <?= $s['active'] ? 'btn-outline-secondary' : 'btn-outline-primary' ?>"<?= !$s['active'] && !$s['test_ok'] ? ' disabled title="Test it first"' : '' ?>><?= $s['active'] ? 'Turn off' : 'Turn on' ?></button></form>
                                <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-toggle="modal" data-bs-target="#storage-edit-<?= $sid ?>">Edit</button>
                                <form class="d-inline" method="post" action="<?= e($this->url('backups.storages.scan', ['storage' => $sid])) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary" title="Find backups in this storage (for example after moving to a new server)"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-search"></i></button></form>
                                <form class="d-inline" method="post" action="<?= e($this->url('backups.storages.delete', ['storage' => $sid])) ?>" data-nim-confirm="Remove the storage <?= e($s['name']) ?>? Backups already uploaded stay there."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" aria-label="Remove <?= e($s['name']) ?>"><i class="bi bi-trash"></i></button></form>
                            <?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                <?php if ($storages === []): ?>
                    <tr><td colspan="5" class="text-center py-4 nim-muted">No remote storage. Add an S3-compatible bucket (AWS S3, Backblaze B2, Wasabi, Cloudflare R2, MinIO…), an SFTP or FTPS server, Google Drive or a mounted disk to keep copies off this server.</td></tr>
                <?php endif; ?>
                </tbody>
            </table>
        </div>
    </div>
</div>

<div class="tab-pane fade" id="accounts" role="tabpanel">
    <?php if ($canManage): ?>
    <div class="nim-actionbar mb-3 px-0">
        <div class="small text-body-secondary">One file per hosting account: its websites' files and databases, database users, DNS zones and cron jobs. Restore it here or on another server, also after the account was terminated.</div>
        <div class="nim-actionbar-end">
            <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-toggle="modal" data-bs-target="#account-upload"><i class="bi bi-upload me-1"></i>Upload a backup file</button>
            <button type="button" class="btn btn-sm btn-primary" data-bs-toggle="modal" data-bs-target="#account-backup"><i class="bi bi-plus-lg me-1"></i>Back up an account</button>
        </div>
    </div>
    <?php endif; ?>
    <div class="nim-panel" data-nim-dt>
        <div class="nim-actionbar"><div class="fw-semibold">Account backup files <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($accountFiles) ?></span></div>
            <div class="nim-actionbar-end"><div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search account backups" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div></div>
        <div class="nim-dtable table-responsive">
            <table class="table align-middle">
                <thead><tr><th>Account</th><th>Made</th><th>Websites</th><th class="text-end">Size</th><th>File</th><th class="text-end">Operate</th></tr></thead>
                <tbody>
                <?php foreach ($accountFiles as $f): $m = (array) ($f['manifest'] ?? []); ?>
                    <tr data-nim-row data-account-file="<?= e((string) ($m['username'] ?? '')) ?>">
                        <td class="fw-semibold"><?= e((string) ($m['username'] ?? '?')) ?><?php if (!empty($m['label'])): ?><div class="small nim-muted fw-normal"><?= e((string) $m['label']) ?></div><?php endif; ?></td>
                        <td class="small text-nowrap"><?= e(substr((string) ($m['created_at'] ?? $f['modified']), 0, 16)) ?></td>
                        <td class="small"><?= e(implode(', ', array_column((array) ($m['sites'] ?? []), 'domain')) ?: '—') ?></td>
                        <td class="text-end small text-nowrap"><?= e(format_bytes((int) $f['size'])) ?></td>
                        <td class="small"><span class="badge text-bg-light border"><?= e($whereLabel[$f['where']] ?? $f['where']) ?></span> <span class="font-monospace text-break"><?= e((string) $f['path']) ?></span>
                            <?php if (($f['problem'] ?? '') !== ''): ?><div class="text-danger-emphasis"><i class="bi bi-exclamation-triangle me-1"></i><?= e((string) $f['problem']) ?></div><?php endif; ?></td>
                        <td class="text-end text-nowrap">
                            <?php if ($canManage): ?>
                                <?php if (($f['problem'] ?? '') === ''): ?><a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('backups.accounts.preview', [], ['path' => (string) $f['path']])) ?>">Restore…</a><?php endif; ?>
                                <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('backups.accounts.download', [], ['path' => (string) $f['path']])) ?>" title="Download" aria-label="Download"><i class="bi bi-download"></i></a>
                                <?php if (!empty($f['removable'])): ?><form class="d-inline" method="post" action="<?= e($this->url('backups.accounts.delete')) ?>" data-nim-confirm="Delete the account backup file <?= e((string) $f['name']) ?>?"><?= $this->csrfField() ?><input type="hidden" name="path" value="<?= e((string) $f['path']) ?>"><button class="btn btn-sm btn-outline-danger" aria-label="Delete"><i class="bi bi-trash"></i></button></form><?php endif; ?>
                            <?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                <tr class="<?= $accountFiles === [] ? '' : 'd-none' ?>" data-nim-dt-empty><td colspan="6" class="text-center py-4 nim-muted">No account backup yet. Back up an account, upload a file, or copy one to a folder such as /backup or /mnt/… (they are listed here).</td></tr>
                </tbody>
            </table>
        </div>
        <div class="nim-pager" data-nim-dt-pager></div>
    </div>
    <p class="small text-body-secondary mt-2 mb-0">Files are listed from this server's account backups, uploads, and from <code>/backup</code>, <code>/backups</code>, <code>/srv/backup(s)</code>, <code>/mnt</code> and <code>/media</code> (two folder levels). A file can be restored when it was made with this server's backup key: on another server, import that key first (Server configuration → Encryption key). Not included: email, SSL certificates (issued again by AutoSSL) and FTP accounts.</p>
</div>

</div>

<?php if ($canManage): ?>
<?= $this->modalStart('server-backup', 'Back up the server configuration', false, '') ?>
    <form method="post" action="<?= e($this->url('backups.server')) ?>" data-nim-busy>
        <?= $this->csrfField() ?>
        <label class="form-label small" for="sv-label">Label (optional)</label>
        <input class="form-control form-control-sm mb-2" id="sv-label" name="label" maxlength="100">
        <?= $this->partial('backups/_options', ['storages' => $storages, 'prefix' => 'sv', 'alwaysEncrypted' => true]) ?>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Start backup</button></div>
    </form>
<?= $this->modalEnd() ?>
<?= $this->modalStart('server-schedule', 'Backup schedule', false, '') ?>
    <?= $this->partial('backups/_schedule_form', ['action' => $this->url('backups.schedules.store'), 'site' => false, 'storages' => $storages]) ?>
<?= $this->modalEnd() ?>
<?= $this->modalStart('storage-add', 'Add remote storage', $addFailed) ?>
    <?= $this->partial('backups/_storage_form', ['storage' => null, 'values' => $addFailed ? $old : [], 'errors' => $addFailed ? $errorsAll : [], 'px' => 'sa-']) ?>
<?= $this->modalEnd() ?>
<?php foreach ($storages as $s): $sid = (int) $s['id']; ?>
<?= $this->modalStart('storage-edit-' . $sid, 'Edit ' . $s['name'], $editFailed === $sid) ?>
    <?= $this->partial('backups/_storage_form', ['storage' => $s, 'values' => $editFailed === $sid ? $old + ['type' => $s['kind']] : $s['fields'] + ['name' => $s['name']], 'errors' => $editFailed === $sid ? $errorsAll : [], 'px' => 'se' . $sid . '-']) ?>
<?= $this->modalEnd() ?>
<?php endforeach; ?>
<?= $this->modalStart('account-backup', 'Back up an account', false, '') ?>
    <form method="post" action="<?= e($this->url('backups.accounts.create')) ?>" data-nim-busy>
        <?= $this->csrfField() ?>
        <label class="form-label small" for="ab-user">Account</label>
        <select class="form-select form-select-sm mb-2" id="ab-user" name="user_id" required>
            <?php foreach ($accounts as $a): ?><option value="<?= (int) $a['id'] ?>"><?= e($a['username']) ?> (<?= (int) $a['websites'] ?> website<?= (int) $a['websites'] === 1 ? '' : 's' ?>)</option><?php endforeach; ?>
        </select>
        <label class="form-label small" for="ab-label">Label (optional)</label>
        <input class="form-control form-control-sm" id="ab-label" name="label" maxlength="100" placeholder="e.g. before termination">
        <div class="form-text">Every website of the account is backed up (files and databases, encrypted), then one account file is written. It is listed here; download it to keep it elsewhere.</div>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"<?= $accounts === [] ? ' disabled' : '' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Back up</button></div>
    </form>
<?= $this->modalEnd() ?>
<?= $this->modalStart('account-upload', 'Upload an account backup file', false, '') ?>
    <div data-nim-account-upload data-url="<?= e($this->url('backups.accounts.upload')) ?>" data-csrf="<?= e($this->csrfToken()) ?>" data-restore-url="<?= e($this->url('backups.accounts.preview')) ?>">
        <label class="form-label small" for="au-file">File (<code>.nimvor-account</code>)</label>
        <input class="form-control form-control-sm" type="file" id="au-file" accept=".nimvor-account" data-nim-account-file>
        <div class="progress mt-3 d-none" role="progressbar" aria-label="Upload progress" data-nim-account-progress><div class="progress-bar" data-width="0"></div></div>
        <div class="small mt-2" data-nim-account-status aria-live="polite"></div>
        <div class="form-text">Uploaded in parts; large files take a while. A file can also be copied to a server folder such as /backup instead.</div>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Close</button><button type="button" class="btn btn-sm btn-primary" data-nim-account-start disabled>Upload</button></div>
    </div>
<?= $this->modalEnd() ?>
<?php endif; ?>
<?php if ($isAdminRole): ?>
<?= $this->modalStart('key-download', 'Download the backup key', false, '') ?>
    <form method="post" action="<?= e($this->url('backups.key.download')) ?>" autocomplete="off" data-nim-noajax>
        <?= $this->csrfField() ?>
        <p class="small">The file holds the key encrypted with the passphrase you choose. Keep the file and the passphrase in separate safe places: with both, anyone can read your encrypted backups. Every download is recorded in the audit log.</p>
        <label class="form-label small" for="kd-pass">Passphrase (at least 12 characters)</label>
        <input class="form-control form-control-sm mb-2" type="password" id="kd-pass" name="passphrase" minlength="12" maxlength="1024" autocomplete="new-password" required>
        <label class="form-label small" for="kd-pass2">Repeat the passphrase</label>
        <input class="form-control form-control-sm mb-2" type="password" id="kd-pass2" name="passphrase_confirmation" minlength="12" maxlength="1024" autocomplete="new-password" required>
        <label class="form-label small" for="kd-cur">Your panel password</label>
        <input class="form-control form-control-sm" type="password" id="kd-cur" name="current_password" autocomplete="current-password" required>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"><i class="bi bi-download me-1"></i>Download</button></div>
    </form>
<?= $this->modalEnd() ?>
<?= $this->modalStart('key-import', 'Import a backup key', false, '') ?>
    <form method="post" action="<?= e($this->url('backups.key.import')) ?>" enctype="multipart/form-data" autocomplete="off" data-nim-busy data-nim-noajax>
        <?= $this->csrfField() ?>
        <p class="small">To restore backups made on another server: the key file downloaded there and its passphrase. A server that already has a different key keeps it (its own backups need it).</p>
        <label class="form-label small" for="ki-file">Key file</label>
        <input class="form-control form-control-sm mb-2" type="file" id="ki-file" name="key_file" accept=".json,application/json" required>
        <label class="form-label small" for="ki-pass">Passphrase</label>
        <input class="form-control form-control-sm" type="password" id="ki-pass" name="passphrase" autocomplete="off" required>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Import</button></div>
    </form>
<?= $this->modalEnd() ?>
<?php endif; ?>
<?php endif; ?>
