<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $post */
/** @var string $bodyHtml */
$v->layout('layouts/site');
$v->set('title', (string) $post['title']);
$v->set('description', (string) $post['excerpt']);
?>
<?= $v->partial('partials/page-head', ['title' => (string) $post['title'], 'intro' => '', 'crumbs' => [['Resources', '/resources'], ['Blog', '/resources/blog'], ['Post', '']]]) ?>
<section class="section">
    <div class="container">
        <article class="prose">
            <p class="post-meta"><time datetime="<?= e(substr((string) $post['published_at'], 0, 10)) ?>"><?= e(substr((string) $post['published_at'], 0, 10)) ?></time><?= !empty($post['author']) ? ' · ' . e($post['author']) : '' ?></p>
            <?= $bodyHtml ?>
            <p class="mt-4"><a href="/resources/blog">&larr; All posts</a></p>
        </article>
    </div>
</section>
