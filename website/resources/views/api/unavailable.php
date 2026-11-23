<?php
/** @var \NimVorSite\Core\View $v */
/** @var string $repo */
$v->layout('layouts/site');
$v->set('title', 'API reference unavailable');
?>
<div class="container api-unavailable">
    <h1>The API reference is not available right now</h1>
    <p>It is read from the project's repository on GitHub and that could not be reached. Try again in a minute, or read the description directly: <a href="<?= e($repo) ?>/blob/main/panel/resources/api/openapi.json" rel="noopener">openapi.json</a>.</p>
</div>
