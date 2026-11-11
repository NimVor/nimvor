<?php
/** @var \NimVor\View\View $this */
/** The page's own title, subtitle and buttons (the sections every page fills in). */
$title = $this->section('title') !== '' ? $this->section('title') : e($panel_name ?? 'NimVor');
?>
<div class="nt-pagehead">
    <div class="nt-pagehead-title">
        <h1 class="h4 mb-0"><?= $title ?></h1>
        <?php if ($this->section('subtitle') !== ''): ?><div class="nim-subtitle"><?= $this->section('subtitle') ?></div><?php endif; ?>
    </div>
    <?php if (trim($this->section('actions')) !== ''): ?><div class="nim-page-actions nt-pagehead-actions"><?= $this->section('actions') ?></div><?php endif; ?>
</div>
