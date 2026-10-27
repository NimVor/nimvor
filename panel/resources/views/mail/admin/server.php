<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed>|null $status */
/** @var array<string, mixed>|null $log */
$canManage = $this->can(\NimVor\Security\Permissions::SERVER_MANAGE);
$canServices = $this->can(\NimVor\Security\Permissions::SERVICES_MANAGE);
// Postfix values as the form shows them (MB, days).
$shown = static function (string $k, string $v): string {
    return match ($k) {
        'message_size_limit' => $v === '' ? '' : (string) (int) round((int) $v / 1048576),
        'maximal_queue_lifetime', 'bounce_queue_lifetime' => (string) (int) $v,
        default => $v,
    };
};
// Related settings together; keys the agent adds later go last.
$order = ['message_size_limit', 'smtpd_recipient_limit', 'maximal_queue_lifetime', 'bounce_queue_lifetime', 'smtpd_client_connection_rate_limit', 'smtpd_client_message_rate_limit', 'smtpd_helo_required', 'disable_vrfy_command', 'smtp_tls_security_level', 'inet_protocols', 'relayhost', 'mail_max_userip_connections', 'auth_failure_delay', 'ssl_min_protocol', 'login_greeting'];
$ordered = static fn (array $labels): array => array_replace(array_intersect_key(array_flip($order), $labels), $labels);
$choices = ['smtpd_helo_required' => ['yes', 'no'], 'disable_vrfy_command' => ['yes', 'no'], 'smtp_tls_security_level' => ['may', 'encrypt', 'dane'], 'inet_protocols' => ['all', 'ipv4', 'ipv6'], 'ssl_min_protocol' => ['', 'TLSv1.2', 'TLSv1.3'], 'authorized_submit_users' => ['!nobody, static:anyone', 'static:anyone']];
?>
<?= $this->partial('mail/admin/_tabs', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>
<?php $configured = ($status['configured'] ?? true) !== false; // older agents: assume set up
if (!$configured): $canManage = false; ?>
<div class="nim-infobar mb-3"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>Email is not set up on this server yet, so these settings cannot be changed. Set it up on the <a href="<?= e($this->url('mail')) ?>">Email</a> page (<strong>Set up and start</strong>): NimVor then writes the Postfix and Dovecot configuration, creates the DKIM service and starts the mail services at boot.</span></div>
<?php endif; ?>
<div class="nim-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#mail-services" aria-controls="mail-services" aria-selected="true"><i class="bi bi-hdd-stack me-1"></i>Services</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#mail-postfix" aria-controls="mail-postfix" aria-selected="false"><i class="bi bi-send me-1"></i>Postfix</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#mail-dovecot" aria-controls="mail-dovecot" aria-selected="false"><i class="bi bi-inbox me-1"></i>Dovecot</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#mail-log" aria-controls="mail-log" aria-selected="false"><i class="bi bi-journal-text me-1"></i>Mail log</button>
</div>
<div class="tab-content">
<div class="tab-pane fade show active" id="mail-services" role="tabpanel">
<div class="nim-panel mb-3">
    <div class="nim-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Service</th><th>Version</th><th>State</th><th>Configuration</th><th></th></tr></thead>
            <tbody>
            <?php foreach ((array) $status['services'] as $s): $check = ['Postfix' => $status['checks']['postfix'] ?? null, 'Dovecot' => $status['checks']['dovecot'] ?? null][$s['name']] ?? null; ?>
                <tr>
                    <td class="fw-semibold"><?= e($s['name']) ?><div class="small nim-muted font-monospace"><?= e($s['unit']) ?></div></td>
                    <td class="small"><?= e($s['version'] !== '' ? $s['version'] : '—') ?></td>
                    <?php $missing = ($s['installed'] ?? true) === false; ?>
                    <td class="small"><?php if ($missing): ?><span class="nim-state">Not installed</span> <span class="nim-muted"><?= $s['name'] === 'OpenDKIM' ? 'created by Email → Set up and start' : 'install it under Server → Packages' ?></span><?php else: ?><?= $s['active'] ? '<span class="nim-state is-ok">Running</span>' : '<span class="nim-state is-error">Stopped</span>' ?> <span class="nim-muted"><?= $s['enabled'] ? 'starts at boot' : 'not started at boot' ?></span><?php endif; ?></td>
                    <td class="small"><?php if ($check === null): ?><span class="nim-muted">—</span><?php elseif ($check === ''): ?><span class="nim-state is-ok">Valid</span><?php else: ?><span class="nim-state is-error">Problem</span><div class="nim-muted text-break"><?= e(mb_strimwidth((string) $check, 0, 200, '…')) ?></div><?php endif; ?></td>
                    <td class="text-end text-nowrap">
                        <?php if ($canServices && !$missing): foreach ($s['active'] ? ['reload' => 'Reload', 'restart' => 'Restart'] : ['start' => 'Start'] as $op => $label): ?>
                            <form class="d-inline" method="post" action="<?= e($this->url('services.control')) ?>" data-nim-busy<?= $op === 'restart' ? ' data-nim-confirm="Restart ' . e($s['name']) . '? Connections in progress are dropped; clients reconnect."' : '' ?>><?= $this->csrfField() ?><input type="hidden" name="unit" value="<?= e($s['unit']) ?>"><input type="hidden" name="op" value="<?= e($op) ?>"><input type="hidden" name="return_to" value="/mail/admin/server"><button class="btn btn-sm btn-outline-secondary"><?= e($label) ?></button></form>
                        <?php endforeach; endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
</div>
<form id="mail-settings" method="post" action="<?= e($this->url('mail.admin.settings')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
</form>
        <?php foreach (['postfix' => 'Postfix (sending and receiving)', 'dovecot' => 'Dovecot (mailboxes and logins)'] as $svc => $title): ?>
            <div class="tab-pane fade" id="mail-<?= e($svc) ?>" role="tabpanel">
                <div class="card mb-3">
                    <div class="card-header"><?= e($title) ?></div>
                    <div class="card-body">
                        <?php foreach ($ordered((array) $status['labels'][$svc]) as $k => $label): $v = $svc === 'postfix' ? $shown($k, (string) ($status['values'][$svc][$k] ?? '')) : (string) ($status['values'][$svc][$k] ?? ''); ?>
                            <div class="row g-2 align-items-center mb-2">
                                <label class="col-sm-7 col-form-label col-form-label-sm" for="ms-<?= e($svc . '-' . $k) ?>"><?= e($label) ?> <span class="nim-muted font-monospace small"><?= e($k) ?></span></label>
                                <div class="col-sm-5">
                                    <?php if ($svc === 'postfix'): ?><input type="hidden" form="mail-settings" name="current[<?= e($k) ?>]" value="<?= e($v) ?>"><?php endif; ?>
                                    <?php if (isset($choices[$k])): ?>
                                        <select class="form-select form-select-sm" form="mail-settings" id="ms-<?= e($svc . '-' . $k) ?>" name="<?= e($svc) ?>[<?= e($k) ?>]"<?= $canManage ? '' : ' disabled' ?>><?php foreach ($choices[$k] as $c): ?><option value="<?= e($c) ?>"<?= $v === $c ? ' selected' : '' ?>><?= e($c === '' ? 'default' : $c) ?></option><?php endforeach; ?></select>
                                    <?php else: ?>
                                        <input class="form-control form-control-sm" form="mail-settings" id="ms-<?= e($svc . '-' . $k) ?>" name="<?= e($svc) ?>[<?= e($k) ?>]" value="<?= e($v) ?>"<?= $svc === 'dovecot' ? ' placeholder="default"' : '' ?><?= $canManage ? '' : ' disabled' ?>>
                                    <?php endif; ?>
                                </div>
                            </div>
                        <?php endforeach; ?>
                        <?php if ($svc === 'postfix'): ?><div class="form-text">Smart host: all outgoing mail is sent through that server (leave empty to deliver directly).</div><?php else: ?><div class="form-text">Empty uses Dovecot's default.</div><?php endif; ?>
                    </div>
                </div>
                <?php if ($canManage): ?><button class="btn btn-primary mb-3" form="mail-settings"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Check and apply</button> <span class="small text-body-secondary">Postfix and Dovecot check the settings first; if either refuses them or does not reload, the previous settings come back.</span><?php endif; ?>
            </div>
        <?php endforeach; ?>
<div class="tab-pane fade" id="mail-log" role="tabpanel">
<div class="card">
    <div class="card-header"><i class="bi bi-journal-text me-1"></i>Latest mail log<?php if (($log['source'] ?? '') !== ''): ?> <span class="small nim-muted fw-normal">(<?= e($log['source']) ?>)</span><?php endif; ?></div>
    <?php $lines = (array) ($log['lines'] ?? []); ?>
    <?php if ($lines === []): ?><div class="card-body small text-body-secondary">No mail log lines.</div><?php else: ?>
        <pre class="nim-pre nim-scroll-400 mb-0 small"><?php foreach ($lines as $l): ?><?= e(str_replace('T', ' ', substr((string) $l['time'], 0, 19)) . ' ' . $l['ident'] . ': ' . $l['message']) . "\n" ?><?php endforeach; ?></pre>
    <?php endif; ?>
</div>
</div>
</div>
