<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed>|null $state */
$canManage = $this->can(\NimVor\Security\Permissions::SERVER_MANAGE);
$dis = $canManage ? '' : ' disabled';
$ago = static function (int $t): string {
    if ($t <= 0) {
        return 'never';
    }
    $s = max(0, time() - $t);
    return $s < 3600 ? (int) ($s / 60) . ' min ago' : ($s < 172800 ? (int) ($s / 3600) . ' h ago' : (int) ($s / 86400) . ' days ago');
};
$trustModal = function (?int $n, ?array $t) use ($dis): string {
    $key = $n === null ? 'new' : (string) $n;
    ob_start(); ?>
    <div class="modal fade" id="trusted-<?= e($key) ?>" tabindex="-1" aria-labelledby="trusted-title-<?= e($key) ?>" aria-hidden="true">
        <div class="modal-dialog">
            <form class="modal-content" method="post" action="<?= e($this->url('mail.greylist.trusted')) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <?php if ($n !== null): ?><input type="hidden" name="index" value="<?= $n ?>"><?php endif; ?>
                <div class="modal-header"><h2 class="modal-title fs-5" id="trusted-title-<?= e($key) ?>"><?= $t === null ? 'Add trusted host' : 'Edit trusted host' ?></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
                <div class="modal-body">
                    <label class="form-label" for="tr-range-<?= e($key) ?>">IP address or range</label>
                    <input class="form-control font-monospace mb-1" id="tr-range-<?= e($key) ?>" name="range" required maxlength="50" value="<?= e((string) ($t['range'] ?? '')) ?>" placeholder="192.0.2.10 or 192.0.2.0/24 or 2001:db8::/48"<?= $dis ?>>
                    <div class="form-text mb-3">Mail from these addresses is never deferred.</div>
                    <label class="form-label" for="tr-comment-<?= e($key) ?>">Comment</label>
                    <input class="form-control" id="tr-comment-<?= e($key) ?>" name="comment" maxlength="200" value="<?= e((string) ($t['comment'] ?? '')) ?>" placeholder="e.g. office mail server"<?= $dis ?>>
                </div>
                <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary"<?= $dis ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save</button></div>
            </form>
        </div>
    </div>
    <?php return (string) ob_get_clean();
};
?>
<?= $this->partial('mail/admin/_tabs', get_defined_vars()) ?>
<?php if ($state === null) { return; } ?>
<?php
$c = (array) $state['config'];
$svc = (array) $state['service'];
$trusted = (array) ($c['trusted'] ?? []);
?>
<?php if (empty($state['configured'])): ?>
    <div class="alert alert-info">Set up email first (Email → Set up email); greylisting then protects its mailboxes.</div>
<?php endif; ?>
<div class="nim-panel mb-3">
    <div class="nim-statusbar">
        <span class="small"><span class="nim-muted">Greylisting</span> <?= !empty($c['enabled']) ? '<span class="nim-state is-ok">On</span>' : '<span class="nim-state is-off">Off</span>' ?></span>
        <span class="small"><span class="nim-muted">Policy service</span> <?= !empty($svc['active']) ? '<span class="nim-state is-ok">Running</span>' : '<span class="nim-state is-off">Stopped</span>' ?></span>
        <span class="small"><span class="nim-muted">Postfix check</span> <?= !empty($svc['postfix']) ? '<span class="nim-state is-ok">Active</span>' : '<span class="nim-state is-off">Not active</span>' ?></span>
        <?php if (!empty($c['enabled']) && (empty($svc['active']) || empty($svc['postfix']))): ?><span class="small text-warning-emphasis"><i class="bi bi-exclamation-triangle me-1"></i>On but not in effect: save the settings again to restart it.</span><?php endif; ?>
    </div>
</div>

