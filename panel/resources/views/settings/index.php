<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, array<string, mixed>> $definitions */
$canEdit = $this->can(\WhaCP\Security\Permissions::SETTINGS_MANAGE);
// Categories: the settings kept here, and the pages that own related ones
// (each keeps its own settings; this page links to them).
$categories = [
    'general' => ['General', 'sliders', []],
    'server' => ['Server', 'hdd-rack', [['server', 'Host name, time zone, root password, restart'], ['services', 'Services'], ['ips', 'IP addresses and IPv6 ranges'], ['packages', 'Packages and updates'], ['ffmpeg', 'FFmpeg'], ['servers', 'All servers']]],
    'security' => ['Security', 'shield-check', [['security', 'Security overview'], ['security.firewall', 'Firewall'], ['security.ssh', 'SSH'], ['security.shell', 'Shell access'], ['security.malware', 'Malware scans'], ['security.policies', 'Security policies (verified IPs, two-factor, password age)'], ['security.password_strength', 'Password strength'], ['security.compilers', 'Compiler access'], ['security.smtp', 'SMTP restrictions']]],
    'websites' => ['Websites and PHP', 'globe', [['websites', 'Websites'], ['php', 'PHP versions, extensions, php.ini and PHP-FPM']]],
    'email' => ['Email', 'envelope', [['mail', 'Email hosting'], ['mail.admin.server', 'Mail server (Postfix, Dovecot)'], ['mail.greylist', 'Greylisting'], ['mail.limits', 'Mail sending limits']]],
    'dns' => ['DNS', 'signpost', [['dns', 'DNS zones']]],
    'ssl' => ['SSL/TLS', 'shield-lock', [['ssl.autossl', 'AutoSSL: run, logs, renewals'], ['ssl.hosts', 'SSL hosts'], ['ssl.certificates', 'Certificate store'], ['ssl.keys', 'Private keys and CSRs'], ['ssl.tls', 'TLS configuration']]],
    'databases' => ['Databases', 'database', [['databases', 'Databases and users'], ['databases.server', 'Database server (MariaDB / MySQL)']]],
    'backups' => ['Backups', 'archive', [['backups', 'Backups, schedules and destinations']]],
    'monitoring' => ['Monitoring and alerts', 'activity', [['monitoring', 'Monitoring'], ['notifications', 'Notifications']]],
    'limits' => ['Resource limits', 'speedometer', [['plans', 'Hosting plans'], ['limits', 'CPU and memory limits (cgroups)'], ['features', 'Feature lists']]],
    'integrations' => ['Integrations', 'plug', [['plugins', 'Plugins'], ['profile', 'API tokens (your profile)']]],
    'system' => ['System', 'info-circle', [['audit', 'Audit log'], ['users', 'Users and roles']]],
];
$byCat = [];
foreach ($definitions as $key => $def) {
    $byCat[$def['category'] ?? 'general'][$key] = $def;
}
$errors = (array) ($this->shared('errors') ?? []);
$first = 'general';
foreach ($byCat as $cat => $defs) {
    if (array_intersect_key($errors, $defs) !== []) {
        $first = $cat;
        break;
    }
}
$field = function (string $key, array $def) use ($old, $values): string {
    if ($def['type'] === 'bool') {
        $on = filter_var($old[$key] ?? $values[$key], FILTER_VALIDATE_BOOL);
        return '<div class="mb-3"><div class="form-check form-switch"><input type="hidden" name="' . e($key) . '" value="0"><input class="form-check-input" type="checkbox" role="switch" id="f-' . e($key) . '" name="' . e($key) . '" value="1"' . ($on ? ' checked' : '') . '><label class="form-check-label" for="f-' . e($key) . '">' . e($def['label']) . '</label></div><div class="form-text">' . e($def['help']) . '</div></div>';
    }
    return $this->partial('partials/field', [
        'name' => $key,
        'label' => $def['label'],
        'type' => ['int' => 'number', 'url' => 'url'][$def['type']] ?? 'text',
        'value' => (string) ($old[$key] ?? $values[$key]),
        'help' => $def['help'],
        'attrs' => $def['type'] === 'int'
            ? ['min' => (string) $def['min'], 'max' => (string) $def['max'], 'required' => 'required']
            : ($def['type'] === 'url'
                ? ['maxlength' => (string) ($def['max_length'] ?? 255), 'placeholder' => 'https://']
                : ['maxlength' => (string) ($def['max_length'] ?? 255)] + (empty($def['optional']) ? ['required' => 'required'] : [])),
    ]);
};
?>
<?php $this->start(); ?>Settings<?php $this->end('title'); ?>
<?php $this->start(); ?>All settings by category; related pages keep their own<?php $this->end('subtitle'); ?>

