<?php
/** @var \NimVorSite\Core\View $v */
$title = $v->meta('title');
$full = $title === '' ? 'NimVor — the open-source control panel for Linux servers' : $title . ' · NimVor';
$desc = $v->meta('description', 'NimVor is a free, open-source hosting control panel for VPS and dedicated servers: websites, email, DNS, databases, SSL and backups, with every change validated and reversible.');
?>
<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title><?= e($full) ?></title>
    <meta name="description" content="<?= e($desc) ?>">
    <link rel="canonical" href="<?= e($siteUrl . $path) ?>">
    <meta property="og:title" content="<?= e($full) ?>">
    <meta property="og:description" content="<?= e($desc) ?>">
    <meta property="og:type" content="website">
    <meta property="og:url" content="<?= e($siteUrl . $path) ?>">
    <meta name="theme-color" content="#f5f8fe">
    <link rel="icon" href="<?= e($v->asset('img/favicon.ico')) ?>" sizes="any">
    <link rel="icon" href="<?= e($v->asset('img/favicon-64.png')) ?>" type="image/png" sizes="64x64">
    <link rel="apple-touch-icon" href="<?= e($v->asset('img/apple-touch-icon.png')) ?>">
    <link rel="stylesheet" href="<?= e($v->asset('css/site.css')) ?>">
    <script src="<?= e($v->asset('js/site.js')) ?>" defer></script>
    <?php if ($v->meta('script') !== ''): ?><script src="<?= e($v->asset($v->meta('script'))) ?>" defer></script><?php endif; ?>
</head>
<body>
<a class="skip" href="#main">Skip to content</a>
<?= $v->partial('partials/header') ?>
<main id="main">
    <?php if ($flash !== null): ?>
        <div class="container pt-2"><div class="alert alert-<?= e($flash['type']) ?>" role="status"><?= e($flash['message']) ?></div></div>
    <?php endif; ?>
    <?= $content ?>
</main>
<?= $v->partial('partials/footer') ?>
<a class="chat-fab" href="/support/contact" aria-label="Contact us"><?= $v->icon('chat', 26) ?></a>
</body>
</html>
