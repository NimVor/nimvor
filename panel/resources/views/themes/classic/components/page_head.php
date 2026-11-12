<?php
/** @var \NimVor\View\View $this */
/** Classic: the title and its subtitle, the page's buttons at the right, and a way back to the home page. */
$title = $this->section('title') !== '' ? $this->section('title') : e($panel_name ?? 'NimVor');
$home = $this->isActive('/websites') && $this->shared('site_menu') === null;
?>
<div class="nt-pagehead ncl-head">
    <div class="nt-pagehead-title">
        <h1 class="h4 mb-0"><?= $title ?></h1>
        <?php if ($this->section('subtitle') !== ''): ?><div class="nim-subtitle"><?= $this->section('subtitle') ?></div><?php endif; ?>
    </div>
    <div class="nim-page-actions nt-pagehead-actions">
        <?= $this->section('actions') ?>
        <?php if (!$home): ?><a class="btn btn-sm btn-outline-secondary ncl-gohome" href="<?= e($this->url('websites')) ?>"><i class="bi bi-house-door me-1" aria-hidden="true"></i>Home</a><?php endif; ?>
    </div>
</div>
