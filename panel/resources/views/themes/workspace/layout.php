<?php
/** @var \NimVor\View\View $this */
/** @var string $content */
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$menu = $nt['menu'];
$activeSite = $menu !== null ? (int) $menu['site']['id'] : 0;
$tabKeys = ['overview', 'files', 'databases', 'mail', 'dns', 'ssl', 'backups', 'php'];
$tabs = [];
$more = [];
if ($menu !== null) {
    foreach ($menu['items'] as $i) {
        if (in_array($i['key'], $tabKeys, true)) {
            $tabs[] = $i;
        } else {
            $more[] = $i;
        }
    }
}
$moreActive = false;
foreach ($more as $m) {
    $moreActive = $moreActive || ($menu !== null && $m['key'] === $menu['active']);
}
$tint = static fn (string $domain): int => abs(crc32($domain)) % 8;
?>
<?= $this->partial('themes/components/head', ['bodyClass' => 'nt-workspace']) ?>
<a class="nt-skip" href="#nt-main">Skip to the page</a>
<div class="nws-shell">
    <aside class="nws-side" id="nws-side" aria-label="Main navigation">
        <a class="nws-brand" href="<?= e($this->url('websites')) ?>">
            <img src="<?= e($this->asset('assets/img/logo-dark.png')) ?>" alt="" width="30" height="30">
            <span><?= e($panel_name ?? 'NimVor') ?></span>
        </a>
        <nav class="nws-nav">
            <a class="nws-link<?= $this->isActive('/websites') && $menu === null ? ' active' : '' ?>" href="<?= e($this->url('websites')) ?>"<?= $this->isActive('/websites') && $menu === null ? ' aria-current="page"' : '' ?>><i class="bi bi-grid-1x2-fill" aria-hidden="true"></i>Websites &amp; domains</a>
            <div class="nws-label">Your websites</div>
            <?php foreach ($nt['sites'] as $s): $isActive = (int) $s['id'] === $activeSite; ?>
                <a class="nws-link nws-site<?= $isActive ? ' active' : '' ?>" href="<?= e($this->url('sites.show', ['id' => (int) $s['id']])) ?>"<?= $isActive ? ' aria-current="location"' : '' ?>>
                    <span class="nws-mini nws-tint-<?= $tint((string) $s['domain']) ?>" aria-hidden="true"><?= e(strtoupper(substr((string) $s['domain'], 0, 1))) ?></span><span class="text-truncate"><?= e($s['domain']) ?></span>
                    <span class="nws-dot <?= !empty($s['account_suspended']) || $s['status'] === 'error' ? 'is-bad' : ($s['enabled'] ? 'is-ok' : 'is-off') ?>" aria-hidden="true"></span>
                </a>
                <?php if ($isActive && $menu !== null): ?>
                    <div class="nws-sub">
                        <?php foreach (ThemeNav::grouped($menu['items']) as $g): ?>
                            <div class="nws-cat"><?= e($g['label']) ?></div>
                            <?php foreach ($g['items'] as $i): $on = $i['key'] === $menu['active']; ?>
                                <a class="nws-sublink<?= $on ? ' active' : '' ?>" href="<?= e($i['url']) ?>"<?= $on ? ' aria-current="page"' : '' ?>><i class="bi bi-<?= e($i['icon']) ?>" aria-hidden="true"></i><span><?= e($i['label']) ?></span></a>
                            <?php endforeach; ?>
                        <?php endforeach; ?>
                    </div>
                <?php endif; ?>
            <?php endforeach; ?>
            <?php if ($nt['accountPages'] !== []): ?>
                <div class="nws-label">Account</div>
                <?php foreach ($nt['accountPages'] as $p): ?>
                    <a class="nws-link<?= $this->isActive(parse_url($p['url'], PHP_URL_PATH) ?: '/__') ? ' active' : '' ?>" href="<?= e($p['url']) ?>"><i class="bi bi-<?= e($p['icon']) ?>" aria-hidden="true"></i><span><?= e($p['label']) ?></span></a>
                <?php endforeach; ?>
            <?php endif; ?>
            <div class="nws-label">You</div>
            <a class="nws-link<?= $this->isActive('/appearance') ? ' active' : '' ?>" href="<?= e($this->url('appearance')) ?>"><i class="bi bi-palette" aria-hidden="true"></i>Appearance</a>
            <a class="nws-link<?= $this->isActive('/profile') ? ' active' : '' ?>" href="<?= e($this->url('profile')) ?>"><i class="bi bi-person" aria-hidden="true"></i>Profile</a>
        </nav>
    </aside>
    <div class="nws-backdrop" data-nt-nav="toggle"></div>
    <div class="nws-main">
        <header class="nws-top">
            <button class="btn nt-iconbtn nt-menu-btn" type="button" data-nt-nav="toggle" aria-label="Show or hide the menu" aria-controls="nws-side" aria-expanded="false"><i class="bi bi-list" aria-hidden="true"></i></button>
            <div class="nws-find"><?= $this->partial('themes/components/finder', ['entries' => $nt['entries'], 'placeholder' => 'Search tools and pages']) ?></div>
            <?= $this->partial('themes/components/breadcrumbs') ?>
            <?= $this->partial('themes/components/account_actions') ?>
        </header>
        <main class="nws-content" id="nt-main">
            <?= $this->partial('partials/theme_preview_bar') ?>
            <?php if ($menu !== null): $site = $menu['site']; ?>
                <section class="nws-sitehead" aria-label="Website">
                    <span class="nws-thumb nws-tint-<?= $tint((string) $site['domain']) ?>" aria-hidden="true"><i></i><i></i><i></i><b><?= e(strtoupper(substr((string) $site['domain'], 0, 1))) ?></b></span>
                    <div class="nws-sitehead-main">
                        <h1 class="h4 mb-1"><?= e($site['domain']) ?> <?= $this->partial('themes/components/site_status', ['site' => $site]) ?></h1>
                        <div class="nws-chips">
                            <?php if ($site['kind'] === 'php'): ?><span class="nws-chip"><i class="bi bi-filetype-php" aria-hidden="true"></i>PHP <?= e($site['php_version']) ?></span><?php endif; ?>
                            <span class="nws-chip"><i class="bi bi-<?= $site['https'] ? 'lock-fill' : 'unlock' ?>" aria-hidden="true"></i>HTTPS <?= $site['https'] ? 'on' : 'off' ?></span>
                            <span class="nws-chip"><i class="bi bi-person" aria-hidden="true"></i><?= e($site['system_user']) ?></span>
                        </div>
                    </div>
                </section>
                <nav class="nws-tabs" aria-label="Website tools">
                    <?php foreach ($tabs as $t): $on = $t['key'] === $menu['active']; ?>
                        <a class="nws-tab<?= $on ? ' active' : '' ?>" href="<?= e($t['url']) ?>"<?= $on ? ' aria-current="page"' : '' ?>><i class="bi bi-<?= e($t['icon']) ?>" aria-hidden="true"></i><?= e($t['label']) ?></a>
                    <?php endforeach; ?>
                    <?php if ($more !== []): ?>
                        <div class="dropdown">
                            <button class="nws-tab dropdown-toggle<?= $moreActive ? ' active' : '' ?>" type="button" data-bs-toggle="dropdown" aria-expanded="false"><i class="bi bi-three-dots" aria-hidden="true"></i>More tools</button>
                            <ul class="dropdown-menu nws-more">
                                <?php foreach ($more as $m): $on = $m['key'] === $menu['active']; ?>
                                    <li><a class="dropdown-item<?= $on ? ' active' : '' ?>" href="<?= e($m['url']) ?>"><i class="bi bi-<?= e($m['icon']) ?> me-2" aria-hidden="true"></i><?= e($m['label']) ?></a></li>
                                <?php endforeach; ?>
                            </ul>
                        </div>
                    <?php endif; ?>
                </nav>
                <?php if (trim($this->section('actions')) !== ''): ?><div class="nim-page-actions nws-actions"><?= $this->section('actions') ?></div><?php endif; ?>
            <?php else: ?>
                <?= $this->partial('themes/components/page_head') ?>
            <?php endif; ?>
            <?= $this->partial('partials/flash') ?>
            <?= $content ?>
            <footer class="nt-footer">NimVor <?= e($app_version ?? '') ?></footer>
        </main>
    </div>
</div>
<?= $this->partial('themes/components/foot') ?>
