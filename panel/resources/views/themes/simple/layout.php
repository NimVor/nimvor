<?php
/** @var \NimVor\View\View $this */
/** @var string $content */
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$menu = $nt['menu'];
$site = $nt['site'];
$pick = ThemeNav::pick($nt['items'], ['files', 'mail', 'databases']);
$grouped = ThemeNav::grouped($nt['items']);
$overview = $menu === null || $menu['active'] === 'overview';
?>
<?= $this->partial('themes/components/head', ['bodyClass' => 'nt-simple']) ?>
<a class="nt-skip" href="#nt-main">Skip to the page</a>
<header class="nsi-top">
    <div class="nsi-wrap nsi-top-in">
        <a class="nt-brand" href="<?= e($this->url('websites')) ?>">
            <img src="<?= e($this->asset('assets/img/logo-dark.png')) ?>" alt="" width="30" height="30">
            <span><?= e($panel_name ?? 'NimVor') ?></span>
        </a>
        <?php if ($site !== null): ?>
            <div class="dropdown nsi-site">
                <button class="btn btn-sm nsi-site-btn dropdown-toggle" type="button" data-bs-toggle="dropdown" aria-expanded="false"><i class="bi bi-globe2 me-1" aria-hidden="true"></i><span class="text-truncate"><?= e($site['domain']) ?></span></button>
                <ul class="dropdown-menu">
                    <?php foreach ($nt['sites'] as $s): ?>
                        <li><a class="dropdown-item<?= (int) $s['id'] === (int) $site['id'] ? ' active' : '' ?>" href="<?= e($this->url('sites.show', ['id' => (int) $s['id']])) ?>"><?= e($s['domain']) ?></a></li>
                    <?php endforeach; ?>
                    <li><hr class="dropdown-divider"></li>
                    <li><a class="dropdown-item" href="<?= e($this->url('websites')) ?>">All websites</a></li>
                </ul>
            </div>
        <?php endif; ?>
        <nav class="nsi-links d-none d-md-flex" aria-label="Main">
            <a class="nsi-link<?= $this->isActive('/websites') && $menu === null ? ' active' : '' ?>" href="<?= e($this->url('websites')) ?>">Home</a>
            <?php foreach ($pick as $t): ?><a class="nsi-link<?= $menu !== null && $menu['active'] === $t['key'] ? ' active' : '' ?>" href="<?= e($t['url']) ?>"><?= e($t['label']) ?></a><?php endforeach; ?>
            <button class="nsi-link" type="button" data-bs-toggle="offcanvas" data-bs-target="#nsi-tools" aria-controls="nsi-tools">All tools</button>
        </nav>
        <div class="ms-auto"><?= $this->partial('themes/components/account_actions') ?></div>
    </div>
</header>
<main class="nsi-wrap nsi-main" id="nt-main">
    <?= $this->partial('partials/theme_preview_bar') ?>
    <?php if ($menu !== null && !$overview): ?>
        <a class="nsi-back" href="<?= e($this->url('sites.show', ['id' => (int) $menu['site']['id']])) ?>"><i class="bi bi-arrow-left" aria-hidden="true"></i><?= e($menu['site']['domain']) ?></a>
    <?php endif; ?>
    <?= $this->partial('themes/components/page_head') ?>
    <?= $this->partial('partials/flash') ?>
    <?= $content ?>
    <footer class="nt-footer">NimVor <?= e($app_version ?? '') ?></footer>
</main>

<nav class="nsi-tabbar d-md-none" aria-label="Main">
    <a class="<?= $this->isActive('/websites') && $menu === null ? 'active' : '' ?>" href="<?= e($this->url('websites')) ?>"><i class="bi bi-house" aria-hidden="true"></i><span>Home</span></a>
    <?php foreach ($pick as $t): ?><a class="<?= $menu !== null && $menu['active'] === $t['key'] ? 'active' : '' ?>" href="<?= e($t['url']) ?>"><i class="bi bi-<?= e($t['icon']) ?>" aria-hidden="true"></i><span><?= e($t['key'] === 'databases' ? 'Databases' : $t['label']) ?></span></a><?php endforeach; ?>
    <button type="button" data-bs-toggle="offcanvas" data-bs-target="#nsi-tools" aria-controls="nsi-tools"><i class="bi bi-grid" aria-hidden="true"></i><span>More</span></button>
</nav>

<div class="offcanvas offcanvas-end nsi-tools" tabindex="-1" id="nsi-tools" aria-labelledby="nsi-tools-title">
    <div class="offcanvas-header">
        <h2 class="offcanvas-title h5" id="nsi-tools-title"><?= $site !== null ? e($site['domain']) : 'Tools' ?></h2>
        <button type="button" class="btn-close" data-bs-dismiss="offcanvas" aria-label="Close"></button>
    </div>
    <div class="offcanvas-body">
        <?php if ($grouped === [] && $nt['accountPages'] === []): ?><p class="text-body-secondary">No tools yet.</p><?php endif; ?>
        <?php foreach ($grouped as $g): ?>
            <h3 class="nsi-tools-h"><?= e($g['label']) ?></h3>
            <div class="nsi-tools-list">
                <?php foreach ($g['items'] as $i): ?><a href="<?= e($i['url']) ?>"><i class="bi bi-<?= e($i['icon']) ?>" aria-hidden="true"></i><?= e($i['label']) ?></a><?php endforeach; ?>
            </div>
        <?php endforeach; ?>
        <h3 class="nsi-tools-h">Account</h3>
        <div class="nsi-tools-list">
            <?php foreach ($nt['accountPages'] as $p): ?><a href="<?= e($p['url']) ?>"><i class="bi bi-<?= e($p['icon']) ?>" aria-hidden="true"></i><?= e($p['label']) ?></a><?php endforeach; ?>
            <a href="<?= e($this->url('appearance')) ?>"><i class="bi bi-palette" aria-hidden="true"></i>Appearance</a>
            <a href="<?= e($this->url('profile')) ?>"><i class="bi bi-person" aria-hidden="true"></i>Profile</a>
        </div>
    </div>
</div>
<?= $this->partial('themes/components/foot') ?>
