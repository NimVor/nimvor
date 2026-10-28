<?php /** @var \NimVor\View\View $this */ ?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'databases']) ?>
<?= $this->partial('databases/_manage', get_defined_vars() + ['returnTo' => '/sites/' . (int) $site['id'] . '/databases']) ?>
