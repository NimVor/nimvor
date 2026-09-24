<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $ssh */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE);
$eff = (array) ($ssh['effective'] ?? []);
$root = ($eff['permitrootlogin'] ?? '') === 'without-password' ? 'prohibit-password' : ($eff['permitrootlogin'] ?? 'prohibit-password');
$admins = (array) ($ssh['admins_with_keys'] ?? []);
$hasKey = !empty($ssh['root_has_keys']) || $admins !== [];
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($ssh !== []): ?>
<div class="row g-3">
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-terminal me-1"></i>Effective settings</div>
            <div class="card-body">
                <dl class="wcp-dl mb-0">
                    <dt>Port</dt><dd><?= e($eff['port'] ?? '') ?></dd>
                    <dt>Root login</dt><dd><?= e($root) ?></dd>
                    <dt>Password logins</dt><dd><?= ($eff['passwordauthentication'] ?? '') === 'yes' ? $this->statusBadge('warning', 'allowed') : $this->statusBadge('ok', 'off') ?></dd>
                    <dt>Key logins</dt><dd><?= e($eff['pubkeyauthentication'] ?? '') ?></dd>
                    <dt>Max. attempts</dt><dd><?= e($eff['maxauthtries'] ?? '') ?></dd>
                    <dt>Keys installed</dt><dd class="small"><?= !empty($ssh['root_has_keys']) ? 'root' : '' ?><?= !empty($ssh['root_has_keys']) && $admins !== [] ? ', ' : '' ?><?= e(implode(', ', $admins)) ?><?= $hasKey ? '' : '<span class="text-warning">none — add a key before turning off passwords</span>' ?></dd>
                </dl>
            </div>
            <div class="card-footer small text-body-secondary">WhaCP manages <code><?= e($ssh['managed_file'] ?? '') ?></code> only.</div>
        </div>
    </div>
    <?php if ($canManage): ?>
        <div class="col-lg-6">
            <div class="card">
                <div class="card-header"><i class="bi bi-shield-lock me-1"></i>Harden SSH</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('security.ssh.configure')) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <?= $this->partial('partials/field', ['name' => 'port', 'label' => 'Port', 'type' => 'number', 'value' => (string) ($eff['port'] ?? '22'), 'help' => 'When the firewall is active, a rule for a new port is added automatically.', 'attrs' => ['min' => '1', 'max' => '65535']]) ?>
                        <label class="form-label" for="s-root">Root login</label>
                        <select class="form-select mb-3" id="s-root" name="permit_root_login">
                            <?php foreach (['prohibit-password' => 'With SSH keys only', 'no' => 'Not allowed', 'yes' => 'With password or key (not recommended)'] as $k => $l): ?><option value="<?= e($k) ?>"<?= $root === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?>
                        </select>
                        <div class="form-check form-switch"><input type="hidden" name="password_authentication" value="0"><input class="form-check-input" type="checkbox" role="switch" id="s-pw" name="password_authentication" value="1"<?= ($eff['passwordauthentication'] ?? '') === 'yes' ? ' checked' : '' ?>><label class="form-check-label" for="s-pw">Allow password logins</label></div>
                        <div class="form-check form-switch mb-3"><input type="hidden" name="x11_forwarding" value="0"><input class="form-check-input" type="checkbox" role="switch" id="s-x11" name="x11_forwarding" value="1"<?= ($eff['x11forwarding'] ?? '') === 'yes' ? ' checked' : '' ?>><label class="form-check-label" for="s-x11">X11 forwarding</label></div>
                        <?= $this->partial('partials/field', ['name' => 'max_auth_tries', 'label' => 'Max. attempts per connection', 'type' => 'number', 'value' => (string) min(10, max(1, (int) ($eff['maxauthtries'] ?? 4))), 'attrs' => ['min' => '1', 'max' => '10']]) ?>
                        <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Validate and apply</button>
                        <div class="form-text">Checked with <code>sshd -t</code> and undone after 2 minutes unless you confirm. Keep your current session open and test a new connection before confirming.</div>
                    </form>
                </div>
            </div>
        </div>
    <?php endif; ?>
</div>
<?php endif; ?>
