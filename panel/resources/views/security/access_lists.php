<?php
/** @var \NimVor\View\View $this */
$canManage = $this->can(\NimVor\Security\Permissions::SECURITY_MANAGE);
?>
<?php $this->start(); ?>Website Access Lists<?php $this->end('title'); ?>
<?php $this->start(); ?>Addresses, browsers and URLs refused or always served on every website<?php $this->end('subtitle'); ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>
<div class="alert alert-info small" role="alert">This server-wide list applies to every website NimVor manages. A website's own list (Websites → the website → IP, browser &amp; URL lists) is added to it, and an allow entry in either wins. Websites created later receive the list when it is next saved.</div>
<?= $this->partial('partials/access_list', get_defined_vars() + ['canManage' => $canManage, 'scope' => 'every website']) ?>
