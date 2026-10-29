<?php
/** @var \NimVor\View\View $this */
/** @var string $content */
?>
<!doctype html>
<html lang="en" data-bs-theme="light">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="csrf-token" content="<?= e($this->csrfToken()) ?>">
    <title><?= e($title ?? 'Sign in') ?> · <?= e($panel_name ?? 'NimVor') ?></title>
    <link rel="icon" href="<?= e($this->asset('assets/img/favicon.ico')) ?>" sizes="any">
    <link rel="icon" href="<?= e($this->asset('assets/img/favicon-64.png')) ?>" type="image/png" sizes="64x64">
    <link rel="apple-touch-icon" href="<?= e($this->asset('assets/img/apple-touch-icon.png')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap/css/bootstrap.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap-icons/bootstrap-icons.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/css/nimvor.css')) ?>">
    <script src="<?= e($this->asset('assets/js/theme.js')) ?>"></script>
</head>
<body class="nim-auth-body">
<main class="nim-auth">
    <div class="nim-auth-brand">
        <img class="nim-logo-light" src="<?= e($this->asset('assets/img/logo-light.png')) ?>" alt="" width="56" height="56">
        <img class="nim-logo-dark" src="<?= e($this->asset('assets/img/logo-dark.png')) ?>" alt="" width="56" height="56">
        <span><?= e($panel_name ?? 'NimVor') ?></span>
    </div>
    <?= $content ?>
    <p class="nim-auth-foot">NimVor <?= e($app_version ?? '') ?></p>
</main>
</body>
</html>
