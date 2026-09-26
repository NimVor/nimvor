<?php
/** @var \WhaCP\View\View $this */
$canManage = $this->can(\WhaCP\Security\Permissions::BACKUPS_MANAGE);
?>
<?php $this->start(); ?>Backups<?php $this->end('title'); ?>
<?php $this->start(); ?>Website and server backups<?php $this->end('subtitle'); ?>
<div class="card mb-3">
    <div class="card-header"><i class="bi bi-globe2 me-1"></i>Website backups</div>
    <?= $this->partial('backups/_list', ['backups' => $backups, 'canManage' => $canManage, 'showSite' => true]) ?>
    <div class="card-footer small text-body-secondary">Create, restore and schedule website backups from each website's <strong>Backups</strong> tab.</div>
</div>
<?php if ($admin): ?>
<div class="row g-3">
    <div class="col-xl-8">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-hdd-rack me-1"></i>Server configuration</div>
            <?= $this->partial('backups/_list', ['backups' => $serverBackups, 'canManage' => $canManage]) ?>
            <div class="card-footer small text-body-secondary">Archives Nginx, PHP, MySQL, Redis, certificates, SSH/firewall and WhaCP configuration for disaster recovery. It contains private keys, so it is always encrypted.</div>
        </div>
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-cloud me-1"></i>Remote storage</div>
            <div class="card-body">
                <?php if ($storages === []): ?><p class="text-body-secondary small">No remote storage. Add an S3-compatible bucket (AWS S3, Backblaze B2, Wasabi, Cloudflare R2, MinIO…), an SFTP or FTPS server, Google Drive or a mounted disk to keep copies off this server.</p><?php endif; ?>
                <?php foreach ($storages as $s): ?>
                    <div class="d-flex align-items-center border-bottom py-2 small">
                        <div><strong><?= e($s['name']) ?></strong> <span class="badge text-bg-light border"><?= e(\WhaCP\Services\BackupService::STORAGE_TYPES[$s['kind']] ?? $s['kind']) ?></span><div class="text-body-secondary"><?= e($s['where']) ?><?= $s['prefix'] !== '' ? ' · ' . e($s['prefix']) . '/' : '' ?></div></div>
                        <?php if ($canManage): ?><form class="ms-auto me-1" method="post" action="<?= e($this->url('backups.storages.scan', ['storage' => (int) $s['id']])) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary" title="Find backups in this storage (for example after moving to a new server)"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-search"></i> Find backups</button></form><form method="post" action="<?= e($this->url('backups.storages.delete', ['storage' => (int) $s['id']])) ?>" data-wcp-confirm="Remove the storage <?= e($s['name']) ?>? Backups already uploaded stay there."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash"></i></button></form><?php endif; ?>
                    </div>
                <?php endforeach; ?>
                <?php if ($canManage): ?>
                    <?php $kind = (string) ($old['type'] ?? 's3'); $f = fn (string $n, string $l, array $x = []): string => $this->partial('partials/field', $x + ['name' => $n, 'label' => $l, 'value' => (string) ($old[$n] ?? ($x['default'] ?? ''))]); ?>
                    <details class="mt-3"<?= isset($old['type']) ? ' open' : '' ?>>
                        <summary class="btn btn-sm btn-outline-primary">Add storage</summary>
                        <form class="mt-3" method="post" action="<?= e($this->url('backups.storages.store')) ?>" data-wcp-busy>
                            <?= $this->csrfField() ?>
                            <div class="row g-2">
                                <div class="col-md-6"><?= $f('name', 'Name', ['attrs' => ['required' => 'required', 'placeholder' => 'Offsite']]) ?></div>
                                <div class="col-md-6 mb-3">
                                    <label class="form-label" for="st-type">Type</label>
                                    <select class="form-select" id="st-type" name="type" data-wcp-toggle-kind>
                                        <?php foreach (\WhaCP\Services\BackupService::STORAGE_TYPES as $k => $label): ?><option value="<?= e($k) ?>"<?= $k === $kind ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                                    </select>
                                </div>
                            </div>
                            <div class="row g-2" data-wcp-kind="s3">
                                <div class="col-md-6"><?= $f('endpoint', 'Endpoint', ['attrs' => ['required' => 'required', 'placeholder' => 'https://s3.eu-central-1.amazonaws.com']]) ?></div>
                                <div class="col-md-3"><?= $f('bucket', 'Bucket', ['attrs' => ['required' => 'required']]) ?></div>
                                <div class="col-md-3"><?= $f('region', 'Region', ['default' => 'us-east-1']) ?></div>
                                <div class="col-md-6"><?= $f('access_key', 'Access key', ['value' => '', 'attrs' => ['required' => 'required', 'autocomplete' => 'off']]) ?></div>
                                <div class="col-md-6"><?= $f('secret_key', 'Secret key', ['type' => 'password', 'value' => '', 'attrs' => ['required' => 'required', 'autocomplete' => 'new-password']]) ?></div>
                                <div class="col-12"><div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="path_style" value="1" id="st-path"><label class="form-check-label" for="st-path">Path-style URLs (MinIO and some other providers)</label></div></div>
                            </div>
                            <div class="row g-2" data-wcp-kind="sftp ftp">
                                <div class="col-md-6"><?= $f('host', 'Host', ['attrs' => ['required' => 'required', 'placeholder' => 'backup.example.com']]) ?></div>
                                <div class="col-md-2"><?= $f('port', 'Port', ['type' => 'number', 'attrs' => ['min' => '1', 'max' => '65535', 'placeholder' => 'default']]) ?></div>
                                <div class="col-md-4"><?= $f('username', 'User name', ['attrs' => ['required' => 'required', 'autocomplete' => 'off']]) ?></div>
                                <div class="col-md-6"><?= $f('password', 'Password', ['type' => 'password', 'value' => '', 'help' => 'For SFTP you can use a private key instead.', 'attrs' => ['autocomplete' => 'new-password']]) ?></div>
                                <div class="col-md-6 mb-3" data-wcp-kind="ftp">
                                    <label class="form-label" for="st-tls">Encryption</label>
                                    <select class="form-select" id="st-tls" name="tls">
                                        <option value="explicit">Explicit TLS (FTPS, port 21) — recommended</option>
                                        <option value="implicit"<?= ($old['tls'] ?? '') === 'implicit' ? ' selected' : '' ?>>Implicit TLS (port 990)</option>
                                        <option value="none"<?= ($old['tls'] ?? '') === 'none' ? ' selected' : '' ?>>None — plain FTP, password sent unencrypted</option>
                                    </select>
                                </div>
                                <div class="col-md-6 mb-3" data-wcp-kind="sftp">
                                    <label class="form-label" for="st-key">Private key (optional)</label>
                                    <textarea class="form-control font-monospace small<?= isset($errors['private_key']) ? ' is-invalid' : '' ?>" id="st-key" name="private_key" rows="3" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" autocomplete="off" spellcheck="false"></textarea>
                                    <div class="form-text">An unencrypted key; its public key must be in the server's authorized_keys.</div>
                                </div>
                            </div>
                            <div class="row g-2" data-wcp-kind="gdrive">
                                <div class="col-md-6"><?= $f('client_id', 'OAuth client ID', ['attrs' => ['required' => 'required', 'autocomplete' => 'off']]) ?></div>
                                <div class="col-md-6"><?= $f('client_secret', 'OAuth client secret', ['type' => 'password', 'value' => '', 'attrs' => ['required' => 'required', 'autocomplete' => 'new-password']]) ?></div>
                                <div class="col-12 form-text mb-3">Create an OAuth client of type <em>Web application</em> in your Google Cloud project with the Drive API enabled, and add <code><?= e(rtrim((string) ($panelUrl ?? ''), '/') ?: 'https://your-panel') ?>/backups/storages/google/callback</code> as an authorised redirect URI. WhaCP only asks for access to the files it creates.</div>
                            </div>
                            <div class="row g-2" data-wcp-kind="local">
                                <div class="col-md-8"><?= $f('path', 'Folder', ['attrs' => ['required' => 'required', 'placeholder' => '/mnt/backup-disk/whacp'], 'help' => 'An existing folder under /mnt, /media, /backup(s) or /srv/backup(s), owned by root. Use a separate disk or a mounted NAS share.']) ?></div>
                            </div>
                            <div class="row g-2" data-wcp-kind="s3 sftp ftp local gdrive">
                                <div class="col-md-6"><?= $f('prefix', 'Sub-folder (optional)', ['attrs' => ['placeholder' => 'whacp/web1']]) ?></div>
                            </div>
                            <button class="btn btn-sm btn-primary">Test and save</button>
                            <div class="form-text">Credentials are stored encrypted in the panel and sent to the agent only for each backup. Use credentials limited to backups. For SFTP and FTPS you confirm the server's key before it is saved; Google Drive opens Google's consent page.</div>
                        </form>
                    </details>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-xl-4">
        <?php if ($canManage): ?>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-plus-lg me-1"></i>Back up the server configuration</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('backups.server')) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <input class="form-control form-control-sm mb-2" name="label" maxlength="100" placeholder="Label (optional)" aria-label="Label">
                        <?= $this->partial('backups/_options', ['storages' => $storages, 'prefix' => 'sv', 'alwaysEncrypted' => true]) ?>
                        <button class="btn btn-sm btn-primary mt-3"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Start backup</button>
                    </form>
                </div>
            </div>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-calendar-check me-1"></i>Server backup schedule</div>
                <div class="card-body">
                    <?= $this->partial('backups/_schedules', ['schedules' => $schedules, 'deleteRoute' => 'backups.schedules.delete', 'routeParams' => []]) ?>
                    <?= $this->partial('backups/_schedule_form', ['action' => $this->url('backups.schedules.store'), 'site' => false, 'storages' => $storages]) ?>
                </div>
            </div>
        <?php endif; ?>
        <div class="card">
            <div class="card-header"><i class="bi bi-key me-1"></i>Encryption key</div>
            <div class="card-body small">
                <?php if ($keyInfo === null): ?>
                    <span class="text-body-secondary">Agent not reachable.</span>
                <?php elseif (empty($keyInfo['exists'])): ?>
                    The key is created with the first encrypted backup at <code><?= e($keyInfo['path']) ?></code>.
                <?php else: ?>
                    Key ID <code><?= e($keyInfo['key_id']) ?></code> in <code><?= e($keyInfo['path']) ?></code>.
                <?php endif; ?>
                <div class="alert alert-warning mt-2 mb-0 p-2"><i class="bi bi-exclamation-triangle me-1"></i>Copy this file somewhere safe (as root). Encrypted backups cannot be restored without it — for example on a new server after a disk failure.</div>
            </div>
        </div>
    </div>
</div>
<?php endif; ?>
