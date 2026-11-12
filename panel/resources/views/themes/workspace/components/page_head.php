<?php
/** @var \NimVor\View\View $this */
/** Workspace: a title bar with the actions at the right and a hairline under it, like a section header. */
$title = $this->section('title') !== '' ? $this->section('title') : e($panel_name ?? 'NimVor');
?>
<div class="nt-pagehead nws-head">
    <div class="nt-pagehead-title">
        <h1 class="h3 mb-1"><?= $title ?></h1>
        <?php if ($this->section('subtitle') !== ''): ?><div class="nim-subtitle"><?= $this->section('subtitle') ?></div><?php endif; ?>
    </div>
    <?php if (trim($this->section('actions')) !== ''): ?><div class="nim-page-actions nt-pagehead-actions"><?= $this->section('actions') ?></div><?php endif; ?>
</div>
