<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $status */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>
<?php $tr = (array) ($status['traceroute'] ?? []); $on = !empty($tr['enabled']); $progs = (array) ($tr['programs'] ?? []); ?>

<div class="wcp-panel mb-3">
    <p class="mb-2">Traceroute displays the packet routing statistics from the server to another network host. It can be used to map the network’s topology and subsequently be used as a tool to focus a hacking attack.</p>
    <div class="d-flex flex-wrap align-items-center gap-2">
        <span class="fw-medium">Traceroute Status:</span>
        <?= $progs === [] ? '<span class="wcp-state is-off">Not installed</span>' : ($on ? '<span class="wcp-state is-warn">Enabled</span>' : '<span class="wcp-state is-ok">Disabled</span>') ?>
        <span class="small text-body-secondary">Disabled means only root can run it (permissions 700).</span>
    </div>
</div>
<div class="wcp-panel mb-3">
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Binary</th><th>Permissions</th></tr></thead>
            <tbody>
            <?php foreach ($progs as $p): $p = (array) $p; ?>
                <tr><td class="font-monospace"><?= e((string) $p['path']) ?></td><td class="font-monospace"><?= e((string) $p['mode']) ?></td></tr>
            <?php endforeach; ?>
            <?php if ($progs === []): ?><tr><td colspan="2" class="text-center py-4 wcp-muted">Traceroute is not installed.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
<?php if ($canManage && $progs !== []): ?>
<form class="wcp-panel" method="post" action="<?= e($this->url('security.traceroute.save')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?><input type="hidden" name="enabled" value="<?= $on ? '0' : '1' ?>">
    <button class="btn btn-sm <?= $on ? 'btn-primary' : 'btn-outline-secondary' ?>"><?= $on ? 'Disable' : 'Enable' ?></button>
</form>
<?php endif; ?>
