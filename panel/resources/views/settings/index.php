<?php
/** @var \WhaCP\View\View $this */
$canEdit = $this->can(\WhaCP\Security\Permissions::SETTINGS_MANAGE);
?>
<?php $this->start(); ?>Settings<?php $this->end('title'); ?>

<div class="row g-3">
    <div class="col-lg-7">
        <div class="card">
            <div class="card-header"><i class="bi bi-sliders me-1"></i>Panel settings</div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('settings.update')) ?>" data-wcp-busy novalidate>
                    <?= $this->csrfField() ?>
                    <fieldset<?= $canEdit ? '' : ' disabled' ?>>
                        <?php foreach ($definitions as $key => $def): ?>
                            <?php if ($def['type'] === 'bool'): $on = filter_var($old[$key] ?? $values[$key], FILTER_VALIDATE_BOOL); ?>
                                <div class="mb-3">
                                    <div class="form-check form-switch">
                                        <input type="hidden" name="<?= e($key) ?>" value="0">
                                        <input class="form-check-input" type="checkbox" role="switch" id="f-<?= e($key) ?>" name="<?= e($key) ?>" value="1"<?= $on ? ' checked' : '' ?>>
                                        <label class="form-check-label" for="f-<?= e($key) ?>"><?= e($def['label']) ?></label>
                                    </div>
                                    <div class="form-text"><?= e($def['help']) ?></div>
                                </div>
                                <?php continue; ?>
                            <?php endif; ?>
                            <?= $this->partial('partials/field', [
                                'name' => $key,
                                'label' => $def['label'],
                                'type' => ['int' => 'number', 'url' => 'url'][$def['type']] ?? 'text',
                                'value' => (string) ($old[$key] ?? $values[$key]),
                                'help' => $def['help'],
                                'attrs' => $def['type'] === 'int'
                                    ? ['min' => (string) $def['min'], 'max' => (string) $def['max'], 'required' => 'required']
                                    : ($def['type'] === 'url'
                                        ? ['maxlength' => (string) ($def['max_length'] ?? 255), 'placeholder' => 'https://']
                                        : ['maxlength' => (string) ($def['max_length'] ?? 255), 'required' => 'required']),
                            ]) ?>
                        <?php endforeach; ?>
                        <?php if ($canEdit): ?>
                            <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save settings</button>
                        <?php endif; ?>
                    </fieldset>
                </form>
            </div>
        </div>

        <div class="card mt-3" id="email">
            <div class="card-header"><i class="bi bi-envelope me-1"></i>Email</div>
            <div class="card-body">
                <p class="small text-body-secondary">Used for alert notifications and “forgot password” links (which also need the public panel URL above). Leave the host empty to turn email off.</p>
                <form method="post" action="<?= e($this->url('settings.mail')) ?>" data-wcp-busy novalidate>
                    <?= $this->csrfField() ?>
                    <fieldset<?= $canEdit ? '' : ' disabled' ?>>
                    <div class="row g-2">
                        <div class="col-sm-8"><?= $this->partial('partials/field', ['name' => 'host', 'label' => 'SMTP server', 'type' => 'text', 'value' => (string) ($old['host'] ?? $mail['host']), 'attrs' => ['maxlength' => '253', 'placeholder' => 'smtp.example.com']]) ?></div>
                        <div class="col-sm-4"><?= $this->partial('partials/field', ['name' => 'port', 'label' => 'Port', 'type' => 'number', 'value' => (string) ($old['port'] ?? $mail['port']), 'attrs' => ['min' => '1', 'max' => '65535']]) ?></div>
                    </div>
                    <div class="mb-3">
                        <label class="form-label" for="mail-enc">Encryption</label>
                        <select class="form-select" id="mail-enc" name="encryption">
                            <?php foreach (\WhaCP\Mail\MailConfig::ENCRYPTION as $k => $label): ?><option value="<?= e($k) ?>"<?= ($old['encryption'] ?? $mail['encryption']) === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                        </select>
                    </div>
                    <div class="row g-2">
                        <div class="col-sm-6"><?= $this->partial('partials/field', ['name' => 'username', 'label' => 'User name', 'type' => 'text', 'value' => (string) ($old['username'] ?? $mail['username']), 'attrs' => ['autocomplete' => 'off', 'maxlength' => '200']]) ?></div>
                        <div class="col-sm-6"><?= $this->partial('partials/field', ['name' => 'password', 'label' => 'Password', 'type' => 'password', 'value' => '', 'help' => $mail['has_password'] ? 'Stored (encrypted). Leave empty to keep it.' : 'Not set.', 'attrs' => ['autocomplete' => 'new-password']]) ?></div>
                    </div>
                    <?php if ($mail['has_password']): ?><div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="clear_password" value="1" id="mail-clear"><label class="form-check-label small" for="mail-clear">Remove the stored password</label></div><?php endif; ?>
                    <?= $this->partial('partials/field', ['name' => 'from', 'label' => 'Sender address', 'type' => 'email', 'value' => (string) ($old['from'] ?? $mail['from']), 'attrs' => ['maxlength' => '254', 'placeholder' => 'panel@example.com']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'alert_to', 'label' => 'Send alerts to', 'type' => 'text', 'value' => (string) ($old['alert_to'] ?? $mail['alert_to']), 'help' => 'Comma-separated addresses. Empty: no alert emails.', 'attrs' => ['maxlength' => '1000']]) ?>
                    <?php if ($canEdit): ?>
                        <button class="btn btn-primary" type="submit">Save email settings</button>
                        <button class="btn btn-outline-secondary" type="submit" formaction="<?= e($this->url('settings.mail.test')) ?>"<?= $mail['host'] === '' ? ' disabled' : '' ?>>Send test message to me</button>
                    <?php endif; ?>
                    </fieldset>
                </form>
            </div>
        </div>
    </div>
    <div class="col-lg-5">
        <div class="card">
            <div class="card-header"><i class="bi bi-info-circle me-1"></i>Installation</div>
            <div class="card-body">
                <dl class="wcp-dl">
                    <?php foreach ($system as $k => $v): ?>
                        <dt><?= e($k) ?></dt><dd><?= e($v) ?></dd>
                    <?php endforeach; ?>
                </dl>
                <p class="small text-body-secondary mb-0">Infrastructure settings (socket paths, database location, cookies) live in <code>/etc/whacp/panel.php</code> and are not editable from the browser.</p>
            </div>
        </div>
    </div>
</div>
