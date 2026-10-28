<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Not allowed');
?>
<section class="error-page"><div><div class="code">403</div><h1>Not allowed</h1><p class="lead">You do not have access to this page.</p><p><a class="btn btn-primary" href="/">Back to the home page</a></p></div></section>
