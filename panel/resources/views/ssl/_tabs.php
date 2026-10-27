<?php
/** @var \NimVor\View\View $this */
/** @var string $tab */
$tabs = [['hosts', 'SSL hosts', 'ssl.hosts'], ['autossl', 'AutoSSL', 'ssl.autossl'], ['certificates', 'Certificates', 'ssl.certificates'], ['keys', 'Keys and CSRs', 'ssl.keys'], ['tls', 'TLS configuration', 'ssl.tls'], ['services', 'Services SSL', 'ssl.services']];
?>
<?php $this->start(); ?>SSL/TLS<?php $this->end('title'); ?>
<?php $this->start(); ?>Hosts, certificates, keys and TLS settings of this server<?php $this->end('subtitle'); ?>
<nav class="nim-pagetabs" aria-label="SSL/TLS">
    <?php foreach ($tabs as [$key, $label, $route]): ?>
        <a class="<?= $tab === $key ? 'active' : '' ?>" href="<?= e($this->url($route)) ?>"<?= $tab === $key ? ' aria-current="page"' : '' ?>><?= e($label) ?></a>
    <?php endforeach; ?>
</nav>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
