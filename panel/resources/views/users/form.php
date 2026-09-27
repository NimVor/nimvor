<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $editUser */
$isNew = $editUser === null;
$val = static fn (string $k, mixed $default = '') => $old[$k] ?? ($editUser[$k] ?? $default);
$roleId = (int) $val('role_id', 0);
$active = $isNew ? ($old['is_active'] ?? '1') === '1' : (isset($old['is_active']) ? (bool) $old['is_active'] : (int) $editUser['is_active'] === 1);
?>
<?php $this->start(); ?><?= $isNew ? 'New panel user' : 'Edit ' . e($editUser['username']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('users')) ?>"><i class="bi bi-arrow-left"></i> Users</a><?php $this->end('actions'); ?>

<div class="row">
    <div class="col-lg-7 col-xl-6">
        <div class="card">
            <div class="card-body">
                <form method="post" action="<?= e($isNew ? $this->url('users.store') : $this->url('users.update', ['id' => (int) $editUser['id']])) ?>" data-wcp-busy novalidate>
                    <?= $this->csrfField() ?>
                    <?php if ($isNew): ?>
                        <?= $this->partial('partials/field', ['name' => 'username', 'label' => 'Username', 'type' => 'text', 'value' => (string) $val('username'), 'help' => '3–32 characters: lowercase letters, digits, dot, dash, underscore.', 'attrs' => ['required' => 'required', 'autocomplete' => 'off', 'maxlength' => '32']]) ?>
                    <?php else: ?>
                        <div class="mb-3"><label class="form-label">Username</label><input class="form-control" value="<?= e($editUser['username']) ?>" disabled></div>
                    <?php endif; ?>
                    <?= $this->partial('partials/field', ['name' => 'email', 'label' => 'Email', 'type' => 'email', 'value' => (string) $val('email'), 'attrs' => ['required' => 'required', 'maxlength' => '190']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'display_name', 'label' => 'Display name', 'type' => 'text', 'value' => (string) $val('display_name'), 'help' => 'Optional.', 'attrs' => ['maxlength' => '100']]) ?>

                    <div class="mb-3">
                        <label class="form-label" for="f-role_id">Role</label>
                        <select class="form-select<?= isset($errors['role_id']) ? ' is-invalid' : '' ?>" id="f-role_id" name="role_id">
                            <?php foreach ($roles as $r): ?>
                                <option value="<?= (int) $r['id'] ?>"<?= $roleId === (int) $r['id'] || ($roleId === 0 && $r['name'] === 'viewer') ? ' selected' : '' ?>><?= e($r['label']) ?> — <?= e($r['description']) ?></option>
                            <?php endforeach; ?>
                        </select>
                        <?php if (isset($errors['role_id'])): ?><div class="invalid-feedback"><?= e($errors['role_id']) ?></div><?php endif; ?>
                    </div>

                    <?php $planId = (int) $val('plan_id', 0); ?>
                    <div class="mb-3">
                        <label class="form-label" for="f-plan_id">Hosting plan</label>
                        <select class="form-select<?= isset($errors['plan_id']) ? ' is-invalid' : '' ?>" id="f-plan_id" name="plan_id">
                            <option value="">No plan (unlimited)</option>
                            <?php foreach ($plans ?? [] as $p): ?><option value="<?= (int) $p['id'] ?>"<?= $planId === (int) $p['id'] ? ' selected' : '' ?>><?= e($p['name']) ?></option><?php endforeach; ?>
                        </select>
                        <?php if (isset($errors['plan_id'])): ?><div class="invalid-feedback"><?= e($errors['plan_id']) ?></div><?php endif; ?>
                        <div class="form-text">Limits websites, databases, cron jobs, workers and disk space. Plans apply to hosting customers and resellers.</div>
                    </div>

                    <div class="form-check form-switch mb-3">
                        <input type="hidden" name="is_active" value="0">
                        <input class="form-check-input<?= isset($errors['is_active']) ? ' is-invalid' : '' ?>" type="checkbox" role="switch" id="f-is_active" name="is_active" value="1"<?= $active ? ' checked' : '' ?>>
                        <label class="form-check-label" for="f-is_active">Account enabled</label>
                        <?php if (isset($errors['is_active'])): ?><div class="invalid-feedback"><?= e($errors['is_active']) ?></div><?php endif; ?>
                    </div>

                    <hr>
                    <p class="small text-body-secondary"><?= $isNew ? 'Choose a password of at least 12 characters.' : 'Leave empty to keep the current password.' ?></p>
                    <?= $this->partial('partials/field', ['name' => 'password', 'label' => $isNew ? 'Password' : 'New password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'new-password'] + ($isNew ? ['required' => 'required'] : [])]) ?>
                    <?= $this->partial('partials/field', ['name' => 'password_confirmation', 'label' => 'Confirm password', 'type' => 'password', 'value' => '', 'attrs' => ['autocomplete' => 'new-password']]) ?>

                    <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $isNew ? 'Create user' : 'Save changes' ?></button>
                </form>
            </div>
        </div>
    </div>
    <?php if (!$isNew): ?>
        <div class="col-lg-5 col-xl-4">
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-phone me-1"></i>Two-factor sign-in</div>
                <div class="card-body small">
                    <?php if (!empty($twoFactor)): ?>
                        <p>On since <?= e(substr((string) $twoFactor['enabled_at'], 0, 10)) ?> · <?= (int) $twoFactor['recovery_left'] ?> recovery codes left.</p>
                        <form method="post" action="<?= e($this->url('users.2fa.reset', ['id' => $editUser['id']])) ?>" data-wcp-confirm="Turn off two-factor sign-in for <?= e($editUser['username']) ?>? Do this only after confirming their identity, e.g. when they lost their phone and recovery codes.">
                            <?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Reset two-factor sign-in</button>
                        </form>
                    <?php else: ?>
                        <p class="mb-0 text-body-secondary">Off. Users turn it on from their own profile.</p>
                    <?php endif; ?>
                </div>
            </div>
    <?php endif; ?>
    <?php if (!$isNew && ($editUser['role_scope'] ?? 'all') === 'own' && $this->serverScope() && $this->can(\WhaCP\Security\Permissions::USERS_MANAGE)): $own = $editUser['suspended_at'] !== null; ?>
            <div class="card mb-3<?= !empty($editUser['suspended']) ? ' border-danger' : '' ?>" id="account-suspension">
                <div class="card-header"><i class="bi bi-pause-circle me-1"></i>Account status</div>
                <div class="card-body small">
                    <?php if ($own): ?>
                        <p class="mb-2"><span class="wcp-state is-error">Suspended</span> since <?= e(str_replace('T', ' ', substr((string) $editUser['suspended_at'], 0, 16))) ?> UTC<?= $editUser['suspend_reason'] !== '' ? ' — ' . e($editUser['suspend_reason']) : '' ?></p>
                        <form method="post" action="<?= e($this->url('users.unsuspend', ['id' => (int) $editUser['id']])) ?>" data-wcp-busy data-wcp-confirm="Resume <?= e($editUser['username']) ?>? Its websites, email, databases, FTP/SFTP, cron jobs and workers are turned back on<?= $editUser['role_name'] === 'reseller' ? ', for its customers too (except customers suspended on their own)' : '' ?>.">
                            <?= $this->csrfField() ?><button class="btn btn-sm btn-success"><i class="bi bi-play-circle me-1"></i>Unsuspend</button>
                        </form>
                        <form class="mt-2" method="post" action="<?= e($this->url('users.suspend', ['id' => (int) $editUser['id']])) ?>" data-wcp-busy>
                            <?= $this->csrfField() ?><input type="hidden" name="reason" value="<?= e($editUser['suspend_reason']) ?>"><button class="btn btn-sm btn-link p-0">Apply the suspension again (retry services that failed)</button>
                        </form>
                    <?php elseif (!empty($editUser['suspended'])): ?>
                        <p class="mb-0"><span class="wcp-state is-error">Suspended</span> through its reseller.</p>
                    <?php else: ?>
                        <p class="mb-2"><span class="wcp-state is-ok">Active</span></p>
                        <button class="btn btn-sm btn-outline-danger" type="button" data-bs-toggle="modal" data-bs-target="#suspend-modal"><i class="bi bi-pause-circle me-1"></i>Suspend account</button>
                    <?php endif; ?>
                </div>
            </div>
            <?php if (!$own && empty($editUser['suspended'])): ?>
            <div class="modal fade" id="suspend-modal" tabindex="-1" aria-labelledby="suspend-title" aria-hidden="true">
                <div class="modal-dialog">
                    <form class="modal-content" method="post" action="<?= e($this->url('users.suspend', ['id' => (int) $editUser['id']])) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <div class="modal-header"><h2 class="modal-title fs-5" id="suspend-title">Suspend <?= e($editUser['username']) ?></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
                        <div class="modal-body small">
                            <p>The account<?= $editUser['role_name'] === 'reseller' ? ' and all its customers' : '' ?> can no longer sign in, and on every server:</p>
                            <ul>
                                <li>its websites go offline (their own settings are kept);</li>
                                <li>email logins are refused (IMAP, POP3, webmail, sending) — incoming mail is still delivered;</li>
                                <li>database users are locked, FTP/SFTP/SSH logins refused;</li>
                                <li>cron jobs and workers stop.</li>
                            </ul>
                            <p class="mb-2">Nothing is deleted. Unsuspending turns everything back on.</p>
                            <label class="form-label" for="suspend-reason">Reason (shown to administrators)</label>
                            <input class="form-control" id="suspend-reason" name="reason" maxlength="250" placeholder="e.g. unpaid invoice">
                        </div>
                        <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-danger"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Suspend</button></div>
                    </form>
                </div>
            </div>
            <?php endif; ?>
    <?php endif; ?>
    <?php if (!$isNew && !empty($hostingOptions)): $ho = $hostingOptions; $sw = static function (string $k, string $label) use ($ho): string { return '<div class="form-check form-switch mb-1"><input type="hidden" name="' . e($k) . '" value="0"><input class="form-check-input" type="checkbox" role="switch" id="ho-' . e($k) . '" name="' . e($k) . '" value="1"' . (!empty($ho[$k]) ? ' checked' : '') . '><label class="form-check-label" for="ho-' . e($k) . '">' . e($label) . '</label></div>'; }; ?>
            <div class="card mb-3" id="hosting-options">
                <div class="card-header"><i class="bi bi-sliders me-1"></i>Hosting options</div>
                <div class="card-body small">
                    <form method="post" action="<?= e($this->url('users.options', ['id' => (int) $editUser['id']])) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <div class="fw-semibold mb-1">DNS for new websites</div>
                        <?= $sw('dns_dkim', 'DKIM') . $sw('dns_spf', 'SPF') . $sw('dns_dmarc', 'DMARC (needs DKIM and SPF)') . $sw('dns_registrar_ns', 'Use the registrar\'s name servers (no zone here)') . $sw('dns_overwrite', 'Overwrite an existing zone') ?>
                        <div class="fw-semibold mt-2 mb-1">Mail</div>
                        <?= $sw('spam_filter', 'Spam filter') . $sw('spam_box', 'Spam Box (Junk folder)') ?>
                        <label class="form-label mt-1 mb-1" for="ho-mr">Mail routing</label>
                        <select class="form-select form-select-sm mb-2" id="ho-mr" name="mail_routing">
                            <?php foreach (['auto' => 'Automatically detect', 'local' => 'Local mail exchanger', 'backup' => 'Backup mail exchanger', 'remote' => 'Remote mail exchanger'] as $k => $l): ?><option value="<?= e($k) ?>"<?= ($ho['mail_routing'] ?? 'auto') === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?>
                        </select>
                        <button class="btn btn-sm btn-primary">Save options</button>
                    </form>
                </div>
            </div>
    <?php endif; ?>
    <?php if (!$isNew && !empty($usage)): ?>
            <div class="card">
                <div class="card-header"><i class="bi bi-speedometer me-1"></i>Usage</div>
                <ul class="list-group list-group-flush small">
                    <?php foreach ($usage as $u): $pct = $u['limit'] > 0 ? min(100, 100 * $u['used'] / $u['limit']) : 0; ?>
                        <li class="list-group-item">
                            <div class="d-flex"><span><?= e($u['label']) ?></span><span class="ms-auto"><?= (int) $u['used'] ?> / <?= $u['limit'] > 0 ? (int) $u['limit'] : '∞' ?></span></div>
                            <?php if ($u['limit'] > 0): ?><div class="progress mt-1" role="progressbar" aria-valuenow="<?= (int) $pct ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar<?= $pct >= 100 ? ' bg-danger' : ($pct >= 80 ? ' bg-warning' : '') ?>" data-width="<?= (int) $pct ?>"></div></div><?php endif; ?>
                        </li>
                    <?php endforeach; ?>
                </ul>
                <div class="card-footer small text-body-secondary">Includes the customers of a reseller.</div>
            </div>
    <?php endif; ?>
    <?php if (!$isNew): ?>
        </div>
    <?php endif; ?>
</div>
