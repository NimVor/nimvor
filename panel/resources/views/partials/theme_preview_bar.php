<?php
/** @var \NimVor\View\View $this */
$theme = $this->theme();
?>
<?php if ($theme['preview']): ?>
<div class="nim-preview-bar" role="status">
    <span class="me-auto"><i class="bi bi-eye me-1" aria-hidden="true"></i>Previewing the <strong><?= e($theme['label']) ?></strong> theme. Nothing is saved yet.</span>
    <form method="post" action="<?= e($this->url('appearance.save')) ?>" data-nim-noajax><?= $this->csrfField() ?><input type="hidden" name="theme" value="<?= e($theme['key']) ?>"><button class="btn btn-sm btn-primary" type="submit">Keep this theme</button></form>
    <form method="post" action="<?= e($this->url('appearance.preview.cancel')) ?>" data-nim-noajax><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary" type="submit">Go back</button></form>
</div>
<?php endif; ?>
