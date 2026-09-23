<?php
/** @var \WhaCP\View\View $this */
/** @var string $content */
$pageTitle = $this->section('title') !== '' ? trim(strip_tags($this->section('title'))) : ($panel_name ?? 'WhaCP');
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
    <script src="<?= e($this->asset('assets/js/theme.js')) ?>"></script>
</head>
<body class="wcp-body">
<div class="wcp-shell">
    <?= $this->partial('partials/sidebar') ?>
    <div class="wcp-main">
        <header class="wcp-topbar">
            <button class="btn btn-link wcp-sidebar-toggle d-lg-none" type="button" data-wcp-toggle="sidebar" aria-label="Toggle navigation">
                <i class="bi bi-list"></i>
            </button>
            <div class="wcp-topbar-title">
                <h1 class="h5 mb-0"><?= $this->section('title') !== '' ? $this->section('title') : e($pageTitle) ?></h1>
                <?php if ($this->section('subtitle') !== ''): ?>
                    <div class="wcp-subtitle"><?= $this->section('subtitle') ?></div>
                <?php endif; ?>
            </div>
            <div class="wcp-topbar-actions">
                <?= $this->section('actions') ?>
                <button class="btn btn-sm btn-outline-secondary" type="button" data-wcp-theme-toggle aria-label="Toggle dark mode">
                    <i class="bi bi-circle-half"></i>
                </button>
                <?php if (!empty($user)): ?>
                    <div class="dropdown">
                        <button class="btn btn-sm btn-outline-secondary dropdown-toggle" data-bs-toggle="dropdown" aria-expanded="false">
                            <i class="bi bi-person-circle me-1"></i><?= e($user['display_name'] !== '' ? $user['display_name'] : $user['username']) ?>
                        </button>
                        <ul class="dropdown-menu dropdown-menu-end">
                            <li><span class="dropdown-item-text small text-body-secondary"><?= e($user['role_label']) ?></span></li>
                            <li><a class="dropdown-item" href="<?= e($this->url('profile')) ?>"><i class="bi bi-person me-2"></i>Profile</a></li>
                            <li><hr class="dropdown-divider"></li>
                            <li>
                                <form method="post" action="<?= e($this->url('logout')) ?>">
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
<?= $this->partial('partials/confirm_modal') ?>
<div class="toast-container position-fixed bottom-0 end-0 p-3" id="wcp-toasts"></div>
<script src="<?= e($this->asset('assets/vendor/bootstrap/js/bootstrap.bundle.min.js')) ?>"></script>
<script src="<?= e($this->asset('assets/js/whacp.js')) ?>"></script>
</body>
</html>
