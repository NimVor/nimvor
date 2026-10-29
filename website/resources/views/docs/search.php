<?php
/** @var \NimVorSite\Core\View $v */
/** @var string $q */
/** @var list<array<string, mixed>> $results */
/** @var array<string, list<array<string, mixed>>> $groups */
$v->layout('layouts/site');
$v->set('title', 'Search the documentation');
?>
<div class="container doc-wrap doc-wrap-2">
    <aside class="doc-side"><?= $v->partial('partials/docs-sidebar', ['groups' => $groups, 'slug' => '', 'q' => $q]) ?></aside>
    <article class="doc">
        <div class="crumbs"><a href="/">Home</a> / <a href="/docs">Documentation</a> / Search</div>
        <h1 class="doc-h1">Search</h1>
        <?php if (mb_strlen($q) < 2): ?>
            <p class="muted">Type at least two characters.</p>
        <?php elseif ($results === []): ?>
            <p>Nothing in the documentation matches <strong><?= e($q) ?></strong>. Try a shorter word, or <a href="/docs">browse the pages</a>.</p>
        <?php else: ?>
            <p class="muted"><?= count($results) ?> page<?= count($results) === 1 ? '' : 's' ?> match <strong><?= e($q) ?></strong>.</p>
            <ul class="doc-results">
                <?php foreach ($results as $r): ?>
                    <li><a href="<?= e(\NimVorSite\Services\DocsCatalog::url((string) $r['slug'])) ?>"><strong><?= e($r['title']) ?></strong><span class="badge"><?= e($r['group']) ?></span></a><p class="muted mb-0"><?= e($r['snippet']) ?></p></li>
                <?php endforeach; ?>
            </ul>
        <?php endif; ?>
    </article>
</div>
