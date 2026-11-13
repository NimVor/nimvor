<?php
/**
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 */
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'accesslists']) ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php return; endif; ?>
<?= $this->partial('partials/access_list', get_defined_vars() + ['canManage' => true, 'scope' => (string) $site['domain']]) ?>
