<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $status */
/** @var list<string> $accounts */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>
<?php $c = (array) ($status['compilers'] ?? []); $restricted = !empty($c['restricted']); $members = array_map('strval', (array) ($c['members'] ?? [])); ?>

<div class="wcp-panel mb-3">
    <p class="mb-2">Many common exploits require a working C compiler on the system. This tweak allows you to deny compiler access to unprivileged users; you can also choose to allow some users to use the compilers while they remain disabled by default.</p>
    <div class="d-flex flex-wrap align-items-center gap-2">
        <span class="fw-medium">Compiler access for unprivileged users:</span>
        <?php if ($restricted): ?><span class="wcp-state is-ok">Disabled</span><?php else: ?><span class="wcp-state is-warn">Enabled</span><?php endif; ?>
        <span class="small text-body-secondary">Restricted programs can only be run by root and the <code><?= e((string) ($c['group'] ?? 'whacp-compiler')) ?></code> group.</span>
    </div>
</div>

<?php if ($canManage): ?>
<form class="wcp-panel mb-3" method="post" action="<?= e($this->url('security.compilers.save')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?>
    <div class="form-check form-switch mb-3">
        <input type="hidden" name="restricted" value="0">
        <input class="form-check-input" type="checkbox" role="switch" id="cc-restricted" name="restricted" value="1"<?= $restricted ? ' checked' : '' ?>>
        <label class="form-check-label fw-medium" for="cc-restricted">Deny compiler access to unprivileged users</label>
    </div>
    <div class="fw-medium mb-1">Users allowed to use the compilers</div>
    <div class="small text-body-secondary mb-2">Applies while compiler access is denied. The panel's own build account (ModSecurity source builds) keeps access.</div>
    <?php if ($accounts === []): ?>
        <div class="small wcp-muted mb-2">No website accounts yet.</div>
    <?php else: ?>
        <div class="row row-cols-1 row-cols-md-3 g-1 mb-3">
            <?php foreach ($accounts as $i => $a): ?>
                <div class="col"><div class="form-check">
                    <input class="form-check-input" type="checkbox" name="users[]" value="<?= e($a) ?>" id="cc-u<?= (int) $i ?>"<?= in_array($a, $members, true) ? ' checked' : '' ?>>
                    <label class="form-check-label font-monospace" for="cc-u<?= (int) $i ?>"><?= e($a) ?></label>
                </div></div>
            <?php endforeach; ?>
        </div>
    <?php endif; ?>
    <button class="btn btn-primary btn-sm">Save</button>
</form>
<?php endif; ?>

<div class="wcp-panel">
    <div class="fw-semibold mb-2">Compilers on this server</div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Binary</th><th>Owner</th><th>Permissions</th></tr></thead>
            <tbody>
            <?php foreach ((array) ($c['programs'] ?? []) as $p): $p = (array) $p; ?>
                <tr><td class="font-monospace"><?= e((string) $p['path']) ?></td><td class="font-monospace"><?= e((string) $p['owner']) ?></td><td class="font-monospace"><?= e((string) $p['mode']) ?></td></tr>
            <?php endforeach; ?>
            <?php if (($c['programs'] ?? []) === []): ?><tr><td colspan="3" class="text-center py-4 wcp-muted">No compilers are installed.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
