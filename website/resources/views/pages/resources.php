<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Resources');
$items = [
    ['/resources/documentation', 'book', 'Documentation', 'Install, administer and extend NimVor, with the commands you will actually use.'],
    ['/resources/blog', 'pen', 'Blog', 'News, releases and how-tos from the project.'],
    ['/resources/community', 'git', 'Community & source', 'The code on GitHub, how to report a problem and how to contribute.'],
    ['/resources/build-services', 'tool', 'Build services', 'Paid help: migrations, installs, reviews and custom features.'],
    ['/resources/sponsors', 'heart', 'Sponsors', 'The people and companies that keep NimVor free.'],
    ['/resources/about', 'info', 'About us', 'What NimVor is, why it exists and the rules it follows.'],
];
?>
<?= $v->partial('partials/page-head', ['title' => 'Resources', 'intro' => 'Everything for installing, running and contributing to NimVor.', 'crumbs' => [['Resources', '']]]) ?>
<section class="section">
    <div class="container grid grid-3">
        <?php foreach ($items as [$href, $icon, $title, $text]): ?>
            <a class="card card-link" href="<?= e($href) ?>"><div class="card-icon"><?= $v->icon($icon) ?></div><h3><?= e($title) ?></h3><p class="muted"><?= e($text) ?></p></a>
        <?php endforeach; ?>
    </div>
</section>
