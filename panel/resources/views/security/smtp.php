<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $status */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE) && ($user['role_name'] ?? '') === 'admin';
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($status === null) { return; } ?>
<?php $s = (array) ($status['smtp'] ?? []); $on = !empty($s['enabled']); ?>

<div class="wcp-panel mb-3">
    <p class="mb-2">This feature prevents users from bypassing the mail server to send mail, a common practice used by spammers. It will allow only the MTA, mailman, and root to connect to remote SMTP servers.</p>
    <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
        <span class="fw-medium">Status:</span>
        <?php if ($on && !empty($s['active'])): ?><span class="wcp-state is-ok">Enabled</span>
        <?php elseif ($on): ?><span class="wcp-state is-warn">Enabled, rules not loaded</span>
        <?php else: ?><span class="wcp-state is-off">Disabled</span><?php endif; ?>
    </div>
    <div class="small text-body-secondary">Outgoing connections to ports <?= e(implode(', ', array_map('strval', (array) ($s['ports'] ?? [25, 465, 587])))) ?> are allowed for: <span class="font-monospace"><?= e(implode(', ', array_map('strval', (array) ($s['allowed'] ?? [])))) ?></span>. Everyone else gets the connection refused. Local delivery (localhost) is not affected.</div>
    <?php if (empty($s['available'])): ?>
        <div class="wcp-infobar mt-2"><i class="bi bi-exclamation-triangle-fill" aria-hidden="true"></i><span>nftables is not installed on this server; install the <code>nftables</code> package to use SMTP restrictions.</span></div>
    <?php endif; ?>
</div>
<?php if ($canManage): ?>
<form class="wcp-panel" method="post" action="<?= e($this->url('security.smtp.save')) ?>" data-wcp-busy>
    <?= $this->csrfField() ?><input type="hidden" name="enabled" value="<?= $on ? '0' : '1' ?>">
    <button class="btn btn-sm <?= $on ? 'btn-outline-danger' : 'btn-primary' ?>"<?= empty($s['available']) && !$on ? ' disabled' : '' ?>><?= $on ? 'Disable' : 'Enable' ?></button>
</form>
<?php endif; ?>
