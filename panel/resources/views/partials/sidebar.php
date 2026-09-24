<?php
/** @var \WhaCP\View\View $this */

use WhaCP\Security\Permissions as P;

// [label, icon, route name or null (planned), permission, active prefix]
$groups = [
    'Overview' => [
        ['Dashboard', 'speedometer2', 'dashboard', P::DASHBOARD_VIEW, '/'],
    ],
    'Hosting' => [
        ['Websites', 'globe2', 'websites', P::WEBSITES_VIEW, '/websites'],
        ['Domains', 'signpost-split', 'domains', P::WEBSITES_VIEW, '/domains'],
        ['Databases', 'database', 'databases', P::DATABASES_VIEW, '/databases'],
        ['PHP', 'filetype-php', 'php', P::PHP_VIEW, '/php'],
        ['Cron Jobs', 'clock-history', 'cron', P::CRON_VIEW, '/cron'],
        ['Workers', 'gear-wide-connected', 'workers', P::WORKERS_MANAGE, '/workers'],
        ['Redis', 'lightning', 'redis', P::REDIS_MANAGE, '/redis'],
        ['SSL', 'shield-lock', 'ssl', P::SSL_VIEW, '/ssl'],
    ],
    'Operations' => [
        ['Backups', 'archive', 'backups', P::BACKUPS_VIEW, '/backups'],
        ['Monitoring', 'activity', 'monitoring', P::MONITORING_VIEW, '/monitoring'],
        ['Logs', 'journal-text', 'logs', P::LOGS_VIEW, '/logs'],
    ],
    'Security' => [
        ['Security', 'shield-check', 'security', P::SECURITY_VIEW, '/security'],
    ],
    'Server' => [
        ['Server', 'hdd-rack', 'server', P::SERVER_VIEW, '/server'],
        ['All servers', 'diagram-3', 'servers', P::SERVERS_MANAGE, '/servers'],
        ['Services', 'cpu', 'services', P::SERVICES_VIEW, '/services'],
        ['Packages', 'box-seam', 'packages', P::PACKAGES_MANAGE, '/packages'],
        ['System accounts', 'people', 'system_users', P::SYSTEM_USERS_VIEW, '/system-users'],
        ['Discovery', 'binoculars', 'discovery', P::WEBSITES_VIEW, '/discovery'],
    ],
    'Administration' => [
        ['Users & roles', 'person-gear', 'users', P::USERS_VIEW, '/users'],
        ['Plans', 'card-checklist', 'plans', P::PLANS_MANAGE, '/plans'],
        ['Settings', 'sliders', 'settings', P::SETTINGS_VIEW, '/settings'],
        ['Audit log', 'list-check', 'audit', P::AUDIT_VIEW, '/audit'],
        ['Plugins', 'plug', 'plugins', P::PLUGINS_MANAGE, '/admin/plugins'],
    ],
];
?>
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
        <?php foreach ($groups as $group => $items): ?>
            <?php
            $visible = array_filter($items, fn (array $i): bool => $i[2] === null || $this->can($i[3]));
            if ($visible === []) {
                continue;
            }
            ?>
            <div class="wcp-nav-group"><?= e($group) ?></div>
            <?php foreach ($visible as [$label, $icon, $route, $perm, $prefix]): ?>
                <?php if ($route === null): ?>
                    <span class="wcp-nav-link is-planned" aria-disabled="true" title="Planned for a later release">
                        <i class="bi bi-<?= e($icon) ?>"></i><span><?= e($label) ?></span><span class="wcp-soon">soon</span>
                    </span>
                <?php else: ?>
                    <a class="wcp-nav-link<?= $this->isActive($prefix) ? ' active' : '' ?>" href="<?= e($this->url($route)) ?>"<?= $this->isActive($prefix) ? ' aria-current="page"' : '' ?>>
                        <i class="bi bi-<?= e($icon) ?>"></i><span><?= e($label) ?></span>
                    </a>
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
</nav>
<div class="wcp-sidebar-backdrop" data-wcp-toggle="sidebar"></div>
