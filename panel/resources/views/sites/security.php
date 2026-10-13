<?php
/**
 * Site → Security: per-website security overview with fix guidance.
 *
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var list<array<string, mixed>> $checks
 */
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'security']) ?>
<p class="small text-body-secondary">A quick security check of <strong><?= e($site['domain']) ?></strong>. Items marked “You can fix this” you can handle from this panel; the rest are for your hosting provider.</p>
<?= $this->partial('sites/_checks', ['site' => $site, 'checks' => $checks]) ?>
