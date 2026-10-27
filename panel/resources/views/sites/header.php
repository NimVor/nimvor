<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
use WhaCP\View\SiteMenu;

$active = $tab ?? 'overview';
$scheme = $site['https'] ? 'https://' : 'http://';
$items = SiteMenu::items($this, $site);
// The sidebar shows this website's menu under "Websites".
$this->share('site_menu', ['site' => $site, 'active' => $active, 'items' => $items]);
$current = null;
foreach ($items as $i) {
    if ($i['key'] === $active) {
        $current = $i;
    }
}
?>
<?php $this->start(); ?><?= e($site['domain']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?>
<span class="wcp-site-status">
    <?php if (!empty($site['account_suspended'])): ?><span class="wcp-state is-error">Account suspended</span>
    <?php elseif ($site['status'] === 'error'): ?><span class="wcp-state is-error">Needs attention</span>
    <?php elseif ($site['enabled']): ?><span class="wcp-state is-ok">Active</span>
    <?php else: ?><span class="wcp-state is-off">Disabled</span><?php endif; ?>
    <?php if ($site['kind'] === 'php'): ?><span>PHP <?= e($site['php_version']) ?></span><?php elseif ($site['kind'] === 'proxy'): ?><span>Application proxy</span><?php else: ?><span>Static website</span><?php endif; ?>
    <span><i class="bi bi-<?= $site['https'] ? 'lock-fill text-success' : 'unlock' ?>" aria-hidden="true"></i> HTTPS <?= $site['https'] ? 'on' : 'off' ?></span>
</span>
<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<?php if (!$this->simpleLayout()): ?>
<button class="btn btn-sm btn-outline-secondary d-lg-none" type="button" data-wcp-toggle="sidebar" aria-controls="wcp-sidebar" data-wcp-site-menu-button><i class="bi bi-list-nested"></i> Website menu</button>
<?php endif; ?>
<?php if ($site['enabled']): ?>
    <a class="btn btn-sm btn-outline-primary" href="<?= e($scheme . $site['domain']) ?>/" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right"></i><span class="d-none d-sm-inline"> Visit site</span></a>
<?php endif; ?>
<?php $this->end('actions'); ?>
<nav class="wcp-crumbs" aria-label="Breadcrumb">
    <a href="<?= e($this->url('websites')) ?>">Websites</a>
    <i class="bi bi-chevron-right" aria-hidden="true"></i>
    <?php if ($active === 'overview' || $current === null): ?>
        <span aria-current="page"><?= e($site['domain']) ?></span>
    <?php else: ?>
        <a href="<?= e($this->url('sites.show', ['id' => (int) $site['id']])) ?>"><?= e($site['domain']) ?></a>
        <i class="bi bi-chevron-right" aria-hidden="true"></i>
        <span aria-current="page"><i class="bi bi-<?= e($current['icon']) ?> me-1" aria-hidden="true"></i><?= e($current['label']) ?></span>
    <?php endif; ?>
</nav>
