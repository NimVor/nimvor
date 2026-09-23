<?php /** @var \WhaCP\View\View $this */ ?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'logs']) ?>
<?= $this->partial('logs/_viewer', get_defined_vars()) ?>
