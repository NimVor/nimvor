<?php /** @var \NimVorSite\Core\View $v */ ?>
<header class="site-header">
    <div class="container bar">
        <a class="brand" href="/" aria-label="NimVor home"><img src="<?= e($v->asset('img/logo.svg')) ?>" width="34" height="34" alt="">NimVor</a>
        <button class="nav-toggle" type="button" data-nav-toggle aria-expanded="false" aria-controls="site-nav" aria-label="Menu"><?= $v->icon('menu', 22) ?></button>
        <nav class="nav" id="site-nav" aria-label="Main">
            <a href="/"<?= $path === '/' ? ' aria-current="page"' : '' ?>>Home</a>
            <a href="/features"<?= $v->active('/features') ?>>Features</a>
            <div class="has-menu">
                <button class="nav-link" type="button" aria-expanded="false" aria-haspopup="true">Resources <?= $v->icon('arrow', 14) ?></button>
                <div class="menu">
                    <a href="/resources/documentation"><span class="ico"><?= $v->icon('book', 20) ?></span><span>Documentation<small>Install, administer, extend</small></span></a>
                    <a href="/resources/blog"><span class="ico"><?= $v->icon('pen', 20) ?></span><span>Blog<small>News and how-tos</small></span></a>
                    <a href="/resources/community"><span class="ico"><?= $v->icon('git', 20) ?></span><span>Community &amp; source<small>GitHub, issues, contributing</small></span></a>
                    <a href="/resources/build-services"><span class="ico"><?= $v->icon('tool', 20) ?></span><span>Build services<small>Migrations, installs, custom work</small></span></a>
                    <a href="/resources/sponsors"><span class="ico"><?= $v->icon('heart', 20) ?></span><span>Sponsors<small>Who keeps NimVor free</small></span></a>
                    <a href="/resources/about"><span class="ico"><?= $v->icon('info', 20) ?></span><span>About us<small>The project and its rules</small></span></a>
                </div>
            </div>
            <div class="has-menu">
                <button class="nav-link" type="button" aria-expanded="false" aria-haspopup="true">Support <?= $v->icon('arrow', 14) ?></button>
                <div class="menu">
                    <a href="/support/contact"><span class="ico"><?= $v->icon('chat', 20) ?></span><span>Contact us<small>Write to the project</small></span></a>
                    <a href="/support/privacy"><span class="ico"><?= $v->icon('lock', 20) ?></span><span>Privacy policy<small>What this site stores</small></span></a>
                </div>
            </div>
        </nav>
        <div class="header-cta">
            <?php if ($user !== null): ?>
                <a class="btn btn-ghost btn-sm" href="<?= ($user['role'] ?? '') === 'admin' ? '/admin' : '/account' ?>">Dashboard</a>
                <form method="post" action="/logout" class="inline-form"><?= $v->csrf() ?><button class="btn btn-ghost btn-sm" type="submit">Log out</button></form>
            <?php else: ?>
                <a class="btn btn-ghost btn-sm" href="/login">Log in</a>
                <a class="btn btn-primary btn-sm" href="/resources/documentation#install">Get NimVor</a>
            <?php endif; ?>
        </div>
    </div>
</header>
