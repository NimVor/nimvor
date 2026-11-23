<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $tags */
/** @var string $intro */
/** @var string $version */
/** @var int $count */
/** @var list<array{name: string, description: string, example: string}> $errors */
/** @var ?string $syncedAt */
$v->layout('layouts/site');
$v->set('title', 'API reference');
$v->set('description', 'The NimVor panel REST API: authentication, every endpoint with parameters, request and response examples, and errors.');
?>
<div class="container doc-wrap doc-wrap-2">
    <aside class="doc-side"><?= $v->partial('partials/api-sidebar', ['tags' => $tags, 'current' => $current, 'q' => $q]) ?></aside>
    <article class="doc">
        <div class="crumbs"><a href="/">Home</a> / <a href="/docs">Documentation</a> / API reference</div>
        <div class="prose doc-body">
            <h1>NimVor panel API <small class="api-ver">v<?= e($version) ?></small></h1>
            <p class="lead"><?= (int) $count ?> endpoints in <?= count($tags) ?> groups. Everything below is generated from the API's own OpenAPI description, <a href="https://github.com/NimVor/nimvor/blob/main/panel/resources/api/openapi.json" rel="noopener">openapi.json</a>, which every panel also serves at <code>/api/v1/openapi.json</code>.</p>
            <?= $intro ?>
            <h2 id="quick-start">Quick start</h2>
            <pre class="codeblock"><code class="language-bash"># 1. Create a token: Profile → API tokens (shown once)
# 2. Call the API
curl -s -H "Authorization: Bearer nimvor_…" https://panel.example.com/api/v1/websites</code></pre>
            <h2 id="groups">Endpoints</h2>
            <div class="api-cards">
                <?php foreach ($tags as $t): ?>
                    <a class="api-card" href="/api-reference/<?= e($t['slug']) ?>">
                        <strong><?= e($t['name']) ?></strong>
                        <span><?= e($t['description']) ?></span>
                        <small><?= count($t['operations']) ?> endpoint<?= count($t['operations']) === 1 ? '' : 's' ?></small>
                    </a>
                <?php endforeach; ?>
            </div>
            <h2 id="errors">Errors</h2>
            <p>Every endpoint can answer with these. The body is always <code>{"success": false, "error": {"code", "message", …}}</code>.</p>
            <?php foreach ($errors as $e): ?>
                <h3><?= e($e['name']) ?></h3>
                <p><?= e($e['description']) ?></p>
                <?php if ($e['example'] !== ''): ?><pre class="codeblock"><code class="language-json"><?= e($e['example']) ?></code></pre><?php endif; ?>
            <?php endforeach; ?>
        </div>
        <?php if ($syncedAt !== null): ?><div class="doc-foot"><span class="muted">Read from the repository <?= e(gmdate('j M Y H:i', (int) strtotime($syncedAt))) ?> UTC</span></div><?php endif; ?>
    </article>
</div>
