<?php
/**
 * The tab buttons of a widget with tabs (the panes are in the widget; assets/js/nimvor.js switches them).
 *
 * @var \NimVor\View\View $this
 * @var array<string, string> $tabs name => label; the first one is open
 * @var string $label what the tabs are for (read by screen readers)
 */
$first = (string) array_key_first($tabs);
?>
<div class="nim-wtabs" role="tablist" aria-label="<?= e($label ?? 'Sections') ?>">
    <?php foreach ($tabs as $name => $text): ?><button type="button" class="nim-wtab<?= (string) $name === $first ? ' active' : '' ?>" role="tab" aria-selected="<?= (string) $name === $first ? 'true' : 'false' ?>" data-nim-tab="<?= e((string) $name) ?>"><?= e($text) ?></button><?php endforeach; ?>
</div>
