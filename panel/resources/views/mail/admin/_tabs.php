<?php
/** @var \WhaCP\View\View $this */
/** @var string $tab */
$titles = ['server' => 'Mail server', 'queue' => 'Mail queue', 'reports' => 'Delivery reports', 'stats' => 'Statistics', 'greylisting' => 'Greylisting', 'greylist-report' => 'Greylisting report', 'sending-limits' => 'Mail sending limits', 'tools' => 'Mail tools'];
?>
<?php $this->start(); ?><?= e($titles[$tab] ?? 'Email') ?><?php $this->end('title'); ?>
<?php $this->start(); ?>Email<?php $this->end('subtitle'); ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
