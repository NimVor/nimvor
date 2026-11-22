<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $ticket */
$v->layout('layouts/site');
$v->set('title', 'Ticket ' . $ticket['ref']);
$v->set('description', 'Your NimVor support ticket.');
$v->set('noindex', '1');
?>
<?= $v->partial('partials/page-head', ['title' => $ticket['subject'], 'intro' => 'Your private ticket page: keep its address, it is the key to this conversation.', 'crumbs' => [['Support', '/support'], ['Ticket ' . $ticket['ref'], '']]]) ?>
<section class="section">
    <div class="container ticket-page">
        <?= $v->partial('partials/ticket-thread', get_defined_vars()) ?>
    </div>
</section>
