<?php
/** @var \NimVor\View\View $this */
/** @var string $content */
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$menu = $nt['menu'];
$site = $nt['site'];
$grouped = ThemeNav::grouped($nt['items']);
$who = !empty($user) ? ($user['display_name'] !== '' ? $user['display_name'] : $user['username']) : '';
$activeKey = $menu['active'] ?? '';
?>
<?= $this->partial('themes/components/head', ['bodyClass' => 'nt-compact']) ?>
<a class="nt-skip" href="#nt-main">Skip to the page</a>
<header class="nco-top">
    <button class="btn nt-iconbtn nt-menu-btn" type="button" data-nt-nav="toggle" aria-label="Show or hide the menu" aria-controls="nco-menu" aria-expanded="false"><i class="bi bi-list" aria-hidden="true"></i></button>
    <a class="nt-brand" href="<?= e($this->url('websites')) ?>"><img src="<?= e($this->asset('assets/img/logo-dark.png')) ?>" alt="" width="24" height="24"><span><?= e($panel_name ?? 'NimVor') ?></span></a>
    <span class="nco-acct d-none d-md-inline">Account: <strong><?= e($who) ?></strong><?= $nt['account']['plan'] !== '' ? ' · ' . e($nt['account']['plan']) : '' ?></span>
    <div class="nco-find"><?= $this->partial('themes/components/finder', ['entries' => $nt['entries'], 'placeholder' => 'Find a tool']) ?></div>
    <?= $this->partial('themes/components/account_actions') ?>
</header>
<?php if ($nt['account']['usage'] !== []): ?>
<div class="nco-limits" aria-label="Account resources">
    <?= $this->partial('themes/components/usage', ['rows' => $nt['account']['usage'], 'variant' => 'inline']) ?>
</div>
<?php endif; ?>
<div class="nco-shell">
    <aside class="nco-menu" id="nco-menu" aria-label="Main navigation">
        <a class="nco-item<?= $this->isActive('/websites') && $menu === null ? ' active' : '' ?>" href="<?= e($this->url('websites')) ?>"><i class="bi bi-house" aria-hidden="true"></i>Home</a>
        <?php if ($site !== null): ?>
            <div class="nco-site">
                <label class="visually-hidden" for="nco-site-select">Website</label>
                <select class="form-select form-select-sm" id="nco-site-select" data-nt-jump>
                    <?php foreach ($nt['sites'] as $s): ?><option value="<?= e($this->url('sites.show', ['id' => (int) $s['id']])) ?>"<?= (int) $s['id'] === (int) $site['id'] ? ' selected' : '' ?>><?= e($s['domain']) ?></option><?php endforeach; ?>
                </select>
            </div>
            <a class="nco-item<?= $activeKey === 'overview' ? ' active' : '' ?>" href="<?= e($this->url('sites.show', ['id' => (int) $site['id']])) ?>"><i class="bi bi-speedometer2" aria-hidden="true"></i>Overview</a>
            <?php foreach ($grouped as $cat => $g):
                $open = false;
                foreach ($g['items'] as $i) {
                    $open = $open || $i['key'] === $activeKey;
                }
                ?>
                <button class="nco-cat" type="button" data-nt-fold="#nco-c-<?= e($cat) ?>" aria-expanded="<?= $open ? 'true' : 'false' ?>"><span><i class="bi bi-<?= e($g['icon']) ?>" aria-hidden="true"></i><?= e($g['label']) ?></span><i class="bi bi-chevron-down" aria-hidden="true"></i></button>
                <div class="nco-sub" id="nco-c-<?= e($cat) ?>"<?= $open ? '' : ' hidden' ?>>
                    <?php foreach ($g['items'] as $i): ?><a class="nco-subitem<?= $i['key'] === $activeKey ? ' active' : '' ?>" href="<?= e($i['url']) ?>"<?= $i['key'] === $activeKey ? ' aria-current="page"' : '' ?>><?= e($i['label']) ?></a><?php endforeach; ?>
                </div>
            <?php endforeach; ?>
        <?php endif; ?>
        <button class="nco-cat" type="button" data-nt-fold="#nco-c-account" aria-expanded="false"><span><i class="bi bi-person-gear" aria-hidden="true"></i>Account</span><i class="bi bi-chevron-down" aria-hidden="true"></i></button>
        <div class="nco-sub" id="nco-c-account" hidden>
            <?php foreach ($nt['accountPages'] as $p): ?><a class="nco-subitem" href="<?= e($p['url']) ?>"><?= e($p['label']) ?></a><?php endforeach; ?>
            <a class="nco-subitem" href="<?= e($this->url('appearance')) ?>">Appearance</a>
            <a class="nco-subitem" href="<?= e($this->url('profile')) ?>">Profile</a>
        </div>
    </aside>
    <div class="nws-backdrop nco-backdrop" data-nt-nav="toggle"></div>
    <main class="nco-main" id="nt-main">
        <div class="nco-crumbs"><?= $this->partial('themes/components/breadcrumbs') ?></div>
        <?= $this->partial('partials/theme_preview_bar') ?>
        <?= $this->partial('themes/components/page_head') ?>
        <?= $this->partial('partials/flash') ?>
        <?= $content ?>
        <footer class="nt-footer">NimVor <?= e($app_version ?? '') ?></footer>
    </main>
</div>
<?= $this->partial('themes/components/foot') ?>
