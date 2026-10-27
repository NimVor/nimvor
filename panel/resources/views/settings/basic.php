<?php
/**
 * Basic WebHost Settings: contact, basic configuration, name servers and
 * defaults for new accounts.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $values
 * @var array<string, mixed> $mail
 * @var string $hostname
 * @var list<string> $addresses
 * @var list<string> $ns
 * @var list<array<string, mixed>> $plans
 * @var list<string> $phpVersions
 * @var array<string, array<string, mixed>> $definitions
 * @var bool $canEdit
 */
$old ??= [];
$errors = (array) ($this->shared('errors') ?? []);
$val = static fn (string $k): string => (string) ($old[$k] ?? (is_bool($values[$k] ?? null) ? ($values[$k] ? '1' : '0') : ($values[$k] ?? '')));
$err = static fn (string $k): string => isset($errors[$k]) ? '<div class="invalid-feedback d-block">' . e((string) $errors[$k]) . '</div>' : '';
$cls = static fn (string $k): string => isset($errors[$k]) ? ' is-invalid' : '';
$help = static fn (string $k): string => '<div class="form-text">' . e((string) ($definitions[$k]['help'] ?? '')) . '</div>';
$dis = $canEdit ? '' : ' disabled';
$save = $canEdit ? '<button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save</button>' : '';
$switch = function (string $k) use ($definitions, $val, $dis): string {
    return '<div class="mb-3"><div class="form-check form-switch"><input type="hidden" name="' . e($k) . '" value="0"><input class="form-check-input" type="checkbox" role="switch" id="b-' . e($k) . '" name="' . e($k) . '" value="1"' . ($val($k) === '1' ? ' checked' : '') . $dis . '><label class="form-check-label" for="b-' . e($k) . '">' . e((string) $definitions[$k]['label']) . '</label></div><div class="form-text">' . e((string) $definitions[$k]['help']) . '</div></div>';
};
?>
<?php $this->start(); ?>Basic WebHost Settings<?php $this->end('title'); ?>
<?php $this->start(); ?>Contact, panel address, name servers and defaults for new accounts<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('settings.tweak')) ?>"><i class="bi bi-sliders2 me-1" aria-hidden="true"></i>Tweak Settings</a>
<?php $this->end('actions'); ?>

<div class="nim-subtabs mb-3" role="tablist">
    <?php foreach (['contact' => 'Contact information', 'basic' => 'Basic configuration', 'nameservers' => 'Name servers', 'accounts' => 'New accounts'] as $id => $label): ?>
        <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#<?= e($id) ?>" aria-controls="<?= e($id) ?>" aria-selected="<?= $id === 'contact' ? 'true' : 'false' ?>"<?= $id === 'contact' ? ' class="active"' : '' ?>><?= e($label) ?></button>
    <?php endforeach; ?>
