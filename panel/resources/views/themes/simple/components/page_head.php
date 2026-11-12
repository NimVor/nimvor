<?php
/** @var \NimVor\View\View $this */
/** Simple: a large title with the subtitle as a lead sentence; the buttons wrap under it on a phone. */
$title = $this->section('title') !== '' ? $this->section('title') : e($panel_name ?? 'NimVor');
?>
<div class="nt-pagehead nsi-head">
    <div class="nt-pagehead-title">
        <h1 class="mb-1"><?= $title ?></h1>
        <?php if ($this->section('subtitle') !== ''): ?><p class="nim-subtitle nsi-lead mb-0"><?= $this->section('subtitle') ?></p><?php endif; ?>
    </div>
    <?php if (trim($this->section('actions')) !== ''): ?><div class="nim-page-actions nt-pagehead-actions"><?= $this->section('actions') ?></div><?php endif; ?>
</div>
