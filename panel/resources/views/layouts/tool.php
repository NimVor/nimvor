<?php
/** @var \WhaCP\View\View $this */
/** @var string $content */
$pageTitle = $this->section('title') !== '' ? trim(strip_tags($this->section('title'))) : ($panel_name ?? 'NimVor');
?>
<!doctype html>
<html lang="en" data-bs-theme="light">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="csrf-token" content="<?= e($this->csrfToken()) ?>">
    <title><?= e($pageTitle) ?> · <?= e($panel_name ?? 'NimVor') ?></title>
    <link rel="icon" href="<?= e($this->asset('assets/img/logo.svg')) ?>" type="image/svg+xml">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap/css/bootstrap.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap-icons/bootstrap-icons.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/css/whacp.css')) ?>">
    <script src="<?= e($this->asset('assets/js/theme.js')) ?>"></script>
</head>
<body class="wcp-tool">
<header class="wcp-tool-bar">
    <?= $this->section('back') ?>
    <div class="wcp-tool-title"><?= $this->section('title') ?></div>
    <div class="ms-auto d-flex align-items-center gap-2">
        <?= $this->section('actions') ?>
        <button class="btn btn-sm btn-outline-secondary" type="button" data-wcp-theme-toggle aria-label="Toggle dark mode"><i class="bi bi-circle-half"></i></button>
    </div>
</header>
<main class="wcp-tool-main">
    <?= $this->partial('partials/flash') ?>
    <?= $content ?>
</main>
<?= $this->partial('partials/confirm_modal') ?>
<?= $this->partial('partials/job_modal') ?>
<div class="toast-container position-fixed bottom-0 end-0 p-3" id="wcp-toasts"></div>
<script src="<?= e($this->asset('assets/vendor/bootstrap/js/bootstrap.bundle.min.js')) ?>"></script>
<script src="<?= e($this->asset('assets/js/whacp.js')) ?>"></script>
<?= $this->section('scripts') ?>
</body>
</html>
