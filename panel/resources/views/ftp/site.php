<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed>|null $status
 * @var array<string, mixed>|null $sftp
 * @var string $host
 */
use WhaCP\Http\Controllers\FtpController;

$id = (int) $site['id'];
$accounts = (array) ($status['accounts'] ?? []);
$cert = (array) ($status['certificate'] ?? []);
$root = rtrim((string) ($status['sftp_root'] ?? ''), '/') . '/' . $site['domain'];
$errors ??= [];
$ftpReady = !empty($status['active']);
$tlsOptional = (($status['settings']['tls'] ?? 'required') === 'optional');
$sftpKeys = array_map(static fn ($k): string => (string) ((array) $k)['key'], (array) ($sftp['keys'] ?? []));
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'ftp']) ?>
<?= $this->partial('partials/secret', ['secret' => $secret ?? null]) ?>
<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
<?php else: ?>
<div class="row g-3">
    <div class="col-xl-7">
        <div class="card mb-3">
            <?php $canAdd = $ftpReady && count($accounts) < FtpController::MAX_ACCOUNTS; $openAdd = $canAdd && ($errors !== [] || $accounts === []); ?>
            <div class="card-header wcp-toolbar">
                <h2 class="wcp-toolbar-title"><i class="bi bi-hdd-network me-1"></i>FTP accounts <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($accounts) ?> of <?= FtpController::MAX_ACCOUNTS ?></span></h2>
                <?php if ($canAdd): ?><button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-ftp"><i class="bi bi-plus-lg me-1"></i>Add FTP account</button><?php endif; ?>
            </div>
            <?php if (!$ftpReady): ?>
                <div class="card-body"><div class="alert alert-info small mb-0">The FTP server is not running on this server<?= $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE) ? '. Set it up under <a href="' . e($this->url('ftp')) . '">Server → FTP server</a>' : '; ask the administrator to set it up' ?>. SFTP (on the right) works without it.</div></div>
            <?php endif; ?>
            <div class="table-responsive">
                <table class="table mb-0 wcp-table align-middle">
                    <thead><tr><th>Login</th><th>Folder</th><th class="text-end">Quota</th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($accounts as $a): $a = (array) $a; $rel = ltrim(substr((string) $a['dir'], strlen($root)), '/'); ?>
                        <tr>
                            <td class="font-monospace"><?= e((string) $a['name']) ?></td>
                            <td class="small font-monospace"><?= e($rel === '' ? '(whole website)' : $rel) ?></td>
                            <td class="text-end"><?= (int) $a['quota_mb'] > 0 ? e(format_bytes((int) $a['quota_mb'] * 1048576)) : '—' ?></td>
                            <td class="text-end text-nowrap">
                                <details class="d-inline-block text-start">
                                    <summary class="btn btn-sm btn-outline-secondary" title="Change"><i class="bi bi-pencil"></i></summary>
                                    <form class="wcp-popover-form card card-body mt-1 position-absolute end-0 shadow-sm" method="post" action="<?= e($this->url('sites.ftp.save', ['id' => $id])) ?>">
                                        <?= $this->csrfField() ?>
                                        <input type="hidden" name="existing" value="1"><input type="hidden" name="name" value="<?= e((string) $a['name']) ?>">
                                        <label class="form-label small mb-1">Folder</label><input class="form-control form-control-sm mb-2 font-monospace" name="dir" value="<?= e($rel) ?>">
                                        <label class="form-label small mb-1">Quota (MB, 0 = none)</label><input class="form-control form-control-sm mb-2" type="number" min="0" name="quota_mb" value="<?= (int) $a['quota_mb'] ?>">
                                        <label class="form-label small mb-1">New password (optional)</label><input class="form-control form-control-sm mb-2" type="password" name="password" autocomplete="new-password" minlength="10">
                                        <div class="form-check small mb-2"><input class="form-check-input" type="checkbox" name="generate" value="1" id="gen-<?= e(md5((string) $a['name'])) ?>"><label class="form-check-label" for="gen-<?= e(md5((string) $a['name'])) ?>">Generate a new password</label></div>
                                        <button class="btn btn-sm btn-primary">Save</button>
                                    </form>
                                </details>
                                <form class="d-inline" method="post" action="<?= e($this->url('sites.ftp.delete', ['id' => $id])) ?>" data-wcp-confirm="Delete the FTP account <?= e((string) $a['name']) ?>? Files are kept."><?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e((string) $a['name']) ?>"><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button></form>
                            </td>
                        </tr>
                    <?php endforeach; ?>
                    <?php if ($accounts === []): ?><tr><td colspan="4"><div class="wcp-empty-state py-3"><i class="bi bi-hdd-network" aria-hidden="true"></i><div class="fw-medium">No FTP accounts yet</div><div class="small">Give a designer or an FTP client (e.g. FileZilla) access to this website's files, or use SFTP on the right.</div></div></td></tr><?php endif; ?>
                    </tbody>
                </table>
            </div>
            <?php if ($canAdd): ?>
            <?= $this->modalStart('new-ftp', 'Add FTP account', $openAdd) ?>
                <form class="p-3" method="post" action="<?= e($this->url('sites.ftp.save', ['id' => $id])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <div class="row g-2 align-items-start">
                        <div class="col-md-6">
                            <label class="form-label" for="ftp-name">Login</label>
                            <div class="input-group"><input class="form-control<?= isset($errors['name']) ? ' is-invalid' : '' ?>" id="ftp-name" name="name" value="<?= e((string) ($old['name'] ?? '')) ?>" required pattern="[a-z0-9][a-z0-9._\-]{0,30}" placeholder="designer" autocomplete="off"><span class="input-group-text">@<?= e(strtolower((string) $site['domain'])) ?></span></div>
                            <?php if (isset($errors['name'])): ?><div class="invalid-feedback d-block"><?= e($errors['name']) ?></div><?php endif; ?>
                        </div>
                        <div class="col-md-6"><?= $this->partial('partials/field', ['name' => 'dir', 'label' => 'Folder (empty = whole website)', 'value' => (string) ($old['dir'] ?? 'public_html')]) ?></div>
                        <div class="col-md-6"><?= $this->partial('partials/field', ['name' => 'password', 'label' => 'Password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'new-password', 'minlength' => '10']]) ?></div>
                        <div class="col-md-3"><?= $this->partial('partials/field', ['name' => 'quota_mb', 'label' => 'Quota (MB)', 'type' => 'number', 'value' => (string) ($old['quota_mb'] ?? '0'), 'attrs' => ['min' => '0']]) ?></div>
                    </div>
                    <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="generate" value="1" id="ftp-gen"><label class="form-check-label" for="ftp-gen">Generate a strong password (shown once)</label></div>
                    <button class="btn btn-sm btn-primary"><i class="bi bi-plus-lg me-1"></i>Add FTP account</button>
                </form>
            <?= $this->modalEnd() ?>
            <?php endif; ?>
            <?php if ($ftpReady): ?>
            <div class="card-footer small">
                <strong>Connect with:</strong> host <code><?= e($host) ?></code>, port 21, <?= $tlsOptional ? 'FTP with explicit TLS (recommended)' : '<strong>FTP with explicit TLS</strong> (FTPS) — plain FTP is refused' ?>, passive mode.
                <?php if ($cert !== []): ?><br>The server uses a self-signed certificate; accept it once in your client after checking its SHA-256 fingerprint <code class="text-break"><?= e(implode(':', str_split((string) $cert['sha256'], 2))) ?></code>.<?php endif; ?>
            </div>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-xl-5">
        <div class="card mb-3" id="sftp">
            <div class="card-header d-flex align-items-center"><i class="bi bi-terminal me-1"></i>SFTP access
                <span class="ms-auto"><?= $sftp !== null ? $this->statusBadge('active', 'on') : $this->statusBadge('inactive', 'off') ?></span></div>
            <div class="card-body">
                <?php $shellMode = (string) ($sftp['shell'] ?? 'disabled'); ?>
                <?php if ($shellMode === 'jailed'): ?>
                    <p class="small text-body-secondary">Log in as the website's own user over SSH (port 22) for SFTP and a <strong>jailed shell</strong>: <code>ssh <?= e((string) $site['system_user']) ?>@<?= e($host) ?></code>. Only the website folder is writable; the rest of the server is hidden or read-only.</p>
                <?php elseif ($shellMode === 'normal'): ?>
                    <p class="small text-body-secondary">Log in as the website's own user over SSH (port 22) for SFTP and a <strong>shell</strong>: <code>ssh <?= e((string) $site['system_user']) ?>@<?= e($host) ?></code>.</p>
                <?php else: ?>
                    <p class="small text-body-secondary">Log in as the website's own user over SSH (port 22), for file transfer only. The login starts in the website folder and cannot reach anything outside the websites folder; there is no shell<?= $this->serverScope() ? ' (administrators can allow one under Security → Shell access)' : '' ?>.</p>
                <?php endif; ?>
                <form method="post" action="<?= e($this->url('sites.sftp', ['id' => $id])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <div class="form-check form-switch mb-3"><input class="form-check-input" type="checkbox" role="switch" name="enabled" value="1" id="sftp-on"<?= $sftp !== null ? ' checked' : '' ?>><label class="form-check-label" for="sftp-on">Allow SFTP for <code><?= e((string) $site['system_user']) ?></code></label></div>
                    <label class="form-label" for="sftp-keys">Public keys (one per line)</label>
                    <textarea class="form-control font-monospace small mb-1" id="sftp-keys" name="keys" rows="4" spellcheck="false" placeholder="ssh-ed25519 AAAA… you@laptop"><?= e(implode("\n", $sftpKeys)) ?></textarea>
                    <?php foreach ((array) ($sftp['keys'] ?? []) as $k): ?><div class="small text-body-secondary font-monospace text-truncate"><?= e((string) ((array) $k)['fingerprint']) ?> <?= e((string) (explode(' ', (string) ((array) $k)['key'], 3)[2] ?? '')) ?></div><?php endforeach; ?>
                    <div class="form-check mt-3"><input class="form-check-input" type="checkbox" name="password_login" value="1" id="sftp-pw"<?= !empty($sftp['password_login']) ? ' checked' : '' ?>><label class="form-check-label" for="sftp-pw">Also allow password login</label></div>
                    <div class="form-text">This is the Linux account <code><?= e((string) $site['system_user']) ?></code>'s own password: SSH, SFTP and the web terminal all use it. WhaCP does not store it; changing it signs out the account's open terminals.</div>
                    <div class="mt-2"><?= $this->partial('partials/field', ['name' => 'sftp_password', 'label' => !empty($sftp['password_login']) ? 'New password (leave empty to keep)' : 'Password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'new-password', 'minlength' => '10']]) ?></div>
                    <button class="btn btn-sm btn-primary">Save SFTP access</button>
                </form>
            </div>
            <?php if ($sftp !== null): ?>
                <div class="card-footer small"><strong>Connect with:</strong> <code>sftp <?= e((string) $site['system_user']) ?>@<?= e($host) ?></code> — the files are in <code>/<?= e((string) $site['domain']) ?></code>.</div>
            <?php endif; ?>
        </div>
    </div>
</div>
<?php endif; ?>
