<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $posts */
$v->layout('layouts/site');
$v->set('title', 'Blog');
$v->set('description', 'News, releases and how-tos from the NimVor project.');
?>
<?= $v->partial('partials/page-head', ['title' => 'Blog', 'intro' => 'News, releases and how-tos from the project.', 'crumbs' => [['Resources', '/resources'], ['Blog', '']]]) ?>
<section class="section">
    <div class="container">
        <?php if ($posts === []): ?>
            <div class="card empty">No posts yet. Check back soon.</div>
        <?php else: ?>
            <div class="grid grid-3">
                <?php foreach ($posts as $p): ?>
                    <article class="card post-card">
                        <time datetime="<?= e(substr((string) $p['published_at'], 0, 10)) ?>"><?= e(substr((string) $p['published_at'], 0, 10)) ?></time>
                        <h3><a href="/resources/blog/<?= e($p['slug']) ?>"><?= e($p['title']) ?></a></h3>
                        <p class="muted"><?= e($p['excerpt']) ?></p>
                        <a href="/resources/blog/<?= e($p['slug']) ?>">Read more <?= $v->icon('arrow', 14) ?></a>
                    </article>
                <?php endforeach; ?>
            </div>
        <?php endif; ?>
    </div>
</section>
