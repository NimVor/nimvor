<?php
/** @var \NimVor\View\View $this */
/** @var string $tab */
$tabs = [['addresses', 'Addresses and usage', 'ips'], ['ranges', 'IPv6 ranges', 'ips.ranges'], ['migrate', 'Migration wizard', 'ips.migrate']];
?>
<?php $this->start(); ?>IP addresses<?php $this->end('title'); ?>
<?php $this->start(); ?>The addresses websites listen on<?php $this->end('subtitle'); ?>
<nav class="nim-pagetabs" aria-label="IP addresses">
    <?php foreach ($tabs as [$key, $label, $route]): ?>
        <a class="<?= $tab === $key ? 'active' : '' ?>" href="<?= e($this->url($route)) ?>"<?= $tab === $key ? ' aria-current="page"' : '' ?>><?= e($label) ?></a>
    <?php endforeach; ?>
</nav>
<?php if (($agentError ?? null) !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
