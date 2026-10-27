<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $state */
$canManage = $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE);
$dis = $canManage ? '' : ' disabled';
?>
<?= $this->partial('mail/admin/_tabs', get_defined_vars()) ?>
<?php if ($state === null) { return; } ?>
<?php
$c = (array) $state['config'];
$svc = (array) $state['service'];
$accounts = array_map('strval', (array) ($state['accounts'] ?? []));
$limits = (array) ($c['accounts'] ?? []);
$usage = (array) ($state['usage'] ?? []);
$used = [];
foreach ((array) ($usage['accounts'] ?? []) as $u) {
    $used[(string) ($u['name'] ?? '')] = $u;
}
$events = array_reverse((array) ($usage['events'] ?? []));
?>
<?php if (empty($state['configured'])): ?>
    <div class="alert alert-info">Set up email first (Email → Set up email); the limits apply at this server's mail server.</div>
<?php endif; ?>
<div class="wcp-panel mb-3">
    <div class="wcp-statusbar">
        <span class="small"><span class="wcp-muted">Sending limits</span> <?= !empty($c['enabled']) ? '<span class="wcp-state is-ok">On</span>' : '<span class="wcp-state is-off">Off</span>' ?></span>
        <span class="small"><span class="wcp-muted">Limit service</span> <?= !empty($svc['active']) ? '<span class="wcp-state is-ok">Running</span>' : '<span class="wcp-state is-off">Stopped</span>' ?></span>
        <span class="small"><span class="wcp-muted">Postfix check</span> <?= !empty($svc['postfix']) ? '<span class="wcp-state is-ok">Active</span>' : '<span class="wcp-state is-off">Not active</span>' ?></span>
        <?php if (!empty($c['enabled']) && (empty($svc['active']) || empty($svc['postfix']))): ?><span class="small text-warning-emphasis"><i class="bi bi-exclamation-triangle me-1"></i>On but not in effect: save the settings again to restart it.</span><?php endif; ?>
    </div>
</div>

<form method="post" action="<?= e($this->url('mail.limits.save')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?>
    <div class="row g-3">
        <div class="col-xl-5">
            <div class="card h-100">
                <div class="card-header"><i class="bi bi-speedometer2 me-1"></i>Settings</div>
                <div class="card-body">
                    <div class="form-check form-switch mb-3"><input type="hidden" name="enabled" value="0"><input class="form-check-input" type="checkbox" role="switch" id="ml-on" name="enabled" value="1"<?= !empty($c['enabled']) ? ' checked' : '' ?><?= $dis ?>><label class="form-check-label fw-medium" for="ml-on">Limit the mail websites send</label></div>
                    <label class="form-label small" for="ml-per">Recipients per website account and hour</label>
                    <input class="form-control form-control-sm mb-1" style="max-width:12rem" type="number" min="0" max="<?= \WhaCP\Services\MailRateService::MAX ?>" id="ml-per" name="per_hour" value="<?= (int) ($c['per_hour'] ?? 0) ?>" required<?= $dis ?>>
                    <div class="form-text mb-3">0: no limit (the use is still counted). Each recipient counts; past the limit, the account's mail is refused temporarily until the next hour, and administrators and the website's owner are notified.</div>
                    <div class="small wcp-muted mb-3">Covers mail a website sends with PHP's <code>mail()</code>, from cron jobs and deploy hooks, or over SMTP to this server without signing in. While the limits are on, website accounts cannot use Postfix's own <code>sendmail</code> directly (they use NimVor's, which counts). Mailboxes sending with a password are not limited here.</div>
                    <?php if ($canManage): ?><button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save settings</button><?php endif; ?>
                </div>
            </div>
        </div>
        <div class="col-xl-7">
            <div class="card h-100">
                <div class="card-header wcp-toolbar">
                    <h2 class="wcp-toolbar-title"><i class="bi bi-people me-1"></i>Website accounts <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($accounts) ?></span></h2>
                    <?php if ($canManage && !empty($c['enabled'])): ?><button class="btn btn-sm btn-outline-secondary" type="submit" form="ml-reset-all"><i class="bi bi-arrow-counterclockwise me-1"></i>Reset all this hour</button><?php endif; ?>
                </div>
                <?php if ($accounts === []): ?>
                    <div class="card-body wcp-muted small">No website accounts on this server.</div>
                <?php else: ?>
                <div class="table-responsive">
                    <table class="table table-sm align-middle mb-0">
                        <thead><tr><th scope="col">Account</th><th scope="col">This hour</th><th scope="col">Own limit</th><th scope="col"><span class="visually-hidden">Actions</span></th></tr></thead>
                        <tbody>
                        <?php foreach ($accounts as $a): $u = $used[$a] ?? null; ?>
                            <tr>
                                <td class="font-monospace"><?= e($a) ?></td>
                                <td><?= (int) ($u['sent'] ?? 0) ?><?php if (!empty($u['blocked'])): ?> <span class="badge text-bg-danger">Limit reached</span><?php endif; ?></td>
                                <td><input class="form-control form-control-sm" style="max-width:9rem" type="number" min="0" max="<?= \WhaCP\Services\MailRateService::MAX ?>" name="accounts[<?= e($a) ?>]" value="<?= isset($limits[$a]) ? (int) $limits[$a] : '' ?>" placeholder="default" aria-label="Own limit for <?= e($a) ?>"<?= $dis ?>></td>
                                <td class="text-end"><?php if ($canManage && !empty($u['blocked'])): ?><button class="btn btn-sm btn-outline-secondary" type="submit" form="ml-reset" name="account" value="<?= e($a) ?>">Let send again</button><?php endif; ?></td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
                <div class="card-footer small wcp-muted">Empty: the default limit. 0: no limit for that account.</div>
                <?php endif; ?>
            </div>
        </div>
    </div>
</form>
<form id="ml-reset" method="post" action="<?= e($this->url('mail.limits.reset')) ?>" data-wcp-busy><?= $this->csrfField() ?></form>
<form id="ml-reset-all" method="post" action="<?= e($this->url('mail.limits.reset')) ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="account" value=""></form>

<?php if ($events !== []): ?>
<div class="card mt-3">
    <div class="card-header"><i class="bi bi-exclamation-octagon me-1"></i>Limits reached</div>
    <div class="table-responsive">
        <table class="table table-sm mb-0">
            <thead><tr><th scope="col">When</th><th scope="col">Account</th><th scope="col">Limit</th></tr></thead>
            <tbody>
            <?php foreach (array_slice($events, 0, 50) as $ev): ?>
                <tr><td><?= e(date('Y-m-d H:i', (int) $ev['time'])) ?></td><td class="font-monospace"><?= e((string) ($ev['name'] !== '' ? $ev['name'] : 'UID ' . (int) $ev['uid'])) ?></td><td><?= (int) $ev['limit'] ?></td></tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
<?php endif; ?>
