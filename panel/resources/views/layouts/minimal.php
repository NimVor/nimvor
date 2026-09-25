<?php
/** @var \WhaCP\View\View $this */
/** @var string $content */
?>
<!doctype html>
<html lang="en" data-bs-theme="light">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="csrf-token" content="<?= e($this->csrfToken()) ?>">
    <title><?= e($title ?? 'Sign in') ?> · <?= e($panel_name ?? 'WhaCP') ?></title>
    <link rel="icon" href="<?= e($this->asset('assets/img/logo.svg')) ?>" type="image/svg+xml">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap/css/bootstrap.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap-icons/bootstrap-icons.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/css/whacp.css')) ?>">
    <script src="<?= e($this->asset('assets/js/theme.js')) ?>"></script>
</head>
<body class="wcp-auth-body">
<main class="wcp-auth">
    <div class="wcp-auth-brand">
        <img src="<?= e($this->asset('assets/img/logo.svg')) ?>" alt="" width="40" height="40">
        <span><?= e($panel_name ?? 'WhaCP') ?></span>
    </div>
    <?= $content ?>
    <p class="wcp-auth-foot">WhaCP <?= e($app_version ?? '') ?></p>
</main>
</body>
</html>
