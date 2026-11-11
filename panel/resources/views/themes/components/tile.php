<?php
/** @var \NimVor\View\View $this */
/** @var array{key: string, label: string, icon: string, url: string} $tool */
/** A tool as a clickable tile; data-nt-tile lets the page's filter box find it. */
?>
<a class="nt-tile" href="<?= e($tool['url']) ?>" data-nt-tile data-name="<?= e(strtolower($tool['label'])) ?>">
    <i class="bi bi-<?= e($tool['icon']) ?>" aria-hidden="true"></i><span><?= e($tool['label']) ?></span>
</a>
