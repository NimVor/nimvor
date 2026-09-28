<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $status */
/** @var array<string, mixed>|null $log */
$canManage = $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE);
$canServices = $this->can(\WhaCP\Security\Permissions::SERVICES_MANAGE);
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
$choices = ['smtpd_helo_required' => ['yes', 'no'], 'disable_vrfy_command' => ['yes', 'no'], 'smtp_tls_security_level' => ['may', 'encrypt', 'dane'], 'inet_protocols' => ['all', 'ipv4', 'ipv6'], 'ssl_min_protocol' => ['', 'TLSv1.2', 'TLSv1.3']];
?>
<?= $this->partial('mail/admin/_tabs', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>
<div class="wcp-panel mb-3">
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Service</th><th>Version</th><th>State</th><th>Configuration</th><th></th></tr></thead>
            <tbody>
            <?php foreach ((array) $status['services'] as $s): $check = ['Postfix' => $status['checks']['postfix'] ?? null, 'Dovecot' => $status['checks']['dovecot'] ?? null][$s['name']] ?? null; ?>
                <tr>
                    <td class="fw-semibold"><?= e($s['name']) ?><div class="small wcp-muted font-monospace"><?= e($s['unit']) ?></div></td>
                    <td class="small"><?= e($s['version'] !== '' ? $s['version'] : '—') ?></td>
                    <td class="small"><?= $s['active'] ? '<span class="wcp-state is-ok">Running</span>' : '<span class="wcp-state is-error">Stopped</span>' ?> <span class="wcp-muted"><?= $s['enabled'] ? 'starts at boot' : 'not started at boot' ?></span></td>
                    <td class="small"><?php if ($check === null): ?><span class="wcp-muted">—</span><?php elseif ($check === ''): ?><span class="wcp-state is-ok">Valid</span><?php else: ?><span class="wcp-state is-error">Problem</span><div class="wcp-muted text-break"><?= e(mb_strimwidth((string) $check, 0, 200, '…')) ?></div><?php endif; ?></td>
                    <td class="text-end text-nowrap">
                        <?php if ($canServices): foreach ($s['active'] ? ['reload' => 'Reload', 'restart' => 'Restart'] : ['start' => 'Start'] as $op => $label): ?>
                            <form class="d-inline" method="post" action="<?= e($this->url('services.control')) ?>" data-wcp-busy<?= $op === 'restart' ? ' data-wcp-confirm="Restart ' . e($s['name']) . '? Connections in progress are dropped; clients reconnect."' : '' ?>><?= $this->csrfField() ?><input type="hidden" name="unit" value="<?= e($s['unit']) ?>"><input type="hidden" name="op" value="<?= e($op) ?>"><input type="hidden" name="return_to" value="/mail/admin/server"><button class="btn btn-sm btn-outline-secondary"><?= e($label) ?></button></form>
                        <?php endforeach; endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>

<form method="post" action="<?= e($this->url('mail.admin.settings')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?>
    <div class="row g-3 mb-3">
        <?php foreach (['postfix' => 'Postfix (sending and receiving)', 'dovecot' => 'Dovecot (mailboxes and logins)'] as $svc => $title): ?>
            <div class="col-xl-6">
                <div class="card h-100">
                    <div class="card-header"><?= e($title) ?></div>
                    <div class="card-body">
                        <?php foreach ($ordered((array) $status['labels'][$svc]) as $k => $label): $v = $svc === 'postfix' ? $shown($k, (string) ($status['values'][$svc][$k] ?? '')) : (string) ($status['values'][$svc][$k] ?? ''); ?>
                            <div class="row g-2 align-items-center mb-2">
                                <label class="col-sm-7 col-form-label col-form-label-sm" for="ms-<?= e($svc . '-' . $k) ?>"><?= e($label) ?> <span class="wcp-muted font-monospace small"><?= e($k) ?></span></label>
                                <div class="col-sm-5">
                                    <?php if ($svc === 'postfix'): ?><input type="hidden" name="current[<?= e($k) ?>]" value="<?= e($v) ?>"><?php endif; ?>
                                    <?php if (isset($choices[$k])): ?>
                                        <select class="form-select form-select-sm" id="ms-<?= e($svc . '-' . $k) ?>" name="<?= e($svc) ?>[<?= e($k) ?>]"<?= $canManage ? '' : ' disabled' ?>><?php foreach ($choices[$k] as $c): ?><option value="<?= e($c) ?>"<?= $v === $c ? ' selected' : '' ?>><?= e($c === '' ? 'default' : $c) ?></option><?php endforeach; ?></select>
                                    <?php else: ?>
                                        <input class="form-control form-control-sm" id="ms-<?= e($svc . '-' . $k) ?>" name="<?= e($svc) ?>[<?= e($k) ?>]" value="<?= e($v) ?>"<?= $svc === 'dovecot' ? ' placeholder="default"' : '' ?><?= $canManage ? '' : ' disabled' ?>>
                                    <?php endif; ?>
                                </div>
                            </div>
                        <?php endforeach; ?>
                        <?php if ($svc === 'postfix'): ?><div class="form-text">Smart host: all outgoing mail is sent through that server (leave empty to deliver directly).</div><?php else: ?><div class="form-text">Empty uses Dovecot's default.</div><?php endif; ?>
                    </div>
                </div>
            </div>
        <?php endforeach; ?>
    </div>
    <?php if ($canManage): ?><button class="btn btn-primary mb-3"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Check and apply</button> <span class="small text-body-secondary">Postfix and Dovecot check the settings first; if either refuses them or does not reload, the previous settings come back.</span><?php endif; ?>
</form>

<div class="card">
    <div class="card-header"><i class="bi bi-journal-text me-1"></i>Latest mail log<?php if (($log['source'] ?? '') !== ''): ?> <span class="small wcp-muted fw-normal">(<?= e($log['source']) ?>)</span><?php endif; ?></div>
    <?php $lines = (array) ($log['lines'] ?? []); ?>
    <?php if ($lines === []): ?><div class="card-body small text-body-secondary">No mail log lines.</div><?php else: ?>
        <pre class="wcp-pre wcp-scroll-400 mb-0 small"><?php foreach ($lines as $l): ?><?= e(str_replace('T', ' ', substr((string) $l['time'], 0, 19)) . ' ' . $l['ident'] . ': ' . $l['message']) . "\n" ?><?php endforeach; ?></pre>
    <?php endif; ?>
</div>
