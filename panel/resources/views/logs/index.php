<?php /** @var \WhaCP\View\View $this */ ?>
<?php $this->start(); ?><?= e($category ?? 'Logs') ?><?php $this->end('title'); ?>
<?php $this->start(); ?>Web server, PHP, database, system and WhaCP logs<?php $this->end('subtitle'); ?>
<?= $this->partial('logs/_viewer', get_defined_vars()) ?>
