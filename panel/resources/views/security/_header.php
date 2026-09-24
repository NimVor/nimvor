<?php
/** @var \WhaCP\View\View $this */
/** @var string $tab */
/** @var array<string, mixed>|null $pending */
$tabs = [['index', 'Overview', 'security', 'shield-check'], ['firewall', 'Firewall', 'security.firewall', 'bricks'], ['ssh', 'SSH', 'security.ssh', 'terminal'], ['fail2ban', 'Fail2ban', 'security.fail2ban', 'slash-circle']];
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE);
?>
<?php $this->start(); ?>Security<?php $this->end('title'); ?>
<?php $this->start(); ?>Firewall, SSH access and brute-force protection<?php $this->end('subtitle'); ?>
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
<ul class="nav nav-tabs wcp-tabs">
    <?php foreach ($tabs as [$key, $label, $route, $icon]): ?>
        <li class="nav-item"><a class="nav-link<?= $tab === $key ? ' active' : '' ?>" href="<?= e($this->url($route)) ?>"><i class="bi bi-<?= e($icon) ?> me-1"></i><?= e($label) ?></a></li>
    <?php endforeach; ?>
</ul>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
