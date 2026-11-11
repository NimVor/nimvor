<?php
/** @var \NimVor\View\View $this */
/** @var string $bodyClass */
$pageTitle = $this->section('title') !== '' ? trim(strip_tags($this->section('title'))) : ($panel_name ?? 'NimVor');
?>
<!doctype html>
<html lang="en" data-bs-theme="light"<?= $this->themeAttrs() ?>>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="csrf-token" content="<?= e($this->csrfToken()) ?>">
    <title><?= e($pageTitle) ?> · <?= e($panel_name ?? 'NimVor') ?></title>
    <link rel="icon" href="<?= e($this->asset('assets/img/favicon.ico')) ?>" sizes="any">
    <link rel="icon" href="<?= e($this->asset('assets/img/favicon-64.png')) ?>" type="image/png" sizes="64x64">
    <link rel="apple-touch-icon" href="<?= e($this->asset('assets/img/apple-touch-icon.png')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap/css/bootstrap.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/vendor/bootstrap-icons/bootstrap-icons.min.css')) ?>">
    <link rel="stylesheet" href="<?= e($this->asset('assets/css/nimvor.css')) ?>">
    <?= $this->partial('partials/theme_assets') ?>
    <?= $this->section('head') ?>
    <script src="<?= e($this->asset('assets/js/theme.js')) ?>"></script>
</head>
<?php // Pages with their own scripts, or that opt out, submit forms normally (see nimvor.js). ?>
<body class="nim-body nt-body <?= e($bodyClass) ?>"<?= $this->section('scripts') !== '' || trim($this->section('ajax')) === 'off' ? ' data-nim-ajax="off"' : '' ?>>
