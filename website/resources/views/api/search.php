<?php
/** @var \NimVorSite\Core\View $v */
/** @var string $q */
/** @var list<array{tag: array<string, mixed>, op: array<string, mixed>}> $hits */
/** @var list<array<string, mixed>> $tags */
$v->layout('layouts/site');
$v->set('title', 'Search the API reference');
$v->set('description', 'Search the NimVor panel API reference.');
?>
<div class="container doc-wrap doc-wrap-2">
    <aside class="doc-side"><?= $v->partial('partials/api-sidebar', ['tags' => $tags, 'current' => $current, 'q' => $q]) ?></aside>
    <article class="doc">
        <div class="crumbs"><a href="/">Home</a> / <a href="/api-reference">API reference</a> / Search</div>
        <div class="prose doc-body">
            <h1>Search</h1>
            <?php if (mb_strlen($q) < 2): ?>
                <p>Type at least two characters: a path, a method, a word from a description or a permission such as <code>websites.manage</code>.</p>
            <?php elseif ($hits === []): ?>
                <p>No endpoint matches “<?= e($q) ?>”.</p>
            <?php else: ?>
                <p><?= count($hits) ?> endpoint<?= count($hits) === 1 ? '' : 's' ?> match “<?= e($q) ?>”.</p>
                <ul class="doc-results">
                    <?php foreach ($hits as $h): ?>
                        <li><a href="/api-reference/<?= e($h['tag']['slug']) ?>#op-<?= e((string) $h['op']['operationId']) ?>"><span class="api-m m-<?= e(strtolower((string) $h['op']['method'])) ?>"><?= e((string) $h['op']['method']) ?></span> <code><?= e((string) $h['op']['path']) ?></code> <span class="muted"><?= e((string) ($h['op']['summary'] ?? '')) ?></span></a></li>
                    <?php endforeach; ?>
                </ul>
            <?php endif; ?>
        </div>
    </article>
</div>
