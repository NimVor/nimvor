<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Something went wrong');
?>
<section class="error-page"><div><div class="code">500</div><h1>Something went wrong</h1><p class="lead">The site had a problem. It has been logged. Please try again in a moment.</p><p><a class="btn btn-primary" href="/">Back to the home page</a></p></div></section>
