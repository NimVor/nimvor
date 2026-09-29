<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $status */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>
<?php $f = (array) ($status['forkbomb'] ?? []); $on = !empty($f['enabled']); ?>

<div class="wcp-panel mb-3">
    <p class="mb-2">Fork Bomb Protection denies users who have terminal access (SSH/Telnet) the ability to use all of the resources on the server. To prevent server crashes, do not allow unlimited resource allocation.</p>
    <p class="mb-2">We recommend that you enable this protection for servers that provide terminal access.</p>
    <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
        <span class="fw-medium">Status:</span>
        <?= $on ? '<span class="wcp-state is-ok">Enabled</span>' : '<span class="wcp-state is-off">Disabled</span>' ?>
    </div>
    <div class="small text-body-secondary">Accounts (UID 1000 and up) are limited to <?= (int) ($f['soft'] ?? 200) ?> processes (hard limit <?= (int) ($f['hard'] ?? 300) ?>) in their login sessions. Root and system services are not limited. It applies to new logins.</div>
</div>
<?php if ($canManage): ?>
<form class="wcp-panel" method="post" action="<?= e($this->url('security.forkbomb.save')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?><input type="hidden" name="enabled" value="<?= $on ? '0' : '1' ?>">
    <button class="btn btn-sm <?= $on ? 'btn-outline-danger' : 'btn-primary' ?>"><?= $on ? 'Disable Protection' : 'Enable Protection' ?></button>
</form>
<?php endif; ?>
