<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $ssh */
/** @var list<array<string, mixed>> $sshKeys */
$sshKeys = $sshKeys ?? [];
$canManageKeys = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE) && (($user['role_name'] ?? '') === 'admin');
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE);
$eff = (array) ($ssh['effective'] ?? []);
$root = ($eff['permitrootlogin'] ?? '') === 'without-password' ? 'prohibit-password' : ($eff['permitrootlogin'] ?? 'prohibit-password');
$admins = (array) ($ssh['admins_with_keys'] ?? []);
$hasKey = !empty($ssh['root_has_keys']) || $admins !== [];
$pw = ($eff['passwordauthentication'] ?? '') === 'yes';
$rootLabels = ['prohibit-password' => 'With SSH keys only', 'no' => 'Not allowed', 'yes' => 'With password or key (not recommended)', 'forced-commands-only' => 'Forced commands only'];
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($ssh !== []): ?>
<div class="wcp-panel mb-3">
    <div class="wcp-statusbar">
        <span class="fw-medium">SSH server</span>
        <span class="small"><span class="wcp-muted">Port</span> <?= e((string) (($eff['port'] ?? '') !== '' ? $eff['port'] : '—')) ?></span>
        <span class="small"><span class="wcp-muted">Root login</span> <?= e($root === '' ? '—' : ($rootLabels[$root] ?? $root)) ?></span>
        <span class="small"><span class="wcp-muted">Passwords</span> <?= $pw ? '<span class="wcp-warn">allowed</span>' : '<span class="wcp-good">off</span>' ?></span>
        <span class="small"><span class="wcp-muted">Keys installed</span> <?= !empty($ssh['root_has_keys']) ? 'root' : '' ?><?= !empty($ssh['root_has_keys']) && $admins !== [] ? ', ' : '' ?><?= e(implode(', ', $admins)) ?><?= $hasKey ? '' : '<span class="wcp-warn">none — add a key before turning off passwords</span>' ?></span>
    </div>
</div>
<div class="wcp-panel">
    <?php if ($canManage): ?>
        <form method="post" action="<?= e($this->url('security.ssh.configure')) ?>" data-wcp-busy>
            <?= $this->csrfField() ?>
    <?php endif; ?>
    <div class="wcp-setrow">
        <label class="wcp-setrow-label" for="s-port">SSH port</label>
        <div><input class="form-control form-control-sm" id="s-port" type="number" name="port" min="1" max="65535" value="<?= e((string) ($eff['port'] ?? '22')) ?>"<?= $canManage ? '' : ' disabled' ?>></div>
        <div class="wcp-setrow-help">A port other than 22 cuts down automated login attempts. When the firewall is on, a rule for the new port is added automatically.</div>
    </div>
    <div class="wcp-setrow">
        <label class="wcp-setrow-label" for="s-root">Root login</label>
        <div><select class="form-select form-select-sm" id="s-root" name="permit_root_login"<?= $canManage ? '' : ' disabled' ?>>
            <?php foreach (['prohibit-password', 'no', 'yes'] as $k): ?><option value="<?= e($k) ?>"<?= $root === $k ? ' selected' : '' ?>><?= e($rootLabels[$k]) ?></option><?php endforeach; ?>
        </select></div>
        <div class="wcp-setrow-help">Keys only is recommended: a password cannot be guessed if it is not accepted.</div>
    </div>
    <div class="wcp-setrow">
        <label class="wcp-setrow-label" for="s-pw">Password logins</label>
        <div class="form-check form-switch mb-0"><input type="hidden" name="password_authentication" value="0"><input class="form-check-input" type="checkbox" role="switch" id="s-pw" name="password_authentication" value="1"<?= $pw ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>></div>
        <div class="wcp-setrow-help"><?= $hasKey ? 'Turn off to allow SSH keys only.' : 'No SSH key is installed yet: add one before turning passwords off.' ?></div>
    </div>
    <div class="wcp-setrow">
        <label class="wcp-setrow-label" for="s-tries">Max. attempts per connection</label>
        <div><input class="form-control form-control-sm" id="s-tries" type="number" name="max_auth_tries" min="1" max="10" value="<?= e((string) min(10, max(1, (int) (($eff['maxauthtries'] ?? '') !== '' ? $eff['maxauthtries'] : 4)))) ?>"<?= $canManage ? '' : ' disabled' ?>></div>
        <div class="wcp-setrow-help">Fewer attempts slow down password guessing.</div>
    </div>
    <div class="wcp-setrow">
        <label class="wcp-setrow-label" for="s-x11">X11 forwarding</label>
        <div class="form-check form-switch mb-0"><input type="hidden" name="x11_forwarding" value="0"><input class="form-check-input" type="checkbox" role="switch" id="s-x11" name="x11_forwarding" value="1"<?= ($eff['x11forwarding'] ?? '') === 'yes' ? ' checked' : '' ?><?= $canManage ? '' : ' disabled' ?>></div>
        <div class="wcp-setrow-help">Only needed to run graphical programs over SSH.</div>
    </div>
    <?php if ($canManage): ?>
        <div class="pt-3 d-flex flex-wrap align-items-center gap-3">
            <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Validate and apply</button>
            <span class="small wcp-muted">Checked with <code>sshd -t</code> and undone after 2 minutes unless you confirm. Keep your current session open and test a new connection before confirming. NimVor manages <code><?= e($ssh['managed_file'] ?? '') ?></code> only.</span>
        </div>
        </form>
    <?php endif; ?>
</div>

