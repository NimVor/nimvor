<?php
/** @var \WhaCP\View\View $this */

use WhaCP\View\Navigation;

$groups = Navigation::visible($this);
?>
<?php $siteMenu = $this->shared('site_menu'); ?>
<nav class="wcp-sidebar" id="wcp-sidebar" aria-label="Main navigation">
    <a class="wcp-brand" href="<?= e($this->url('dashboard')) ?>">
        <img src="<?= e($this->asset('assets/img/logo.svg')) ?>" alt="" width="28" height="28">
        <span><?= e($panel_name ?? 'WhaCP') ?></span>
    </a>
    <?php $st = isset($agent_status) && is_callable($agent_status) ? $agent_status() : null; ?>
    <?php if ($st === null): ?>
        <div class="wcp-mode-pill is-offline" title="The WhaCP agent is not reachable."><i class="bi bi-plug"></i> Agent offline</div>
    <?php elseif (empty($st['write_enabled'])): ?>
        <div class="wcp-mode-pill" title="The agent is in read-only mode: WhaCP observes the server but does not change it."><i class="bi bi-eye"></i> Read-only mode</div>
    <?php else: ?>
        <div class="wcp-mode-pill is-write" title="The agent may apply changes. Every change is validated, backed up and audited."><i class="bi bi-pencil-square"></i> Write mode</div>
    <?php endif; ?>
    <div class="wcp-nav">
        <?php foreach ($groups as $group => $visible): ?>
            <div class="wcp-nav-group"><?= e($group) ?></div>
            <?php foreach ($visible as $item): ?>
                <?php $inSite = $item['route'] === 'websites' && $siteMenu !== null; $active = $this->isActive($item['prefix']); ?>
                <a class="wcp-nav-link<?= $active || $inSite ? ' active' : '' ?>" href="<?= e($item['url']) ?>"<?= $active ? ' aria-current="page"' : '' ?>>
                    <i class="bi bi-<?= e($item['icon']) ?>"></i><span><?= e($item['label']) ?></span>
                </a>
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
    <?php if (!empty($user)): ?>
        <?php $who = $user['display_name'] !== '' ? $user['display_name'] : $user['username']; ?>
        <a class="wcp-sidebar-account" href="<?= e($this->url('profile')) ?>" title="Profile">
            <span class="wcp-avatar" aria-hidden="true"><?= e(mb_strtoupper(mb_substr((string) $who, 0, 1))) ?></span>
            <span class="min-w-0"><span class="d-block text-truncate fw-medium"><?= e($who) ?></span><span class="d-block small text-truncate wcp-sidebar-role"><?= e($user['role_label']) ?></span></span>
        </a>
    <?php endif; ?>
</nav>
<div class="wcp-sidebar-backdrop" data-wcp-toggle="sidebar"></div>
