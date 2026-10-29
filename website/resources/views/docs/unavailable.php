<?php
/** @var \NimVorSite\Core\View $v */
/** @var string $repo */
$v->layout('layouts/site');
$v->set('title', 'Documentation');
?>
<?= $v->partial('partials/page-head', ['title' => 'Documentation', 'intro' => 'The documentation is being loaded.', 'crumbs' => [['Documentation', '']]]) ?>
<section class="section"><div class="narrow prose">
    <p>The site could not fetch the documentation from GitHub just now. Try again in a minute. Until then you can read the same pages in the <a href="<?= e($repo) ?>/tree/main/docs" rel="noopener">repository</a>.</p>
</div></section>
