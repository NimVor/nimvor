<?php
/**
 * Remote storage form: adding one ($storage null) or editing one (its type
 * is fixed; secret fields left empty keep their value).
 *
 * @var \WhaCP\View\View $this
 * @var array<string, mixed>|null $storage
 * @var array<string, mixed> $values form values (old input or the storage's settings)
 * @var array<string, string> $errors
 * @var string $px id prefix
 */
use WhaCP\Services\BackupService;

$edit = $storage !== null;
$kind = (string) ($values['type'] ?? 's3');
$f = function (string $n, string $l, array $x = []) use ($values, $errors, $px): string {
    return $this->partial('partials/field', $x + ['idPrefix' => $px, 'errors' => $errors, 'name' => $n, 'label' => $l, 'value' => (string) ($x['value'] ?? ($values[$n] ?? ($x['default'] ?? '')))]);
};
$secret = $edit ? ['placeholder' => 'unchanged', 'autocomplete' => 'new-password'] : ['required' => 'required', 'autocomplete' => 'new-password'];
?>
<form method="post" action="<?= e($edit ? $this->url('backups.storages.update', ['storage' => (int) $storage['id']]) : $this->url('backups.storages.store')) ?>" data-wcp-busy data-wcp-noajax>
    <?= $this->csrfField() ?>
    <div class="row g-2">
        <div class="col-md-6"><?= $f('name', 'Name', ['value' => (string) ($values['name'] ?? ($storage['name'] ?? '')), 'attrs' => ['required' => 'required', 'placeholder' => 'Offsite', 'maxlength' => '60']]) ?></div>
        <div class="col-md-6 mb-3">
            <label class="form-label" for="<?= e($px) ?>type">Type</label>
            <?php if ($edit): ?>
                <input class="form-control" id="<?= e($px) ?>type" value="<?= e(BackupService::STORAGE_TYPES[$kind] ?? $kind) ?>" disabled>
            <?php else: ?>
                <select class="form-select" id="<?= e($px) ?>type" name="type" data-wcp-toggle-kind>
                    <?php foreach (BackupService::STORAGE_TYPES as $k => $label): ?><option value="<?= e($k) ?>"<?= $k === $kind ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                </select>
            <?php endif; ?>
        </div>
    </div>
    <div class="row g-2" data-wcp-kind="s3"<?= $edit && $kind !== 's3' ? ' hidden' : '' ?>>
        <?php if (!$edit || $kind === 's3'): ?>
        <div class="col-md-6"><?= $f('endpoint', 'Endpoint', ['attrs' => ['required' => 'required', 'placeholder' => 'https://s3.eu-central-1.amazonaws.com']]) ?></div>
        <div class="col-md-3"><?= $f('bucket', 'Bucket', ['attrs' => ['required' => 'required']]) ?></div>
        <div class="col-md-3"><?= $f('region', 'Region', ['default' => 'us-east-1']) ?></div>
        <div class="col-md-6"><?= $f('access_key', 'Access key', ['value' => '', 'attrs' => $edit ? ['placeholder' => 'unchanged', 'autocomplete' => 'off'] : ['required' => 'required', 'autocomplete' => 'off']]) ?></div>
        <div class="col-md-6"><?= $f('secret_key', 'Secret key', ['type' => 'password', 'value' => '', 'attrs' => $secret]) ?></div>
        <div class="col-12"><div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="path_style" value="1" id="<?= e($px) ?>path"<?= !empty($values['path_style']) ? ' checked' : '' ?>><label class="form-check-label" for="<?= e($px) ?>path">Path-style URLs (MinIO and some other providers)</label></div></div>
        <?php endif; ?>
    </div>
    <div class="row g-2" data-wcp-kind="sftp ftp"<?= $edit && !in_array($kind, ['sftp', 'ftp'], true) ? ' hidden' : '' ?>>
        <?php if (!$edit || in_array($kind, ['sftp', 'ftp'], true)): ?>
        <div class="col-md-6"><?= $f('host', 'Host', ['attrs' => $edit ? ['disabled' => 'disabled'] : ['required' => 'required', 'placeholder' => 'backup.example.com']]) ?></div>
        <div class="col-md-2"><?= $f('port', 'Port', ['type' => 'number', 'value' => (string) ((int) ($values['port'] ?? 0) ?: ''), 'attrs' => $edit ? ['readonly' => 'readonly'] : ['min' => '1', 'max' => '65535', 'placeholder' => 'default']]) ?></div>
        <div class="col-md-4"><?= $f('username', 'User name', ['attrs' => ['required' => 'required', 'autocomplete' => 'off']]) ?></div>
        <div class="col-md-6"><?= $f('password', 'Password', ['type' => 'password', 'value' => '', 'help' => $edit ? 'Leave empty to keep the current one.' : 'For SFTP you can use a private key instead.', 'attrs' => ['autocomplete' => 'new-password'] + ($edit ? ['placeholder' => 'unchanged'] : [])]) ?></div>
        <div class="col-md-6 mb-3" data-wcp-kind="ftp"<?= $edit && $kind !== 'ftp' ? ' hidden' : '' ?>>
            <label class="form-label" for="<?= e($px) ?>tls">Encryption</label>
            <select class="form-select" id="<?= e($px) ?>tls" name="tls">
                <option value="explicit">Explicit TLS (FTPS, port 21) — recommended</option>
                <option value="implicit"<?= ($values['tls'] ?? '') === 'implicit' ? ' selected' : '' ?>>Implicit TLS (port 990)</option>
                <option value="none"<?= ($values['tls'] ?? '') === 'none' ? ' selected' : '' ?>>None — plain FTP, password sent unencrypted</option>
            </select>
        </div>
        <div class="col-md-6 mb-3" data-wcp-kind="sftp"<?= $edit && $kind !== 'sftp' ? ' hidden' : '' ?>>
            <label class="form-label" for="<?= e($px) ?>key">Private key (optional)</label>
            <textarea class="form-control font-monospace small<?= isset($errors['private_key']) ? ' is-invalid' : '' ?>" id="<?= e($px) ?>key" name="private_key" rows="3" placeholder="<?= $edit ? 'unchanged' : '-----BEGIN OPENSSH PRIVATE KEY-----' ?>" autocomplete="off" spellcheck="false"></textarea>
            <div class="form-text">An unencrypted key; its public key must be in the server's authorized_keys.</div>
        </div>
        <?php if ($edit): ?><div class="col-12 form-text mb-2">The server (host and port) of a storage cannot be changed: its key was confirmed for that server. Add a new storage for another server.</div><?php endif; ?>
        <?php endif; ?>
    </div>
    <div class="row g-2" data-wcp-kind="gdrive"<?= $edit && $kind !== 'gdrive' ? ' hidden' : '' ?>>
        <?php if (!$edit): ?>
        <div class="col-md-6"><?= $f('client_id', 'OAuth client ID', ['attrs' => ['required' => 'required', 'autocomplete' => 'off']]) ?></div>
        <div class="col-md-6"><?= $f('client_secret', 'OAuth client secret', ['type' => 'password', 'value' => '', 'attrs' => ['required' => 'required', 'autocomplete' => 'new-password']]) ?></div>
        <div class="col-12 form-text mb-3">Create an OAuth client of type <em>Web application</em> in your Google Cloud project with the Drive API enabled, and add <code><?= e(rtrim((string) ($panelUrl ?? ''), '/') ?: 'https://your-panel') ?>/backups/storages/google/callback</code> as an authorised redirect URI. NimVor only asks for access to the files it creates.</div>
        <?php elseif ($kind === 'gdrive'): ?>
        <div class="col-12 form-text mb-2">Google Drive's access comes from Google's consent page: here only the name and sub-folder change.</div>
        <?php endif; ?>
    </div>
    <div class="row g-2" data-wcp-kind="local"<?= $edit && $kind !== 'local' ? ' hidden' : '' ?>>
        <?php if (!$edit || $kind === 'local'): ?>
        <div class="col-md-8"><?= $f('path', 'Folder', ['attrs' => ['required' => 'required', 'placeholder' => '/backups'], 'help' => '/backups (or /backup, /srv/backups, a folder in them) is created when missing, for root only. A folder of a mounted disk or NAS share under /mnt or /media must exist: NimVor never creates it, so backups do not fill this server\'s disk while that disk is missing. A folder on this server\'s own disk is not off the server: for safety, use a separate disk, a NAS or a remote storage.']) ?></div>
        <?php endif; ?>
    </div>
    <div class="row g-2">
        <div class="col-md-6"><?= $f('prefix', 'Sub-folder (optional)', ['attrs' => ['placeholder' => 'whacp/web1']]) ?></div>
    </div>
    <div class="form-text mb-3">Credentials are stored encrypted in the panel and sent to the agent only for each backup. Use credentials limited to backups.<?= $edit ? ' The changed settings are tested before they are saved.' : ' For SFTP and FTPS you confirm the server\'s key before it is saved; Google Drive opens Google\'s consent page.' ?></div>
    <div class="d-flex justify-content-end gap-2">
        <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
        <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $edit ? 'Test and save' : 'Test and add' ?></button>
    </div>
</form>
