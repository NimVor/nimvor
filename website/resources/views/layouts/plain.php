<?php /** @var \NimVorSite\Core\View $v */ ?>
<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="robots" content="noindex, nofollow">
    <title><?= e(($v->meta('title') !== '' ? $v->meta('title') . ' · ' : '') . 'NimVor') ?></title>
    <link rel="icon" href="<?= e($v->asset('img/logo.svg')) ?>" type="image/svg+xml">
    <link rel="stylesheet" href="<?= e($v->asset('css/site.css')) ?>">
</head>
<body>
<main class="setup-wrap"><div class="setup-card">
    <a class="brand brand-dark" href="/"><img src="<?= e($v->asset('img/logo.svg')) ?>" width="34" height="34" alt="">NimVor</a>
    <?= $content ?>
</div></main>
</body>
</html>