<div class="row g-3" data-wcp-settings-tabs>
    <div class="col-lg-3">
        <nav class="nav nav-pills flex-column wcp-settings-nav" role="tablist" aria-label="Setting categories" aria-orientation="vertical">
            <?php foreach ($categories as $cat => [$catLabel, $catIcon]): ?>
                <a class="nav-link<?= $cat === $first ? ' active' : '' ?>" id="tab-<?= e($cat) ?>" href="#<?= e($cat) ?>" data-bs-toggle="pill" data-bs-target="#pane-<?= e($cat) ?>" role="tab" aria-controls="pane-<?= e($cat) ?>" aria-selected="<?= $cat === $first ? 'true' : 'false' ?>"><i class="bi bi-<?= e($catIcon) ?> me-2" aria-hidden="true"></i><?= e($catLabel) ?></a>
            <?php endforeach; ?>
        </nav>
    </div>
    <div class="col-lg-9 tab-content">
        <?php foreach ($categories as $cat => [$catLabel, $catIcon, $links]): $defs = $byCat[$cat] ?? []; $links = array_values(array_filter($links, fn (array $l): bool => $this->hasRoute($l[0]) && $this->routeAllowed($l[0]))); ?>
            <section class="tab-pane fade<?= $cat === $first ? ' show active' : '' ?>" id="pane-<?= e($cat) ?>" role="tabpanel" aria-labelledby="tab-<?= e($cat) ?>" tabindex="0">
                <?php if ($defs !== []): ?>
                    <div class="card mb-3">
                        <div class="card-header"><i class="bi bi-<?= e($catIcon) ?> me-1"></i><?= e($catLabel) ?></div>
                        <div class="card-body">
                            <form method="post" action="<?= e($this->url('settings.update')) ?>" data-wcp-busy novalidate>
                                <?= $this->csrfField() ?><input type="hidden" name="tab" value="<?= e($cat) ?>">
                                <fieldset<?= $canEdit ? '' : ' disabled' ?>>
                                    <?php foreach ($defs as $key => $def): ?><?= $field($key, $def) ?><?php endforeach; ?>
                                    <?php if ($canEdit): ?><button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save</button><?php endif; ?>
                                </fieldset>
                            </form>
                        </div>
                    </div>
                <?php endif; ?>
                <?php if ($cat === 'email'): ?>
                <div class="card">
                    <div class="card-header"><i class="bi bi-envelope me-1"></i>Email</div>
                    <div class="card-body">
                        <p class="small text-body-secondary">Used for alert notifications and “forgot password” links (which also need the public panel URL under General). Leave the host empty to turn email off.</p>
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
                <?php endif; ?>
                <?php if ($cat === 'system'): ?>
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
                <?php endif; ?>
                <?php if ($links !== []): ?>
                    <div class="card<?= $cat === 'email' || $cat === 'system' ? ' mt-3' : '' ?>">
                        <div class="card-header">Also for <?= e(mb_strtolower($catLabel)) ?></div>
                        <div class="list-group list-group-flush">
                            <?php foreach ($links as [$linkRoute, $linkText]): ?><a class="list-group-item list-group-item-action d-flex align-items-center" href="<?= e($this->url($linkRoute)) ?>"><?= e($linkText) ?><i class="bi bi-chevron-right ms-auto" aria-hidden="true"></i></a><?php endforeach; ?>
                        </div>
                    </div>
                <?php elseif ($defs === [] && $cat !== 'email' && $cat !== 'system'): ?>
                    <div class="card"><div class="card-body text-body-secondary">No settings here.</div></div>
                <?php endif; ?>
            </section>
        <?php endforeach; ?>
    </div>
</div>
