<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Too many requests');
?>
<section class="error-page"><div><div class="code">429</div><h1>Too many requests</h1><p class="lead">You have done that too often. Please wait a while and try again.</p><p><a class="btn btn-primary" href="/">Back to the home page</a></p></div></section>
