<?php
/** @var \NimVor\View\View $this */

use NimVor\View\Navigation;

$groups = Navigation::visible($this);
?>
<?php $siteMenu = $this->shared('site_menu'); ?>
<nav class="nim-sidebar" id="nim-sidebar" aria-label="Main navigation">
    <div class="nim-sidebar-head">
    <a class="nim-brand" href="<?= e($this->url('dashboard')) ?>">
        <img src="<?= e($this->asset('assets/img/logo-dark.png')) ?>" alt="" width="32" height="32">
        <span><?= e($panel_name ?? 'NimVor') ?></span>
    </a>
    <?php $st = isset($agent_status) && is_callable($agent_status) ? $agent_status() : null; ?>
    <?php if ($st === null): ?>
        <div class="nim-mode-pill is-offline" title="The NimVor agent is not reachable."><i class="bi bi-plug"></i> Agent offline</div>
    <?php elseif (empty($st['write_enabled'])): ?>
        <div class="nim-mode-pill" title="The agent is in read-only mode: NimVor observes the server but does not change it."><i class="bi bi-eye"></i> Read-only mode</div>
    <?php endif; ?>
    <button class="nim-search-trigger nim-sidebar-search" type="button" data-bs-toggle="modal" data-bs-target="#nim-palette" aria-label="Search tools (Ctrl+K)">
        <i class="bi bi-search" aria-hidden="true"></i><span>Search tools…</span><kbd>Ctrl K</kbd>
    </button>
    </div>
    <div class="nim-nav">
        <?php foreach ($groups as $group => $visible): ?>
            <?php
            $gid = 'nim-nav-' . preg_replace('/[^a-z0-9]+/', '-', strtolower((string) $group));
            $hasActive = false;
            foreach ($visible as $it) {
                $hasActive = $hasActive || $this->isActive($it['prefix']) || ($it['route'] === 'websites' && $siteMenu !== null) || array_filter(array_column($it['children'], 'active')) !== [];
            }
            ?>
            <button class="nim-nav-group" type="button" aria-expanded="true" aria-controls="<?= e($gid) ?>" data-nim-nav-toggle="<?= e((string) $group) ?>"<?= $hasActive ? ' data-nim-nav-current' : '' ?>><span><?= e($group) ?></span><i class="bi bi-chevron-down" aria-hidden="true"></i></button>
            <div class="nim-nav-section" id="<?= e($gid) ?>">
            <?php foreach ($visible as $item): ?>
                <?php $inSite = $item['route'] === 'websites' && $siteMenu !== null; $active = $this->isActive($item['prefix']); ?>
                <?php $hasSub = !$inSite && $item['children'] !== []; $subOpen = $hasSub && ($active || array_filter(array_column($item['children'], 'active')) !== []); $subId = 'nim-sub-' . preg_replace('/[^a-z0-9]+/', '-', (string) $item['route']); ?>
                <a class="nim-nav-link<?= $active || $inSite ? ' active' : '' ?><?= $hasSub ? ' has-sub' : '' ?>" href="<?= e($item['url']) ?>"<?= $active ? ' aria-current="page"' : '' ?><?= $hasSub ? ' data-nim-subnav-toggle aria-controls="' . e($subId) . '" aria-expanded="' . ($subOpen ? 'true' : 'false') . '"' : '' ?>>
                    <i class="bi bi-<?= e($item['icon']) ?>"></i><span><?= e($item['label']) ?></span><?php if ($hasSub): ?><i class="bi bi-chevron-down nim-nav-caret" aria-hidden="true"></i><?php endif; ?>
                </a>
                <?php if ($hasSub): ?>
                    <div class="nim-subnav" id="<?= e($subId) ?>" data-nim-section-menu="<?= e($item['route']) ?>"<?= $subOpen ? '' : ' hidden' ?>>
                        <?php foreach ($item['children'] as $ch): ?>
                            <a class="nim-subnav-link<?= $ch['active'] ? ' active' : '' ?>" href="<?= e($ch['url']) ?>"<?= $ch['active'] ? ' aria-current="page"' : '' ?>><span><?= e($ch['label']) ?></span></a>
                        <?php endforeach; ?>
                    </div>
                <?php endif; ?>
                <?php if ($inSite): ?>
                    <div class="nim-subnav" data-nim-site-menu>
                        <div class="nim-subnav-title" title="<?= e($siteMenu['site']['domain']) ?>"><?= e($siteMenu['site']['domain']) ?></div>
                        <?php foreach ($siteMenu['items'] as $si): ?>
                            <a class="nim-subnav-link<?= $si['key'] === $siteMenu['active'] ? ' active' : '' ?>" href="<?= e($si['url']) ?>"<?= $si['key'] === $siteMenu['active'] ? ' aria-current="page"' : '' ?>>
                                <i class="bi bi-<?= e($si['icon']) ?>" aria-hidden="true"></i><span><?= e($si['label']) ?></span>
                            </a>
                        <?php endforeach; ?>
                    </div>
                <?php endif; ?>
            <?php endforeach; ?>
            </div>
        <?php endforeach; ?>
        <?php
        $pluginItems = isset($plugin_nav) && is_callable($plugin_nav) ? array_filter($plugin_nav(), fn (array $i): bool => $this->can($i['permission'])) : [];
        ?>
        <?php if ($pluginItems !== []): ?>
            <div class="nim-nav-group">Plugins</div>
            <?php foreach ($pluginItems as $i): ?>
                <a class="nim-nav-link<?= $this->isActive($i['url']) ? ' active' : '' ?>" href="<?= e($i['url']) ?>"><i class="bi bi-<?= e($i['icon']) ?>"></i><span><?= e($i['label']) ?></span></a>
            <?php endforeach; ?>
        <?php endif; ?>
    </div>
</nav>
<div class="nim-sidebar-backdrop" data-nim-toggle="sidebar"></div>
