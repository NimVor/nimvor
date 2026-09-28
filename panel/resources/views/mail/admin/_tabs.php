<?php
/** @var \WhaCP\View\View $this */
/** @var string $tab */
$tabs = [['server', 'Mail server', 'mail.admin.server', []], ['queue', 'Mail queue', 'mail.admin.queue', []], ['reports', 'Delivery reports', 'mail.admin.reports', []], ['stats', 'Statistics', 'mail.admin.reports', ['view' => 'stats']], ['greylisting', 'Greylisting', 'mail.greylist', []], ['greylist-report', 'Greylisting report', 'mail.greylist.report', []]];
?>
<?php $this->start(); ?>Email administration<?php $this->end('title'); ?>
<?php $this->start(); ?>Mail server settings, queue, delivery reports and statistics<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('mail')) ?>"><i class="bi bi-envelope me-1"></i>Email</a><?php $this->end('actions'); ?>
<nav class="wcp-pagetabs" aria-label="Email administration">
    <?php foreach ($tabs as [$key, $label, $route, $q]): ?>
        <a class="<?= $tab === $key ? 'active' : '' ?>" href="<?= e($this->url($route, [], $q)) ?>"<?= $tab === $key ? ' aria-current="page"' : '' ?>><?= e($label) ?></a>
    <?php endforeach; ?>
</nav>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
