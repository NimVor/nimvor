<?php /** @var \NimVor\View\View $this */ ?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'logs']) ?>
<?= $this->partial('logs/_viewer', get_defined_vars()) ?>
