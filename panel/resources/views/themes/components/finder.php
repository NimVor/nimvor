<?php
/** @var \NimVor\View\View $this */
/** @var list<array{label: string, icon: string, url: string}> $entries everything the viewer can open */
/** @var string $placeholder */
$placeholder ??= 'Find a tool…';
?>
<div class="nt-finder" data-nt-finder>
    <label class="visually-hidden" for="nt-finder-input"><?= e($placeholder) ?></label>
    <i class="bi bi-search" aria-hidden="true"></i>
    <input class="form-control form-control-sm" id="nt-finder-input" type="search" placeholder="<?= e($placeholder) ?>" autocomplete="off" data-nt-finder-input aria-controls="nt-finder-list" aria-expanded="false">
    <ul class="nt-finder-list" id="nt-finder-list" role="listbox" hidden>
        <?php foreach ($entries as $en): ?>
            <li role="option" data-nt-finder-item data-name="<?= e(strtolower($en['label'])) ?>"><a href="<?= e($en['url']) ?>"><i class="bi bi-<?= e($en['icon']) ?>" aria-hidden="true"></i><?= e($en['label']) ?></a></li>
        <?php endforeach; ?>
    </ul>
</div>