<div class="nim-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#greylist-settings" aria-controls="greylist-settings" aria-selected="true"><i class="bi bi-hourglass-split me-1"></i>Settings</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#trusted" aria-controls="trusted" aria-selected="false"><i class="bi bi-shield-check me-1"></i>Trusted hosts <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($trusted) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#providers" aria-controls="providers" aria-selected="false"><i class="bi bi-buildings me-1"></i>Common mail providers</button>
</div>
<div class="tab-content">
<div class="tab-pane fade show active" id="greylist-settings" role="tabpanel">
<form class="card" method="post" action="<?= e($this->url('mail.greylist.save')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <div class="card-header"><i class="bi bi-hourglass-split me-1"></i>Settings</div>
    <div class="card-body">
        <div class="form-check form-switch mb-3"><input type="hidden" name="enabled" value="0"><input class="form-check-input" type="checkbox" role="switch" id="gl-on" name="enabled" value="1"<?= !empty($c['enabled']) ? ' checked' : '' ?><?= $dis ?>><label class="form-check-label fw-medium" for="gl-on">Enable greylisting</label></div>
        <div class="row g-2 mb-2 align-items-end">
            <div class="col-sm-4"><label class="form-label small" for="gl-deferral">Initial deferral period (minutes)</label><input class="form-control form-control-sm" type="number" min="1" max="1440" id="gl-deferral" name="deferral" value="<?= (int) $c['deferral'] ?>" required<?= $dis ?>></div>
            <div class="col-sm-4"><label class="form-label small" for="gl-resend">Resend acceptance period (minutes)</label><input class="form-control form-control-sm" type="number" min="1" max="10080" id="gl-resend" name="resend" value="<?= (int) $c['resend'] ?>" required<?= $dis ?>></div>
            <div class="col-sm-4"><label class="form-label small" for="gl-expire">Record expiration time (minutes)</label><input class="form-control form-control-sm" type="number" min="60" max="525600" id="gl-expire" name="expire" value="<?= (int) $c['expire'] ?>" required<?= $dis ?>></div>
        </div>
        <div class="form-text mb-3">The first message from a new sender address, recipient and sending server is refused temporarily; a retry after the deferral period and within the acceptance period is accepted, and the combination is then remembered for the record expiration time (renewed with each message).</div>
        <div class="form-check mb-2"><input type="hidden" name="spf_bypass" value="0"><input class="form-check-input" type="checkbox" id="gl-spf" name="spf_bypass" value="1"<?= !empty($c['spf_bypass']) ? ' checked' : '' ?><?= $dis ?>><label class="form-check-label" for="gl-spf">Bypass greylisting for hosts with valid SPF records</label><div class="form-text mt-0">Mail from a server that the sender domain's SPF record authorizes (result “pass”) is not deferred.</div></div>
        <div class="form-check mb-3"><input type="hidden" name="auto_trust_new" value="0"><input class="form-check-input" type="checkbox" id="gl-autotrust" name="auto_trust_new" value="1"<?= !empty($c['auto_trust_new']) ? ' checked' : '' ?><?= $dis ?>><label class="form-check-label" for="gl-autotrust">Automatically trust newly added mail providers</label><div class="form-text mt-0">Providers added to NimVor's list in an update start out trusted.</div></div>
        <?php if ($canManage): ?><button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save settings</button><?php endif; ?>
    </div>
