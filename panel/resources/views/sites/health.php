<?php
/**
 * Site → Health: per-website health overview with fix guidance.
 *
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var list<array<string, mixed>> $checks
 */
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'health']) ?>
<p class="small text-body-secondary">The health of <strong><?= e($site['domain']) ?></strong> — whether it is running, backed up and in good shape. Each item below says what to do and who can do it.</p>
<?= $this->partial('sites/_checks', ['site' => $site, 'checks' => $checks]) ?>
