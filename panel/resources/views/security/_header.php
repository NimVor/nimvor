<?php
/** @var \WhaCP\View\View $this */
/** @var string $tab */
/** @var array<string, mixed>|null $pending */
$tabs = [['index', 'Overview', 'security'], ['firewall', 'Firewall', 'security.firewall'], ['ssh', 'SSH', 'security.ssh'], ['fail2ban', 'Brute-force protection', 'security.fail2ban'], ['malware', 'Malware scan', 'security.malware']];
if ($this->serverScope()) {
    $tabs[] = ['shell', 'Shell access', 'security.shell'];
}
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE);
?>
<?php $this->start(); ?>Security<?php $this->end('title'); ?>
<?php $this->start(); ?>Firewall, SSH access, brute-force protection and malware scans<?php $this->end('subtitle'); ?>
<nav class="wcp-pagetabs" aria-label="Security">
    <?php foreach ($tabs as [$key, $label, $route]): ?>
        <a class="<?= $tab === $key ? 'active' : '' ?>" href="<?= e($this->url($route)) ?>"<?= $tab === $key ? ' aria-current="page"' : '' ?>><?= e($label) ?></a>
    <?php endforeach; ?>
</nav>
<?php if ($pending !== null): ?>
    <div class="alert alert-warning d-flex flex-wrap align-items-center gap-2" role="alert">
        <i class="bi bi-hourglass-split"></i>
        <div class="me-auto"><strong>Unconfirmed change:</strong> <?= e($pending['summary'] ?? '') ?>.
            It is undone automatically in <strong data-wcp-countdown="<?= e($pending['deadline'] ?? '') ?>">2:00</strong> unless you confirm that you can still connect (open a new SSH session to test).</div>
        <?php if ($canManage): ?>
            <form method="post" action="<?= e($this->url('security.confirm')) ?>"><?= $this->csrfField() ?><input type="hidden" name="id" value="<?= e($pending['id']) ?>"><input type="hidden" name="return_to" value="<?= e($current_path ?? '/security') ?>"><button class="btn btn-sm btn-success">Keep change</button></form>
            <form method="post" action="<?= e($this->url('security.undo')) ?>"><?= $this->csrfField() ?><input type="hidden" name="id" value="<?= e($pending['id']) ?>"><input type="hidden" name="return_to" value="<?= e($current_path ?? '/security') ?>"><button class="btn btn-sm btn-outline-secondary">Undo now</button></form>
        <?php endif; ?>
    </div>
<?php endif; ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
