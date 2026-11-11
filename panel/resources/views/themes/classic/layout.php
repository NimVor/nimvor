<?php
/** @var \NimVor\View\View $this */
/** @var string $content */
use NimVor\View\ThemeContext;

$nt = ThemeContext::build($this);
?>
<?= $this->partial('themes/components/head', ['bodyClass' => 'nt-classic']) ?>
<a class="nt-skip" href="#nt-main">Skip to the page</a>
<header class="ncl-top">
    <div class="ncl-wrap ncl-top-in">
        <a class="nt-brand" href="<?= e($this->url('websites')) ?>">
            <img src="<?= e($this->asset('assets/img/logo-dark.png')) ?>" alt="" width="28" height="28">
            <span><?= e($panel_name ?? 'NimVor') ?></span>
        </a>
        <div class="ncl-find"><?= $this->partial('themes/components/finder', ['entries' => $nt['entries'], 'placeholder' => 'Find a tool…']) ?></div>
        <?= $this->partial('themes/components/account_actions') ?>
    </div>
</header>
<div class="ncl-bar">
    <div class="ncl-wrap ncl-bar-in">
        <?= $this->partial('themes/components/breadcrumbs') ?>
        <?php if (count($nt['sites']) > 1): ?>
            <label class="ncl-switch"><span class="visually-hidden">Switch website</span>
                <select class="form-select form-select-sm" data-nt-jump aria-label="Switch website">
                    <option value="">Switch website…</option>
                    <?php foreach ($nt['sites'] as $s): ?><option value="<?= e($this->url('sites.show', ['id' => (int) $s['id']])) ?>"><?= e($s['domain']) ?></option><?php endforeach; ?>
                </select>
            </label>
        <?php endif; ?>
    </div>
</div>
<main class="ncl-wrap ncl-main" id="nt-main">
    <?= $this->partial('partials/theme_preview_bar') ?>
    <?= $this->partial('themes/components/page_head') ?>
    <?= $this->partial('partials/flash') ?>
    <?= $content ?>
</main>
<footer class="ncl-wrap nt-footer">NimVor <?= e($app_version ?? '') ?> · <a href="<?= e($this->url('appearance')) ?>">Appearance</a></footer>
<?= $this->partial('themes/components/foot') ?>
