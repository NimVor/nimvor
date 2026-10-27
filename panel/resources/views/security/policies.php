<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $values */
/** @var list<array<string, mixed>> $roles */
/** @var list<array<string, mixed>> $myIps */
/** @var string $rules */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
$dis = $canManage ? '' : ' disabled';
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>

<div class="wcp-panel mb-3">
    <p class="mb-0">This interface provides the configuration settings of the Security Policy that apply to WhaCP, WhaCP webmail, and on the server.</p>
</div>

<form method="post" action="<?= e($this->url('security.policies.save')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?>
    <div class="wcp-panel mb-3">
        <div class="fw-semibold mb-3">Security Policy Items</div>

        <div class="border-bottom pb-3 mb-3">
            <div class="form-check form-switch">
                <input type="hidden" name="policy_verified_ips" value="0">
                <input class="form-check-input" type="checkbox" role="switch" id="p-ips" name="policy_verified_ips" value="1"<?= !empty($values['policy_verified_ips']) ? ' checked' : '' ?><?= $dis ?>>
                <label class="form-check-label fw-medium" for="p-ips">Limit logins to verified IP addresses</label>
            </div>
            <div class="small text-body-secondary mt-1">A WhaCP sign-in from an IP address the user has not verified needs a 6-digit code sent to the user's email address. Addresses users sign in from are remembered; when you turn this on, each user's last sign-in address counts as verified. Users without an email address can only sign in from verified addresses. On the server, <code>sudo -u whacp -E php /opt/whacp/panel/bin/whacp user:verify-ip &lt;user&gt; &lt;ip&gt;</code> verifies an address for a user.</div>
        </div>

        <div class="border-bottom pb-3 mb-3">
            <div class="fw-medium">Two-Factor Authentication: Google Authenticator</div>
            <div class="small text-body-secondary mb-2">Users of the selected roles must set up an authenticator app (Google Authenticator or any TOTP app) or a security key before they can use WhaCP.</div>
            <?php foreach ($roles as $role): ?>
                <div class="form-check form-check-inline">
                    <input class="form-check-input" type="checkbox" name="two_factor_roles[]" value="<?= (int) $role['id'] ?>" id="p-2fa-<?= (int) $role['id'] ?>"<?= !empty($role['require_two_factor']) ? ' checked' : '' ?><?= $dis ?>>
                    <label class="form-check-label" for="p-2fa-<?= (int) $role['id'] ?>"><?= e((string) ($role['label'] ?? $role['name'])) ?></label>
                </div>
            <?php endforeach; ?>
        </div>

        <div class="border-bottom pb-3 mb-3">
            <div class="form-check form-switch">
                <input type="hidden" name="policy_password_strength" value="0">
                <input class="form-check-input" type="checkbox" role="switch" id="p-strength" name="policy_password_strength" value="1"<?= !empty($values['policy_password_strength']) ? ' checked' : '' ?><?= $dis ?>>
                <label class="form-check-label fw-medium" for="p-strength">Password Strength</label>
            </div>
            <div class="small text-body-secondary mt-1">New passwords of WhaCP users, mailboxes (webmail) and the server's root password must meet the <a href="<?= e($this->url('security.password_strength')) ?>">Password Strength Configuration</a>: <?= e($rules) ?></div>
        </div>

        <div class="mb-3">
            <label class="form-label fw-medium" for="p-age">Password Age</label>
            <div class="input-group input-group-sm wcp-w-30">
                <input class="form-control" type="number" min="0" max="3650" id="p-age" name="policy_password_age_days" value="<?= (int) $values['policy_password_age_days'] ?>"<?= $dis ?>>
                <span class="input-group-text">days</span>
            </div>
            <div class="small text-body-secondary mt-1">WhaCP users must choose a new password when theirs is older than this. 0 turns it off.</div>
        </div>
        <?php if ($canManage): ?><button class="btn btn-primary btn-sm">Save</button><?php endif; ?>
    </div>
</form>

<div class="wcp-panel" id="verified-ips">
    <div class="fw-semibold mb-2">Your verified IP addresses</div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>IP address</th><th>Verified</th><th>Last used</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($myIps as $ip): ?>
                <tr>
                    <td class="font-monospace"><?= e((string) $ip['ip']) ?></td>
                    <td class="small"><?= e((string) $ip['verified_at']) ?></td>
                    <td class="small"><?= e((string) ($ip['last_used_at'] ?? '')) ?></td>
                    <td class="text-end">
                        <form method="post" action="<?= e($this->url('security.policies.forget_ip')) ?>" data-wcp-confirm="Forget this address? Signing in from it will need a code again.">
                            <?= $this->csrfField() ?><input type="hidden" name="id" value="<?= (int) $ip['id'] ?>">
                            <button class="btn btn-sm btn-outline-danger">Forget</button>
                        </form>
                    </td>
                </tr>
            <?php endforeach; ?>
            <?php if ($myIps === []): ?><tr><td colspan="4" class="text-center py-4 wcp-muted">None yet.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
