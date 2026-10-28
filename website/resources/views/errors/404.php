<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Page not found');
?>
<section class="error-page"><div><div class="code">404</div><h1>Page not found</h1><p class="lead">The page you asked for does not exist or has moved.</p><p><a class="btn btn-primary" href="/">Back to the home page</a></p></div></section>
