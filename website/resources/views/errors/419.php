<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Session expired');
?>
<section class="error-page"><div><div class="code">419</div><h1>Session expired</h1><p class="lead">Your form expired. Go back, reload the page and try again.</p><p><a class="btn btn-primary" href="/">Back to the home page</a></p></div></section>
