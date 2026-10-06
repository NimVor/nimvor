<?php
/** @var \WhaCP\View\View $this */
/** @var string $content */
$pageTitle = $this->section('title') !== '' ? trim(strip_tags($this->section('title'))) : ($panel_name ?? 'WhaCP');
// Hosting customers: no sidebar, the brand in the top bar leads to their websites.
$simple = $this->simpleLayout();
?>
<!doctype html>
<html lang="en" data-bs-theme="light">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="csrf-token" content="<?= e($this->csrfToken()) ?>">
    <title><?= e($pageTitle) ?> · <?= e($panel_name ?? 'WhaCP') ?></title>
    <link rel="icon" href="<?= e($this->asset('assets/img/logo.svg')) ?>" type="image/svg+xml">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap/css/bootstrap.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap-icons/bootstrap-icons.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/css/whacp.css')) ?>">
    <?= $this->section('head') ?>
    <script src="<?= e($this->asset('assets/js/theme.js')) ?>"></script>
</head>
<?php // Pages with their own scripts, or that opt out, submit forms normally (see whacp.js). ?>
<body class="wcp-body<?= $simple ? ' is-simple' : '' ?>"<?= $this->section('scripts') !== '' || trim($this->section('ajax')) === 'off' ? ' data-wcp-ajax="off"' : '' ?>>
<div class="wcp-shell">
    <?php if (!$simple): ?><?= $this->partial('partials/sidebar') ?><?php endif; ?>
    <div class="wcp-main">
        <?php if ($simple): ?>
            <div class="wcp-brandbar">
                <a class="wcp-brand" href="<?= e($this->url('websites')) ?>">
                    <img src="<?= e($this->asset('assets/img/logo.svg')) ?>" alt="" width="26" height="26">
                    <span><?= e($panel_name ?? 'WhaCP') ?></span>
                </a>
                <a class="wcp-brandbar-link<?= $this->isActive('/websites') || $this->isActive('/sites') ? ' active' : '' ?>" href="<?= e($this->url('websites')) ?>"><i class="bi bi-globe2 me-1"></i>Websites</a>
            </div>
        <?php endif; ?>
        <header class="wcp-topbar">
            <?php if (!$simple): ?>
            <button class="btn btn-link wcp-sidebar-toggle d-lg-none" type="button" data-wcp-toggle="sidebar" aria-label="Toggle navigation">
                <i class="bi bi-list"></i>
            </button>
            <?php endif; ?>
            <div class="wcp-topbar-title">
                <h1 class="h5 mb-0"><?= $this->section('title') !== '' ? $this->section('title') : e($pageTitle) ?></h1>
                <?php if ($this->section('subtitle') !== ''): ?>
                    <div class="wcp-subtitle"><?= $this->section('subtitle') ?></div>
                <?php endif; ?>
            </div>
            <div class="wcp-topbar-actions">
                <?php if (!$simple): ?>
                    <button class="wcp-search-trigger" type="button" data-bs-toggle="modal" data-bs-target="#wcp-palette" aria-label="Search tools (Ctrl+K)">
                        <i class="bi bi-search" aria-hidden="true"></i><span class="d-none d-md-inline">Search tools…</span><kbd class="d-none d-md-inline">Ctrl K</kbd>
                    </button>
                <?php endif; ?>
                <?= $this->section('actions') ?>
                <?php $sw = isset($server_switcher) && is_callable($server_switcher) ? $server_switcher() : null; ?>
                <?php if ($sw !== null && count($sw['servers']) > 1): ?>
                    <form method="post" action="<?= e($this->url('servers.switch')) ?>" class="d-flex" data-wcp-noajax>
                        <?= $this->csrfField() ?>
                        <select class="form-select form-select-sm" name="server_id" data-wcp-autosubmit aria-label="Server">
                            <?php foreach ($sw['servers'] as $srv): ?><option value="<?= (int) $srv['id'] ?>"<?= (int) $srv['id'] === $sw['current'] ? ' selected' : '' ?>><?= e($srv['name']) ?></option><?php endforeach; ?>
                        </select>
                    </form>
                <?php endif; ?>
                <?php $unreadCount = isset($notification_count) && is_callable($notification_count) ? $notification_count() : null; ?>
                <?php if ($unreadCount !== null): ?>
                    <div class="dropdown">
                        <a class="btn btn-sm btn-outline-secondary position-relative" href="<?= e($this->url('notifications')) ?>" role="button" data-bs-toggle="dropdown" data-bs-auto-close="outside" aria-expanded="false" data-wcp-bell data-wcp-bell-url="<?= e($this->url('notifications.summary')) ?>" data-wcp-bell-read="<?= e($this->url('notifications.read')) ?>" aria-label="Notifications<?= $unreadCount > 0 ? ' (' . (int) $unreadCount . ' unread)' : '' ?>">
                            <i class="bi bi-bell"></i>
                            <span class="position-absolute top-0 start-100 translate-middle badge rounded-pill text-bg-danger<?= $unreadCount > 0 ? '' : ' d-none' ?>" data-wcp-bell-count><?= $unreadCount > 99 ? '99+' : (int) $unreadCount ?></span>
                        </a>
                        <div class="dropdown-menu dropdown-menu-end p-0 wcp-bell-menu" data-wcp-bell-menu>
                            <div class="d-flex align-items-center justify-content-between px-3 py-2 border-bottom">
                                <span class="fw-semibold">Notifications</span>
                                <button class="btn btn-sm btn-link p-0" type="button" data-wcp-bell-all>Mark all read</button>
                            </div>
                            <div class="wcp-bell-list" data-wcp-bell-list><div class="px-3 py-3 small text-body-secondary">Loading…</div></div>
                            <a class="d-block text-center small px-3 py-2 border-top" href="<?= e($this->url('notifications')) ?>">View all notifications</a>
                        </div>
                    </div>
                <?php endif; ?>
                <button class="btn btn-sm btn-outline-secondary" type="button" data-wcp-theme-toggle aria-label="Toggle dark mode">
                    <i class="bi bi-circle-half"></i>
                </button>
                <?php if (!empty($user)): ?>
                    <div class="dropdown">
                        <?php $who = $user['display_name'] !== '' ? $user['display_name'] : $user['username']; ?>
                        <button class="btn btn-sm btn-outline-secondary dropdown-toggle" data-bs-toggle="dropdown" aria-expanded="false" aria-label="Account: <?= e($who) ?>">
                            <i class="bi bi-person-circle"></i><span class="d-none d-sm-inline ms-1"><?= e($who) ?></span>
                        </button>
                        <ul class="dropdown-menu dropdown-menu-end">
                            <li><span class="dropdown-item-text small text-body-secondary"><?= e($user['role_label']) ?></span></li>
                            <li><a class="dropdown-item" href="<?= e($this->url('profile')) ?>"><i class="bi bi-person me-2"></i>Profile</a></li>
                            <li><hr class="dropdown-divider"></li>
                            <li>
                                <form method="post" action="<?= e($this->url('logout')) ?>" data-wcp-noajax>
                                    <?= $this->csrfField() ?>
                                    <button class="dropdown-item" type="submit"><i class="bi bi-box-arrow-right me-2"></i>Sign out</button>
                                </form>
                            </li>
                        </ul>
                    </div>
                <?php endif; ?>
            </div>
        </header>
        <main class="wcp-content">
            <?= $this->partial('partials/flash') ?>
            <?= $content ?>
        </main>
        <footer class="wcp-footer">
            WhaCP <?= e($app_version ?? '') ?> · Open-source server control panel
        </footer>
    </div>
</div>
<?php if (!$simple): ?><?= $this->partial('partials/palette') ?><?php endif; ?>
<?= $this->partial('partials/confirm_modal') ?>
<?= $this->partial('partials/job_modal') ?>
<div class="toast-container position-fixed bottom-0 end-0 p-3" id="wcp-toasts"></div>
<script src="<?= e($this->asset('assets/vendor/bootstrap/js/bootstrap.bundle.min.js')) ?>"></script>
<script src="<?= e($this->asset('assets/js/whacp.js')) ?>"></script>
<?= $this->section('scripts') ?>
</body>
</html>
