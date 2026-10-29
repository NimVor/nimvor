<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $page */
/** @var list<array{level: int, id: string, text: string}> $toc */
/** @var array<string, list<array<string, mixed>>> $groups */
/** @var ?array<string, mixed> $prev */
/** @var ?array<string, mixed> $next */
/** @var string $slug */
/** @var string $editUrl */
/** @var ?string $syncedAt */
$v->layout('layouts/site');
$v->set('title', (string) $page['title']);
$v->set('description', 'NimVor documentation: ' . $page['title'] . '.');
?>
<div class="container doc-wrap">
    <aside class="doc-side"><?= $v->partial('partials/docs-sidebar', ['groups' => $groups, 'slug' => $slug]) ?></aside>
    <article class="doc">
        <div class="crumbs"><a href="/">Home</a> / <a href="/docs">Documentation</a><?= $slug === 'index' ? '' : ' / ' . e($page['label']) ?></div>
        <div class="prose doc-body" data-doc-body><?= $page['html'] ?></div>
        <div class="doc-foot">
            <a href="<?= e($editUrl) ?>" rel="noopener">Edit this page on GitHub</a>
            <?php if ($syncedAt !== null): ?><span class="muted">Synced from the repository <?= e(gmdate('j M Y H:i', (int) strtotime($syncedAt))) ?> UTC</span><?php endif; ?>
        </div>
        <nav class="doc-pager" aria-label="Previous and next page">
            <?php if ($prev !== null): ?><a href="<?= e($prev['url']) ?>"><small>Previous</small><?= e($prev['label']) ?></a><?php else: ?><span></span><?php endif; ?>
            <?php if ($next !== null): ?><a class="right" href="<?= e($next['url']) ?>"><small>Next</small><?= e($next['label']) ?></a><?php endif; ?>
        </nav>
    </article>
    <?php if (count($toc) > 1): ?>
        <aside class="doc-toc" aria-label="On this page">
            <h2>On this page</h2>
            <ul>
                <?php foreach ($toc as $t): ?>
                    <li class="l<?= (int) $t['level'] ?>"><a href="#<?= e($t['id']) ?>"><?= e($t['text']) ?></a></li>
                <?php endforeach; ?>
            </ul>
        </aside>
    <?php endif; ?>
</div>