</form>
</div>
<div class="tab-pane fade" id="trusted" role="tabpanel">
<div class="card">
    <div class="card-header nim-toolbar">
        <h2 class="nim-toolbar-title"><i class="bi bi-shield-check me-1"></i>Trusted hosts <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($trusted) ?></span></h2>
        <?php if ($canManage): ?><button class="btn btn-sm btn-outline-primary" type="button" data-bs-toggle="modal" data-bs-target="#trusted-new"><i class="bi bi-plus-lg me-1"></i>Add</button><?php endif; ?>
    </div>
    <?php if ($trusted === []): ?>
        <div class="nim-empty-state"><i class="bi bi-shield-check" aria-hidden="true"></i><div class="fw-medium">No trusted hosts</div><div class="small">Add servers or ranges whose mail must never be deferred.</div></div>
    <?php else: ?>
        <div class="nim-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Address or range</th><th>Comment</th><th></th></tr></thead>
                <tbody>
                <?php foreach ($trusted as $n => $t): ?>
                    <tr>
                        <td class="font-monospace"><?= e((string) $t['range']) ?></td>
                        <td class="small"><?= e((string) ($t['comment'] ?? '')) ?></td>
                        <td class="text-end text-nowrap">
                            <?php if ($canManage): ?>
                                <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#trusted-<?= (int) $n ?>" aria-label="Edit <?= e((string) $t['range']) ?>"><i class="bi bi-pencil"></i></button>
                                <form class="d-inline" method="post" action="<?= e($this->url('mail.greylist.trusted.delete', ['n' => (int) $n])) ?>" data-nim-confirm="Remove the trusted host <?= e((string) $t['range']) ?>? Its mail is greylisted again."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" aria-label="Delete <?= e((string) $t['range']) ?>"><i class="bi bi-trash"></i></button></form>
                            <?php endif; ?>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>
</div>
<div class="tab-pane fade" id="providers" role="tabpanel">
<form class="card" method="post" action="<?= e($this->url('mail.greylist.providers')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <div class="card-header nim-toolbar">
        <h2 class="nim-toolbar-title"><i class="bi bi-buildings me-1"></i>Common mail providers</h2>
        <?php if ($canManage): ?><button class="btn btn-sm btn-outline-secondary" type="submit" formaction="<?= e($this->url('mail.greylist.providers.update')) ?>" title="Read the address ranges of trusted providers from their SPF records now"><i class="bi bi-arrow-repeat me-1"></i>Update ranges now</button><?php endif; ?>
    </div>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle mb-0">
            <thead><tr><th>Provider</th><th>SPF record</th><th>Address ranges</th><th class="text-center">Trust</th><th class="text-center">Auto update</th></tr></thead>
            <tbody>
            <?php foreach ((array) $state['providers'] as $p): $st = (array) ($c['providers'][$p['id']] ?? []); $n = count((array) ($st['ranges'] ?? [])); ?>
                <tr>
                    <td class="fw-medium"><?= e((string) $p['name']) ?></td>
                    <td class="small font-monospace"><?= e(implode(', ', (array) $p['spf'])) ?></td>
                    <td class="small"><?= $n > 0 ? $n . ' · updated ' . e($ago((int) ($st['updated'] ?? 0))) : '<span class="nim-muted">not read yet</span>' ?><?php if (($st['error'] ?? '') !== ''): ?><div class="text-danger-emphasis text-break"><?= e(mb_strimwidth((string) $st['error'], 0, 160, '…')) ?></div><?php endif; ?></td>
                    <td class="text-center"><input class="form-check-input" type="checkbox" name="trust[]" value="<?= e((string) $p['id']) ?>" aria-label="Trust <?= e((string) $p['name']) ?>"<?= !empty($st['trust']) ? ' checked' : '' ?><?= $dis ?>></td>
                    <td class="text-center"><input class="form-check-input" type="checkbox" name="auto_update[]" value="<?= e((string) $p['id']) ?>" aria-label="Update the ranges of <?= e((string) $p['name']) ?> automatically"<?= !empty($st['auto_update']) ? ' checked' : '' ?><?= $dis ?>></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <div class="card-body border-top">
        <?php if ($canManage): ?><button class="btn btn-primary btn-sm"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save providers</button><?php endif; ?>
        <span class="small text-body-secondary ms-1">Mail from a trusted provider's addresses is never deferred. Ranges come from the provider's SPF record; with auto update they are read again once a day while greylisting is on.</span>
    </div>
</form>
</div>
</div>
<?php if ($canManage): ?>
    <?= $trustModal(null, null) ?>
    <?php foreach ($trusted as $n => $t): ?><?= $trustModal((int) $n, (array) $t) ?><?php endforeach; ?>
<?php endif; ?>
