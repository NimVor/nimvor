<?php
/** @var \WhaCP\View\View $this */
/** @var string $content */
$pageTitle = $this->section('title') !== '' ? trim(strip_tags($this->section('title'))) : ($panel_name ?? 'WhaCP');
// Hosting customers: no sidebar, the brand in the top bar leads to their websites.
$simple = $this->simpleLayout();
// Server administrators: the server's name, system, version and load in the top bar.
$serverAdmin = !$simple && !empty($user) && \WhaCP\Hosting\Scope::isAll($user);
$topStatus = $serverAdmin && isset($agent_status) && is_callable($agent_status) ? $agent_status() : null;
$topHost = (string) ($topStatus['hostname'] ?? '');
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
            <button class="btn btn-link wcp-sidebar-toggle" type="button" data-wcp-toggle="sidebar" aria-label="Show or hide the menu" aria-controls="wcp-sidebar">
                <i class="bi bi-list"></i>
            </button>
            <?php endif; ?>
            <div class="wcp-topbar-title">
                <h1 class="h5 mb-0"><?= $this->section('title') !== '' ? $this->section('title') : e($pageTitle) ?></h1>
                <?php if ($this->section('subtitle') !== ''): ?>
                    <div class="wcp-subtitle"><?= $this->section('subtitle') ?></div>
                <?php endif; ?>
            </div>
            <?php if ($serverAdmin && $topStatus !== null): $load = (array) ($topStatus['load'] ?? []); ?>
                <dl class="wcp-topbar-server d-none d-xl-flex" aria-label="Server">
                    <div><dt>Hostname</dt><dd class="text-truncate" title="<?= e($topHost) ?>"><?= e($topHost !== '' ? $topHost : '—') ?></dd></div>
                    <div><dt>OS</dt><dd class="text-truncate"><?= e((string) ($topStatus['os']['pretty_name'] ?? '—')) ?></dd></div>
                    <div><dt>WhaCP</dt><dd><?= e((string) ($app_version ?? '')) ?></dd></div>
                    <div><dt>Load averages</dt><dd>
                        <?php $loadText = count($load) === 3 ? implode(' ', array_map(static fn ($v): string => number_format((float) $v, 2), $load)) : '—'; ?>
                        <?php if ($this->can(\WhaCP\Security\Permissions::MONITORING_VIEW)): ?>
                            <a href="<?= e($this->url('monitoring.live')) ?>" title="1, 5 and 15 minutes · live monitoring" data-wcp-topbar-load="<?= e($this->url('topbar.status')) ?>"><i class="bi bi-activity me-1" aria-hidden="true"></i><span><?= e($loadText) ?></span></a>
                        <?php else: ?><span><?= e($loadText) ?></span><?php endif; ?>
                    </dd></div>
                </dl>
            <?php endif; ?>
            <div class="wcp-topbar-actions">
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
<?php if ($serverAdmin && $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE) && $topHost !== ''): ?>
    <div class="modal fade" id="wcp-reboot" tabindex="-1" aria-labelledby="wcp-reboot-title" aria-hidden="true">
        <div class="modal-dialog modal-dialog-centered">
            <form class="modal-content" method="post" action="<?= e($this->url('server.reboot')) ?>" data-wcp-noajax>
                <?= $this->csrfField() ?>
                <div class="modal-header"><h2 class="modal-title h5" id="wcp-reboot-title"><i class="bi bi-power me-1"></i>Restart the server</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
                <div class="modal-body">
                    <p class="small">Websites, mail and the panel are unavailable until the server is back (usually 1–3 minutes). Signed-in users are warned; you can cancel on the Server page until then.</p>
                    <div class="row g-2">
                        <div class="col-sm-5"><label class="form-label small" for="wcp-reboot-when">When</label><select class="form-select form-select-sm" id="wcp-reboot-when" name="minutes"><option value="1">in 1 minute</option><option value="5">in 5 minutes</option><option value="30">in 30 minutes</option><option value="60">in 1 hour</option></select></div>
                        <div class="col-sm-7"><label class="form-label small" for="wcp-reboot-confirm">Type <code><?= e($topHost) ?></code> to confirm</label><input class="form-control form-control-sm" id="wcp-reboot-confirm" name="confirm" autocomplete="off" required pattern="<?= e(preg_quote($topHost, '/')) ?>"></div>
                    </div>
                </div>
                <div class="modal-footer"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-danger"><i class="bi bi-power me-1"></i>Restart server</button></div>
            </form>
        </div>
    </div>
<?php endif; ?>
<?= $this->partial('partials/job_modal') ?>
<div class="toast-container position-fixed bottom-0 end-0 p-3" id="wcp-toasts"></div>
<script src="<?= e($this->asset('assets/vendor/bootstrap/js/bootstrap.bundle.min.js')) ?>"></script>
<script src="<?= e($this->asset('assets/js/whacp.js')) ?>"></script>
<?= $this->section('scripts') ?>
</body>
</html>
