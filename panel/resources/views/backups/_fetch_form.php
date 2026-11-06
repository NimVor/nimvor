<?php
/**
 * The window that fetches a file from a remote SFTP or FTPS server. The server's key is checked first and shown for
 * the person to confirm; the login is sent only with the fetch and is not stored.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $values old input (never the password or the private key)
 * @var array<string, string> $errors
 */
$v = static fn (string $k, string $d = ''): string => is_scalar($values[$k] ?? null) ? (string) $values[$k] : $d;
$err = static fn (string $k): string => isset($errors[$k]) ? ' is-invalid' : '';
$fb = static function (string $k) use ($errors): string {
    return isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e($errors[$k]) . '</div>' : '';
};
$protocol = $v('protocol', 'sftp');
?>
<form method="post" action="<?= e($this->url('backups.fetched.fetch')) ?>" autocomplete="off" data-fx-form data-probe-url="<?= e($this->url('backups.fetched.probe')) ?>" data-nim-noajax>
    <?= $this->csrfField() ?>
    <input type="hidden" name="fetch_form" value="1">
    <input type="hidden" name="host_key" value="" data-fx-hostkey>
    <input type="hidden" name="tls_pin" value="" data-fx-pin>
    <div class="row g-2">
        <div class="col-md-4 mb-2">
            <label class="form-label" for="fx-protocol">Protocol</label>
            <select class="form-select<?= $err('protocol') ?>" id="fx-protocol" name="protocol" data-nim-toggle-kind>
                <option value="sftp"<?= $protocol === 'sftp' ? ' selected' : '' ?>>SFTP (SSH)</option>
                <option value="ftp"<?= $protocol === 'ftp' ? ' selected' : '' ?>>FTPS (FTP over TLS)</option>
            </select><?= $fb('protocol') ?>
        </div>
        <div class="col-md-5 mb-2">
            <label class="form-label" for="fx-host">Host</label>
            <input class="form-control<?= $err('host') ?>" id="fx-host" name="host" value="<?= e($v('host')) ?>" placeholder="files.example.com" maxlength="253" data-fx-reset><?= $fb('host') ?>
        </div>
        <div class="col-md-3 mb-2">
            <label class="form-label" for="fx-port">Port</label>
            <input class="form-control<?= $err('port') ?>" id="fx-port" name="port" type="number" min="1" max="65535" value="<?= e($v('port')) ?>" placeholder="default" data-fx-reset><?= $fb('port') ?>
        </div>
        <div class="col-md-6 mb-2">
            <label class="form-label" for="fx-username">User name</label>
            <input class="form-control<?= $err('username') ?>" id="fx-username" name="username" value="<?= e($v('username')) ?>" autocomplete="off" maxlength="100"><?= $fb('username') ?>
        </div>
        <div class="col-md-6 mb-2">
            <label class="form-label" for="fx-password">Password</label>
            <input class="form-control<?= $err('password') ?>" id="fx-password" name="password" type="password" value="" autocomplete="new-password" maxlength="500"><?= $fb('password') ?>
            <div class="form-text" data-nim-kind="sftp">Or paste a private key below.</div>
        </div>
        <div class="col-12 mb-2" data-nim-kind="sftp">
            <label class="form-label" for="fx-private_key">Private key (optional)</label>
            <textarea class="form-control font-monospace small<?= $err('private_key') ?>" id="fx-private_key" name="private_key" rows="3" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" autocomplete="off" spellcheck="false"></textarea><?= $fb('private_key') ?>
            <div class="form-text">An unencrypted key whose public key is in the server's authorized_keys.</div>
        </div>
        <div class="col-md-6 mb-2" data-nim-kind="ftp">
            <label class="form-label" for="fx-tls">Encryption</label>
            <select class="form-select<?= $err('tls') ?>" id="fx-tls" name="tls" data-fx-reset>
                <option value="explicit"<?= $v('tls', 'explicit') === 'explicit' ? ' selected' : '' ?>>Explicit TLS (port 21) — recommended</option>
                <option value="implicit"<?= $v('tls') === 'implicit' ? ' selected' : '' ?>>Implicit TLS (port 990)</option>
            </select><?= $fb('tls') ?>
            <div class="form-text">Plain FTP is not offered: it would send the login and the file unprotected.</div>
        </div>
        <div class="col-12 mb-2">
            <label class="form-label" for="fx-remote_path">Remote file</label>
            <input class="form-control font-monospace<?= $err('remote_path') ?>" id="fx-remote_path" name="remote_path" value="<?= e($v('remote_path')) ?>" placeholder="/backups/site.tar.gz  or  sftp://user@files.example.com/backups/site.tar.gz" maxlength="1024" data-fx-path><?= $fb('remote_path') ?>
            <div class="form-text">The path of the file on the remote server, or its full address. A path that does not start with / is relative to the login folder. Do not put the password in the address.</div>
        </div>
        <div class="col-md-6 mb-2">
            <label class="form-label" for="fx-name">Save as (optional)</label>
            <input class="form-control<?= $err('name') ?>" id="fx-name" name="name" value="<?= e($v('name')) ?>" maxlength="151" placeholder="same name as the remote file"><?= $fb('name') ?>
            <div class="form-text">The file is saved in NimVor's folder of fetched files; an existing name gets a number added.</div>
        </div>
    </div>
    <div class="alert alert-warning small d-none" role="status" aria-live="polite" data-fx-key>
        <div class="fw-semibold mb-1"><i class="bi bi-shield-check me-1"></i>Confirm the server's key</div>
        <div data-fx-key-text></div>
        <div class="mt-1">Continue only if it matches the key your provider gave you. The login is sent only after you choose “Fetch file”, and only to a server with exactly this key.</div>
    </div>
    <div class="alert alert-danger small d-none" role="alert" data-fx-error></div>
    <div class="d-flex justify-content-end gap-2 mt-3">
        <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
        <button class="btn btn-sm btn-primary" data-fx-go><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><span data-fx-go-text>Check the server</span></button>
    </div>
</form>
