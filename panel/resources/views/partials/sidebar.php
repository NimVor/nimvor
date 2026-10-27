<?php
/** @var \WhaCP\View\View $this */

use WhaCP\View\Navigation;

$groups = Navigation::visible($this);
?>
<?php $siteMenu = $this->shared('site_menu'); ?>
<nav class="wcp-sidebar" id="wcp-sidebar" aria-label="Main navigation">
    <div class="wcp-sidebar-head">
    <a class="wcp-brand" href="<?= e($this->url('dashboard')) ?>">
        <img src="<?= e($this->asset('assets/img/logo.svg')) ?>" alt="" width="28" height="28">
        <span><?= e($panel_name ?? 'NimVor') ?></span>
    </a>
    <?php $st = isset($agent_status) && is_callable($agent_status) ? $agent_status() : null; ?>
    <?php if ($st === null): ?>
        <div class="wcp-mode-pill is-offline" title="The NimVor agent is not reachable."><i class="bi bi-plug"></i> Agent offline</div>
    <?php elseif (empty($st['write_enabled'])): ?>
        <div class="wcp-mode-pill" title="The agent is in read-only mode: NimVor observes the server but does not change it."><i class="bi bi-eye"></i> Read-only mode</div>
    <?php endif; ?>
    <button class="wcp-search-trigger wcp-sidebar-search" type="button" data-bs-toggle="modal" data-bs-target="#wcp-palette" aria-label="Search tools (Ctrl+K)">
        <i class="bi bi-search" aria-hidden="true"></i><span>Search tools…</span><kbd>Ctrl K</kbd>
    </button>
    </div>
    <div class="wcp-nav">
        <?php foreach ($groups as $group => $visible): ?>
            <?php
            $gid = 'wcp-nav-' . preg_replace('/[^a-z0-9]+/', '-', strtolower((string) $group));
            $hasActive = false;
            foreach ($visible as $it) {
                $hasActive = $hasActive || $this->isActive($it['prefix']) || ($it['route'] === 'websites' && $siteMenu !== null) || array_filter(array_column($it['children'], 'active')) !== [];
            }
            ?>
            <button class="wcp-nav-group" type="button" aria-expanded="true" aria-controls="<?= e($gid) ?>" data-wcp-nav-toggle="<?= e((string) $group) ?>"<?= $hasActive ? ' data-wcp-nav-current' : '' ?>><span><?= e($group) ?></span><i class="bi bi-chevron-down" aria-hidden="true"></i></button>
            <div class="wcp-nav-section" id="<?= e($gid) ?>">
            <?php foreach ($visible as $item): ?>
                <?php $inSite = $item['route'] === 'websites' && $siteMenu !== null; $active = $this->isActive($item['prefix']); ?>
                <?php $hasSub = !$inSite && $item['children'] !== []; $subOpen = $hasSub && ($active || array_filter(array_column($item['children'], 'active')) !== []); $subId = 'wcp-sub-' . preg_replace('/[^a-z0-9]+/', '-', (string) $item['route']); ?>
                <a class="wcp-nav-link<?= $active || $inSite ? ' active' : '' ?><?= $hasSub ? ' has-sub' : '' ?>" href="<?= e($item['url']) ?>"<?= $active ? ' aria-current="page"' : '' ?><?= $hasSub ? ' data-wcp-subnav-toggle aria-controls="' . e($subId) . '" aria-expanded="' . ($subOpen ? 'true' : 'false') . '"' : '' ?>>
                    <i class="bi bi-<?= e($item['icon']) ?>"></i><span><?= e($item['label']) ?></span><?php if ($hasSub): ?><i class="bi bi-chevron-down wcp-nav-caret" aria-hidden="true"></i><?php endif; ?>
                </a>
                <?php if ($hasSub): ?>
                    <div class="wcp-subnav" id="<?= e($subId) ?>" data-wcp-section-menu="<?= e($item['route']) ?>"<?= $subOpen ? '' : ' hidden' ?>>
                        <?php foreach ($item['children'] as $ch): ?>
                            <a class="wcp-subnav-link<?= $ch['active'] ? ' active' : '' ?>" href="<?= e($ch['url']) ?>"<?= $ch['active'] ? ' aria-current="page"' : '' ?>><span><?= e($ch['label']) ?></span></a>
                        <?php endforeach; ?>
                    </div>
                <?php endif; ?>
                <?php if ($inSite): ?>
                    <div class="wcp-subnav" data-wcp-site-menu>
                        <div class="wcp-subnav-title" title="<?= e($siteMenu['site']['domain']) ?>"><?= e($siteMenu['site']['domain']) ?></div>
                        <?php foreach ($siteMenu['items'] as $si): ?>
                            <a class="wcp-subnav-link<?= $si['key'] === $siteMenu['active'] ? ' active' : '' ?>" href="<?= e($si['url']) ?>"<?= $si['key'] === $siteMenu['active'] ? ' aria-current="page"' : '' ?>>
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
            <div class="wcp-nav-group">Plugins</div>
            <?php foreach ($pluginItems as $i): ?>
                <a class="wcp-nav-link<?= $this->isActive($i['url']) ? ' active' : '' ?>" href="<?= e($i['url']) ?>"><i class="bi bi-<?= e($i['icon']) ?>"></i><span><?= e($i['label']) ?></span></a>
            <?php endforeach; ?>
        <?php endif; ?>
    </div>
</nav>
<div class="wcp-sidebar-backdrop" data-wcp-toggle="sidebar"></div>
