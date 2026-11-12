<?php
/** @var \NimVor\View\View $this */
/** Compact: a single dense line: the title, its subtitle beside it, and small buttons at the right. */
$title = $this->section('title') !== '' ? $this->section('title') : e($panel_name ?? 'NimVor');
?>
<div class="nt-pagehead nco-head">
    <h1 class="h6 mb-0"><?= $title ?></h1>
    <?php if ($this->section('subtitle') !== ''): ?><span class="nim-subtitle"><?= $this->section('subtitle') ?></span><?php endif; ?>
    <?php if (trim($this->section('actions')) !== ''): ?><div class="nim-page-actions nt-pagehead-actions ms-auto"><?= $this->section('actions') ?></div><?php endif; ?>
</div>
