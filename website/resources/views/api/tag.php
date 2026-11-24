<?php
/** @var \NimVorSite\Core\View $v */
/** @var array{name: string, slug: string, description: string, operations: list<array<string, mixed>>} $tag */
/** @var list<array<string, mixed>> $tags */
/** @var \NimVorSite\Support\ApiSchema $schema */
/** @var \Closure(string): string $markdown */
$v->layout('layouts/site');
$v->set('title', $tag['name'] . ' · API reference');
$v->set('description', 'NimVor panel API: ' . $tag['name'] . ' endpoints. ' . $tag['description']);
?>
<div class="container doc-wrap doc-wrap-2">
    <aside class="doc-side"><?= $v->partial('partials/api-sidebar', ['tags' => $tags, 'current' => $current, 'q' => $q]) ?></aside>
    <article class="doc">
        <div class="crumbs"><a href="/">Home</a> / <a href="/api-reference">API reference</a> / <?= e($tag['name']) ?></div>
        <div class="prose doc-body">
            <h1><?= e($tag['name']) ?></h1>
            <?php if ($tag['description'] !== ''): ?><div class="lead"><?= $markdown($tag['description']) ?></div><?php endif; ?>
            <?php foreach ($tag['operations'] as $op): ?>
                <?= $v->partial('partials/api-operation', ['op' => $op, 'schema' => $schema, 'markdown' => $markdown]) ?>
            <?php endforeach; ?>
        </div>
    </article>
</div>
