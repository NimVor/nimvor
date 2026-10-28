<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Method not allowed');
?>
<section class="error-page"><div><div class="code">405</div><h1>Method not allowed</h1><p class="lead">That request is not allowed on this page.</p><p><a class="btn btn-primary" href="/">Back to the home page</a></p></div></section>
