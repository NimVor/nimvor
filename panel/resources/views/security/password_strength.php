<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $values */
/** @var array<string, array<string, mixed>> $definitions */
/** @var list<array{0: string, 1: int}> $examples */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
$fields = ['password_min_strength' => 'Password strength', 'password_min_length' => 'Number of total characters', 'password_min_special' => 'Number of special characters', 'password_min_numeric' => 'Number of numeric characters', 'password_min_alpha' => 'Number of alphabet characters', 'password_min_upper' => 'Number of uppercase characters', 'password_min_lower' => 'Number of lowercase characters'];
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>

<div class="wcp-panel mb-3">
    <p class="mb-2">The minimum requirements for new passwords of NimVor users, mailboxes (webmail) and the server's root password. Existing passwords are not changed; use Password Age (Configure Security Policies) to make users choose new ones.</p>
    <div class="d-flex flex-wrap align-items-center gap-2">
        <span class="fw-medium">Enforcement:</span>
        <?= !empty($values['policy_password_strength']) ? '<span class="wcp-state is-ok">On</span>' : '<span class="wcp-state is-off">Off</span>' ?>
        <a class="small" href="<?= e($this->url('security.policies')) ?>">Configure Security Policies</a>
    </div>
</div>

<form class="wcp-panel mb-3" method="post" action="<?= e($this->url('security.password_strength.save')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?>
    <div class="row g-3">
        <?php foreach ($fields as $key => $label): $d = $definitions[$key]; ?>
            <div class="col-md-6 col-xl-4">
                <label class="form-label fw-medium" for="ps-<?= e($key) ?>"><?= e($label) ?></label>
                <input class="form-control form-control-sm" type="number" id="ps-<?= e($key) ?>" name="<?= e($key) ?>" min="<?= (int) $d['min'] ?>" max="<?= (int) $d['max'] ?>" value="<?= (int) $values[$key] ?>" required<?= $canManage ? '' : ' disabled' ?>>
                <div class="form-text"><?= e((string) $d['help']) ?><?= $key === 'password_min_strength' ? ' ' . \WhaCP\Security\PasswordPolicy::STRONG . ' or more counts as strong.' : '' ?></div>
            </div>
        <?php endforeach; ?>
    </div>
    <?php if ($canManage): ?><button class="btn btn-primary btn-sm mt-3">Save</button><?php endif; ?>
</form>

<div class="wcp-panel">
    <div class="fw-semibold mb-2">Strength examples</div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Password</th><th>Strength</th></tr></thead>
            <tbody>
            <?php foreach ($examples as [$p, $s]): ?>
                <tr><td class="font-monospace"><?= e($p) ?></td><td><?= (int) $s ?></td></tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
