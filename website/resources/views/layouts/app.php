<?php
/** @var \NimVorSite\Core\View $v */
$full = ($v->meta('title') !== '' ? $v->meta('title') . ' · ' : '') . 'NimVor';
?>
<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="robots" content="noindex, nofollow">
    <title><?= e($full) ?></title>
    <link rel="icon" href="<?= e($v->asset('img/favicon.ico')) ?>" sizes="any">
    <link rel="icon" href="<?= e($v->asset('img/favicon-64.png')) ?>" type="image/png" sizes="64x64">
    <link rel="apple-touch-icon" href="<?= e($v->asset('img/apple-touch-icon.png')) ?>">
    <link rel="stylesheet" href="<?= e($v->asset('css/site.css')) ?>">
    <script src="<?= e($v->asset('js/site.js')) ?>" defer></script>
</head>
<body>
<a class="skip" href="#main">Skip to content</a>
<?= $v->partial('partials/header') ?>
<div class="app-shell">
    <nav class="app-nav" aria-label="<?= $area === 'admin' ? 'Administration' : 'Your account' ?>">
        <?php if ($area === 'admin'): ?>
            <h5>Administration</h5>
            <a href="/admin"<?= $path === '/admin' ? ' aria-current="page"' : '' ?>><?= $v->icon('chart', 18) ?>Overview</a>
            <a href="/admin/reports"<?= $v->active('/admin/reports') ?>><?= $v->icon('file', 18) ?>Reports</a>
            <a href="/admin/users"<?= $v->active('/admin/users') ?>><?= $v->icon('users', 18) ?>Users</a>
            <a href="/admin/messages"<?= $v->active('/admin/messages') ?>><?= $v->icon('mail', 18) ?>Messages</a>
            <a href="/admin/requests"<?= $v->active('/admin/requests') ?>><?= $v->icon('tool', 18) ?>Build requests</a>
            <h5>Content</h5>
            <a href="/admin/posts"<?= $v->active('/admin/posts') ?>><?= $v->icon('pen', 18) ?>Blog</a>
            <a href="/admin/sponsors"<?= $v->active('/admin/sponsors') ?>><?= $v->icon('heart', 18) ?>Sponsors</a>
            <a href="/admin/content"<?= $v->active('/admin/content') ?>><?= $v->icon('book', 18) ?>Docs and stats</a>
            <h5>Your account</h5>
            <a href="/account"><?= $v->icon('users', 18) ?>Client view</a>
        <?php else: ?>
            <h5>Your account</h5>
            <a href="/account"<?= $path === '/account' ? ' aria-current="page"' : '' ?>><?= $v->icon('chart', 18) ?>Dashboard</a>
            <a href="/account/requests"<?= $v->active('/account/requests') ?>><?= $v->icon('tool', 18) ?>Build requests</a>
            <a href="/account/messages"<?= $v->active('/account/messages') ?>><?= $v->icon('mail', 18) ?>Messages</a>
            <a href="/account/profile"<?= $v->active('/account/profile') ?>><?= $v->icon('lock', 18) ?>Profile and password</a>
            <?php if (($user['role'] ?? '') === 'admin'): ?>
                <h5>Administration</h5>
                <a href="/admin"><?= $v->icon('shield', 18) ?>Admin area</a>
            <?php endif; ?>
        <?php endif; ?>
    </nav>
    <main id="main" class="app-main">
        <?php if ($flash !== null): ?>
            <div class="alert alert-<?= e($flash['type']) ?>" role="status"><?= e($flash['message']) ?></div>
        <?php endif; ?>
        <?= $content ?>
    </main>
</div>
</body>
</html>
