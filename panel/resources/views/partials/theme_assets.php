<?php
/** @var \NimVor\View\View $this */
// Only the chosen theme's stylesheet (and the accent colours, when one was picked) is loaded.
$theme = $this->theme();
?>
<?php if ($theme['css'] !== null): ?>    <link rel="stylesheet" href="<?= e($this->asset('assets/css/themes/base.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset($theme['css'])) ?>">
<?php endif; ?>
<?php if ($theme['accent'] !== ''): ?>    <link rel="stylesheet" href="<?= e($this->asset('assets/css/themes/accents.css')) ?>">
<?php endif; ?>
