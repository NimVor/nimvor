<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $ssh */
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
            <span class="small wcp-muted">Checked with <code>sshd -t</code> and undone after 2 minutes unless you confirm. Keep your current session open and test a new connection before confirming. WhaCP manages <code><?= e($ssh['managed_file'] ?? '') ?></code> only.</span>
        </div>
        </form>
    <?php endif; ?>
</div>
<?php endif; ?>