<div class="wcp-panel mt-3" id="ssh-keys">
    <h2 class="h6 mb-1">Administrator SSH keys</h2>
    <p class="small wcp-muted mb-3">Public keys that let root and administrators (members of sudo, admin or wheel) sign in over SSH without a password. Generate a key here (or add a public key you already have), test a new connection with it, then turn off password logins above.</p>
    <?php if ($sshKeys === []): ?>
        <div class="small wcp-muted">No administrator accounts found (the agent may need an update).</div>
    <?php else: ?>
    <div class="table-responsive"><table class="table table-sm align-middle mb-3">
        <thead><tr><th>Account</th><th>Key</th><th>Fingerprint</th><th></th></tr></thead>
        <tbody>
        <?php foreach ($sshKeys as $a): $keys = (array) ($a['keys'] ?? []); ?>
            <?php if ($keys === []): ?>
                <tr><td class="fw-medium"><?= e((string) $a['user']) ?></td><td colspan="3" class="small wcp-muted">No keys<?= !empty($a['other_lines']) ? ' (' . (int) $a['other_lines'] . ' other line(s) kept as they are)' : '' ?></td></tr>
            <?php endif; ?>
            <?php foreach ($keys as $i => $k): ?>
                <tr>
                    <td class="fw-medium"><?= $i === 0 ? e((string) $a['user']) : '' ?></td>
                    <td class="small"><span class="font-monospace"><?= e((string) $k['type']) ?></span><?= !empty($k['bits']) ? ' ' . (int) $k['bits'] : '' ?> <?= e((string) ($k['comment'] ?? '')) ?><?= !empty($k['options']) ? ' <span class="badge text-bg-secondary" title="Added by hand with restrictions (command=, from=…)">restricted</span>' : '' ?></td>
                    <td class="small font-monospace text-break"><?= e((string) $k['fingerprint']) ?></td>
                    <td class="text-end"><?php if ($canManageKeys): ?>
                        <form method="post" action="<?= e($this->url('security.ssh.key_remove')) ?>" data-wcp-confirm="Remove this key from <?= e((string) $a['user']) ?>? Its owner can no longer sign in with it."><?= $this->csrfField() ?><input type="hidden" name="user" value="<?= e((string) $a['user']) ?>"><input type="hidden" name="fingerprint" value="<?= e((string) $k['fingerprint']) ?>"><button class="btn btn-sm btn-outline-danger">Remove</button></form>
                    <?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
        <?php endforeach; ?>
        </tbody>
    </table></div>
    <?php if ($canManageKeys): ?>
    <form class="mb-4" method="post" action="<?= e($this->url('security.ssh.key_generate')) ?>" data-wcp-noajax data-wcp-busy autocomplete="off">
        <?= $this->csrfField() ?>
        <h3 class="h6 small fw-semibold mb-2">Generate a new key</h3>
        <div class="row g-2 align-items-end">
            <div class="col-md-3">
                <label class="form-label small" for="g-user">Account</label>
                <select class="form-select form-select-sm" id="g-user" name="user">
                    <?php foreach ($sshKeys as $a): ?><option value="<?= e((string) $a['user']) ?>"><?= e((string) $a['user']) ?></option><?php endforeach; ?>
                </select>
            </div>
            <div class="col-md-3">
                <label class="form-label small" for="g-comment">Name</label>
                <input class="form-control form-control-sm" id="g-comment" name="comment" maxlength="64" placeholder="e.g. office-laptop">
            </div>
            <div class="col-md-3">
                <label class="form-label small" for="g-pass">Passphrase (recommended)</label>
                <input class="form-control form-control-sm" id="g-pass" type="password" name="passphrase" minlength="8" autocomplete="new-password">
            </div>
            <div class="col-md-3">
                <label class="form-label small" for="g-pass2">Repeat passphrase</label>
                <input class="form-control form-control-sm" id="g-pass2" type="password" name="passphrase_confirmation" autocomplete="new-password">
            </div>
        </div>
        <div class="pt-2 d-flex flex-wrap align-items-center gap-3">
            <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Generate and allow key</button>
            <span class="small wcp-muted">Creates an RSA 4096 key pair, allows it for the account at once and shows the private key one time for download. The passphrase protects the downloaded file; NimVor does not keep the private key or the passphrase.</span>
        </div>
    </form>
    <form method="post" action="<?= e($this->url('security.ssh.key_add')) ?>" data-wcp-busy>
        <h3 class="h6 small fw-semibold mb-2">Or add an existing public key</h3>
        <?= $this->csrfField() ?>
        <div class="row g-2 align-items-end">
            <div class="col-md-3">
                <label class="form-label small" for="k-user">Account</label>
                <select class="form-select form-select-sm" id="k-user" name="user">
                    <?php foreach ($sshKeys as $a): ?><option value="<?= e((string) $a['user']) ?>"><?= e((string) $a['user']) ?></option><?php endforeach; ?>
                </select>
            </div>
            <div class="col-md-9">
                <label class="form-label small" for="k-key">Public key</label>
                <textarea class="form-control form-control-sm font-monospace" id="k-key" name="key" rows="2" required placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA… you@laptop"></textarea>
            </div>
        </div>
        <div class="pt-2 d-flex flex-wrap align-items-center gap-3">
            <button class="btn btn-sm btn-outline-primary">Add key</button>
            <span class="small wcp-muted">Ed25519, ECDSA or RSA (2048 bits or more). Your panel password is asked again before keys change.</span>
        </div>
    </form>
    <?php elseif ($this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE)): ?>
        <div class="small wcp-muted">Only administrators add or remove SSH keys.</div>
    <?php endif; ?>
    <?php endif; ?>
</div>
<?php endif; ?>
