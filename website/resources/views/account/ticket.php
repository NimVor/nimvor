<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $ticket */
$v->layout('layouts/app');
$v->share('area', 'account');
$v->set('title', 'Ticket ' . $ticket['ref']);
?>
<div class="app-top"><div><h1><?= e($ticket['subject']) ?></h1><p class="muted mb-0"><a href="/account/tickets">&larr; All tickets</a></p></div></div>
<?= $v->partial('partials/ticket-thread', get_defined_vars()) ?>
