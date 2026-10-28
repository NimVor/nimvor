<?php /** @var \NimVor\View\View $this */ ?>
<?php $this->start(); ?>Database logs<?php $this->end('title'); ?>
<?php $this->start(); ?>MariaDB / MySQL error log and service journal<?php $this->end('subtitle'); ?>
<?php if ($sources === [] && $agentError === null): ?><div class="alert alert-info small">No database server log was found on this server.</div><?php endif; ?>
<?= $this->partial('logs/_viewer', get_defined_vars()) ?>
