<?php
/** @var \NimVor\View\View $this */
/** @var string $content */
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$menu = $nt['menu'];
// The tool the page belongs to: its icon and category colour mark the page head.
$tool = null;
if ($menu !== null) {
    foreach ($menu['items'] as $i) {
        if ($i['key'] === $menu['active']) {
            $tool = $i;
        }
    }
}
$cat = $tool !== null && $tool['key'] !== 'overview' ? ThemeNav::category($tool['key']) : 'home';
$who = !empty($user) ? ($user['display_name'] !== '' ? $user['display_name'] : $user['username']) : '';
?>
<?= $this->partial('themes/components/head', ['bodyClass' => 'nt-classic']) ?>
<a class="nt-skip" href="#nt-main">Skip to the page</a>
<header class="ncl-top">
    <div class="ncl-wrap ncl-top-in">
        <a class="nt-brand" href="<?= e($this->url('websites')) ?>">
            <img src="<?= e($this->asset('assets/img/logo-dark.png')) ?>" alt="" width="30" height="30">
            <span><?= e($panel_name ?? 'NimVor') ?></span>
        </a>
        <a class="ncl-home" href="<?= e($this->url('websites')) ?>" aria-label="Home"><i class="bi bi-house-door" aria-hidden="true"></i><span>Home</span></a>
        <div class="ncl-find"><?= $this->partial('themes/components/finder', ['entries' => $nt['entries'], 'placeholder' => 'Search tools']) ?><kbd class="ncl-kbd" aria-hidden="true">/</kbd></div>
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
    <div class="ncl-pagehead ncl-cat-<?= e($cat) ?>">
        <?php if ($tool !== null && $tool['key'] !== 'overview'): ?><span class="ncl-pagehead-icon" aria-hidden="true"><i class="bi bi-<?= e($tool['icon']) ?>"></i></span><?php endif; ?>
        <?= $this->partial('themes/components/page_head') ?>
    </div>
    <?= $this->partial('partials/flash') ?>
    <?= $content ?>
</main>
<footer class="ncl-wrap nt-footer">NimVor <?= e($app_version ?? '') ?> · <a href="<?= e($this->url('appearance')) ?>">Appearance</a></footer>
<?= $this->partial('themes/components/foot') ?>