</div>
<div class="tab-content">
    <div class="tab-pane fade show active" id="contact" role="tabpanel">
        <form class="nim-panel p-3" method="post" action="<?= e($this->url('settings.basic.save')) ?>" data-nim-busy>
            <?= $this->csrfField() ?><input type="hidden" name="tab" value="contact">
            <label class="form-label" for="b-alert_to">Server contact email</label>
            <input class="form-control<?= $cls('alert_to') ?>" id="b-alert_to" name="alert_to" value="<?= e((string) ($old['alert_to'] ?? $mail['alert_to'])) ?>" maxlength="1000" placeholder="admin@example.com"<?= $dis ?>>
            <?= $err('alert_to') ?>
            <div class="form-text mb-3">Alerts and notifications about this server go here; several addresses separated by commas. They are sent through the email settings (<?= $mail['host'] === '' ? '<strong>not set up yet</strong>' : 'via ' . e((string) $mail['host']) ?>, <a href="<?= e($this->url('settings')) ?>#email">change</a>).</div>
            <?= $save ?>
        </form>
    </div>
    <div class="tab-pane fade" id="basic" role="tabpanel">
        <form class="nim-panel p-3" method="post" action="<?= e($this->url('settings.basic.save')) ?>" data-nim-busy>
            <?= $this->csrfField() ?><input type="hidden" name="tab" value="basic">
            <div class="mb-3">
                <span class="form-label d-block">Server host name</span>
                <span class="font-monospace"><?= $hostname !== '' ? e($hostname) : '<span class="nim-muted">unknown</span>' ?></span>
                <?php if ($this->hasRoute('server') && $this->routeAllowed('server')): ?> · <a href="<?= e($this->url('server')) ?>">change on the Server page</a><?php endif; ?>
                <div class="form-text">A full name (server.example.com) whose address record points to this server: email and the hostname certificate use it.</div>
            </div>
            <?php foreach (['panel_name' => 'text', 'panel_url' => 'url'] as $k => $type): ?>
                <div class="mb-3">
                    <label class="form-label" for="b-<?= e($k) ?>"><?= e((string) $definitions[$k]['label']) ?></label>
                    <input class="form-control<?= $cls($k) ?>" type="<?= e($type) ?>" id="b-<?= e($k) ?>" name="<?= e($k) ?>" value="<?= e($val($k)) ?>" maxlength="<?= (int) ($definitions[$k]['max_length'] ?? 255) ?>"<?= $k === 'panel_name' ? ' required' : ' placeholder="https://panel.example.com"' ?><?= $dis ?>>
                    <?= $err($k) ?><?= $help($k) ?>
                </div>
            <?php endforeach; ?>
            <div class="mb-3">
                <label class="form-label" for="b-dns_default_ipv4"><?= e((string) $definitions['dns_default_ipv4']['label']) ?></label>
                <input class="form-control<?= $cls('dns_default_ipv4') ?>" id="b-dns_default_ipv4" name="dns_default_ipv4" value="<?= e($val('dns_default_ipv4')) ?>" maxlength="15" list="b-ips" placeholder="<?= e($addresses[0] ?? '203.0.113.10') ?>"<?= $dis ?>>
                <datalist id="b-ips"><?php foreach ($addresses as $a): ?><option value="<?= e($a) ?>"></option><?php endforeach; ?></datalist>
                <?= $err('dns_default_ipv4') ?><?= $help('dns_default_ipv4') ?>
                <?php if ($addresses !== []): ?><div class="form-text">This server's addresses: <?= e(implode(', ', $addresses)) ?>.</div><?php endif; ?>
            </div>
            <div class="mb-3">
                <label class="form-label" for="b-dns_hostmaster"><?= e((string) $definitions['dns_hostmaster']['label']) ?></label>
                <input class="form-control<?= $cls('dns_hostmaster') ?>" id="b-dns_hostmaster" name="dns_hostmaster" value="<?= e($val('dns_hostmaster')) ?>" maxlength="200" placeholder="hostmaster@example.com"<?= $dis ?>>
                <?= $err('dns_hostmaster') ?><?= $help('dns_hostmaster') ?>
            </div>
            <?= $save ?>
        </form>
    </div>
    <div class="tab-pane fade" id="nameservers" role="tabpanel">
        <form class="nim-panel p-3" method="post" action="<?= e($this->url('settings.basic.save')) ?>" data-nim-busy>
            <?= $this->csrfField() ?><input type="hidden" name="tab" value="nameservers">
            <p class="small nim-muted">New DNS zones get NS records for these servers. Register them (glue records) at your domain registrar, pointing to this server's address, when this server is the name server.</p>
            <?php foreach ($ns as $i => $n): $f = 'ns' . ($i + 1); ?>
                <div class="mb-3 row g-2 align-items-center">
                    <label class="col-sm-3 col-form-label" for="b-<?= e($f) ?>">Name server <?= $i + 1 ?></label>
                    <div class="col-sm-9">
                        <input class="form-control<?= $cls($f) ?>" id="b-<?= e($f) ?>" name="<?= e($f) ?>" value="<?= e((string) ($old[$f] ?? $n)) ?>" maxlength="253" placeholder="<?= e($f) ?>.example.com"<?= $dis ?>>
                        <?= $err($f) ?>
                    </div>
                </div>
            <?php endforeach; ?>
            <?= $save ?>
        </form>
    </div>
    <div class="tab-pane fade" id="accounts" role="tabpanel">
        <form class="nim-panel p-3" method="post" action="<?= e($this->url('settings.basic.save')) ?>" data-nim-busy>
            <?= $this->csrfField() ?><input type="hidden" name="tab" value="accounts">
            <p class="small nim-muted">What the form for a new account or website starts with; each can still be changed there.</p>
            <div class="row g-3 mb-3">
                <div class="col-md-6">
                    <label class="form-label" for="b-default_plan_id"><?= e((string) $definitions['default_plan_id']['label']) ?></label>
                    <select class="form-select<?= $cls('default_plan_id') ?>" id="b-default_plan_id" name="default_plan_id"<?= $dis ?>>
                        <option value="0">None (unlimited)</option>
                        <?php foreach ($plans as $p): ?><option value="<?= (int) $p['id'] ?>"<?= $val('default_plan_id') === (string) $p['id'] ? ' selected' : '' ?>><?= e((string) $p['name']) ?></option><?php endforeach; ?>
                    </select>
                    <?= $err('default_plan_id') ?><?= $help('default_plan_id') ?>
                </div>
                <div class="col-md-6">
                    <label class="form-label" for="b-default_php_version"><?= e((string) $definitions['default_php_version']['label']) ?></label>
                    <select class="form-select<?= $cls('default_php_version') ?>" id="b-default_php_version" name="default_php_version"<?= $dis ?>>
                        <option value="">The newest installed</option>
                        <?php foreach (array_unique(array_merge($phpVersions, $val('default_php_version') !== '' ? [$val('default_php_version')] : [])) as $pv): ?><option value="<?= e($pv) ?>"<?= $val('default_php_version') === $pv ? ' selected' : '' ?>>PHP <?= e($pv) ?><?= in_array($pv, $phpVersions, true) ? '' : ' (not installed)' ?></option><?php endforeach; ?>
                    </select>
                    <?= $err('default_php_version') ?><?= $help('default_php_version') ?>
                </div>
            </div>
            <?php foreach (['account_dns_dkim', 'account_dns_spf', 'account_dns_dmarc', 'account_spam_filter', 'account_spam_box'] as $k): ?><?= $switch($k) ?><?php endforeach; ?>
            <div class="mb-3">
                <label class="form-label" for="b-account_mail_routing"><?= e((string) $definitions['account_mail_routing']['label']) ?></label>
                <select class="form-select<?= $cls('account_mail_routing') ?>" id="b-account_mail_routing" name="account_mail_routing"<?= $dis ?>>
                    <?php foreach ((array) $definitions['account_mail_routing']['options'] as $o => $l): ?><option value="<?= e((string) $o) ?>"<?= $val('account_mail_routing') === (string) $o ? ' selected' : '' ?>><?= e((string) $l) ?></option><?php endforeach; ?>
                </select>
                <?= $err('account_mail_routing') ?><?= $help('account_mail_routing') ?>
            </div>
            <?= $save ?>
        </form>
    </div>
</div>
