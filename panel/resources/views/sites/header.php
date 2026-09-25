<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
use WhaCP\Security\Permissions as P;

$tabs = [
    ['overview', 'Overview', 'sites.show', P::WEBSITES_VIEW, 'info-circle'],
    ['settings', 'Settings', 'sites.edit', P::WEBSITES_MANAGE, 'sliders'],
    ['ssl', 'SSL', 'sites.ssl', P::SSL_VIEW, 'shield-lock'],
    ['dns', 'DNS', 'sites.dns', P::DNS_MANAGE, 'signpost'],
    ['mail', 'Email', 'sites.mail', P::MAIL_MANAGE, 'envelope'],
    ['databases', 'Databases', 'sites.databases', P::DATABASES_VIEW, 'database'],
    ['files', 'Files', 'sites.files', P::FILES_MANAGE, 'folder2-open'],
    ['ftp', 'FTP / SFTP', 'sites.ftp', P::FTP_MANAGE, 'hdd-network'],
    ['logs', 'Logs', 'sites.logs', P::LOGS_VIEW, 'journal-text'],
    ['cron', 'Cron', 'sites.cron', P::CRON_VIEW, 'clock-history'],
    ['workers', 'Workers', 'sites.workers', P::WORKERS_MANAGE, 'gear-wide-connected'],
    ['git', 'Git', 'sites.git', P::DEPLOY_MANAGE, 'git'],
    ['redis', 'Redis', 'sites.redis', P::WEBSITES_MANAGE, 'lightning'],
    ['backups', 'Backups', 'sites.backups', P::BACKUPS_VIEW, 'archive'],
    ['apps', 'Apps', 'sites.apps', P::APPS_INSTALL, 'grid-3x3-gap'],
];
$active = $tab ?? 'overview';
$scheme = $site['https'] ? 'https://' : 'http://';
?>
<?php $this->start(); ?><?= e($site['domain']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?>
<?= $site['enabled'] ? $this->statusBadge('active', 'enabled') : $this->statusBadge('inactive', 'disabled') ?>
<?php if ($site['status'] === 'error'): ?><?= $this->statusBadge('error', 'needs attention') ?><?php endif; ?>
· <?= e(\WhaCP\Services\WebsiteService::KINDS[$site['kind']] ?? $site['kind']) ?><?= $site['kind'] === 'php' ? ' · PHP ' . e($site['php_version']) : '' ?>
<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('websites')) ?>"><i class="bi bi-arrow-left"></i> Websites</a>
<?php if ($site['enabled']): ?>
    <a class="btn btn-sm btn-outline-primary" href="<?= e($scheme . $site['domain']) ?>/" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right"></i> Open</a>
<?php endif; ?>
<?php $this->end('actions'); ?>
<ul class="nav nav-tabs wcp-tabs">
    <?php foreach ($tabs as [$key, $label, $route, $perm, $icon]): ?>
        <?php if ($this->hasRoute($route) && $this->can($perm)): ?>
            <li class="nav-item"><a class="nav-link<?= $active === $key ? ' active' : '' ?>" href="<?= e($this->url($route, ['id' => (int) $site['id']])) ?>"><i class="bi bi-<?= e($icon) ?> me-1"></i><?= e($label) ?></a></li>
        <?php endif; ?>
    <?php endforeach; ?>
</ul>
