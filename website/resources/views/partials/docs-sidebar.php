<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, list<array<string, mixed>>> $groups */
/** @var string $slug */
?>
<details class="doc-menu" open>
    <summary><?= $v->icon('menu', 18) ?> Documentation menu</summary>
    <form class="doc-search" method="get" action="/docs/search" role="search">
        <label class="sr-only" for="doc-q">Search the documentation</label>
        <input class="input" id="doc-q" name="q" type="search" placeholder="Search the docs" minlength="2" maxlength="80" value="<?= e($q ?? '') ?>">
    </form>
    <nav aria-label="Documentation">
        <?php foreach ($groups as $group => $items): ?>
            <h2 class="doc-group"><?= e($group) ?></h2>
            <ul>
                <?php foreach ($items as $i): ?>
                    <li><a class="<?= str_contains((string) $i['slug'], '/') ? 'sub' : '' ?>" href="<?= e($i['url']) ?>"<?= $i['slug'] === $slug ? ' aria-current="page"' : '' ?>><?= e($i['label']) ?></a></li>
                <?php endforeach; ?>
            </ul>
        <?php endforeach; ?>
    </nav>
</details>
