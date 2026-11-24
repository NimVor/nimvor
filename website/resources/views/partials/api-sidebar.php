<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array{name: string, slug: string, description: string, operations: list<array<string, mixed>>}> $tags */
/** @var string $current */
/** @var string $q */
?>
<details class="doc-menu" open>
    <summary><?= $v->icon('menu', 18) ?> API menu</summary>
    <form class="doc-search" method="get" action="/api-reference/search" role="search">
        <label class="sr-only" for="api-q">Search the API reference</label>
        <input class="input" id="api-q" name="q" type="search" placeholder="Search endpoints" minlength="2" maxlength="80" value="<?= e($q ?? '') ?>">
    </form>
    <nav aria-label="API reference">
        <ul>
            <li><a href="/api-reference"<?= $current === '' ? ' aria-current="page"' : '' ?>>Introduction</a></li>
        </ul>
        <h2 class="doc-group">Endpoints</h2>
        <ul>
            <?php foreach ($tags as $t): ?>
                <li>
                    <a href="/api-reference/<?= e($t['slug']) ?>"<?= $current === $t['slug'] ? ' aria-current="page"' : '' ?>><?= e($t['name']) ?> <span class="api-count"><?= count($t['operations']) ?></span></a>
                    <?php if ($current === $t['slug']): ?>
                        <ul class="api-ops">
                            <?php foreach ($t['operations'] as $op): ?>
                                <li><a href="#op-<?= e((string) $op['operationId']) ?>"><span class="api-m m-<?= e(strtolower((string) $op['method'])) ?>"><?= e((string) $op['method']) ?></span> <?= e((string) ($op['summary'] ?? $op['path'])) ?></a></li>
                            <?php endforeach; ?>
                        </ul>
                    <?php endif; ?>
                </li>
            <?php endforeach; ?>
        </ul>
    </nav>
</details>
