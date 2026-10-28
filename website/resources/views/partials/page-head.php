<?php /** @var string $title */ /** @var string $intro */ /** @var list<array{0:string,1:string}> $crumbs */ ?>
<section class="page-head">
    <div class="container">
        <?php if (!empty($crumbs)): ?>
            <div class="crumbs"><a href="/">Home</a><?php foreach ($crumbs as [$label, $href]): ?> / <?= $href === '' ? e($label) : '<a href="' . e($href) . '">' . e($label) . '</a>' ?><?php endforeach; ?></div>
        <?php endif; ?>
        <h1><?= e($title) ?></h1>
        <?php if (!empty($intro)): ?><p><?= e($intro) ?></p><?php endif; ?>
    </div>
</section>
