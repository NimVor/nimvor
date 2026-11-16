<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, list<array<string, mixed>>> $groups */
/** @var int $total */
$v->layout('layouts/site');
$v->set('title', 'Videos');
$v->set('description', 'Video walkthroughs of NimVor: install, websites, email, DNS, SSL, backups, security and moving accounts from other panels.');
?>
<?= $v->partial('partials/page-head', ['title' => 'Videos', 'intro' => 'Short walkthroughs of the panel, recorded on a demo server.', 'crumbs' => [['Resources', '/resources'], ['Videos', '']]]) ?>
<section class="section">
    <div class="container">
        <?php if ($total === 0): ?>
            <div class="card empty"><h3>No videos yet</h3><p>Walkthroughs are on their way. Meanwhile the <a href="/docs">documentation</a> covers every page of the panel.</p></div>
        <?php endif; ?>
        <?php foreach ($groups as $category => $list): ?>
            <h2 class="mt-3"><?= e($category) ?></h2>
            <div class="grid grid-3">
                <?php foreach ($list as $row): ?>
                    <a class="card card-link video-card" href="/resources/videos/<?= e($row['slug']) ?>">
                        <span class="video-thumb<?= $row['poster'] === null ? ' video-thumb-plain' : '' ?>">
                            <?php if ($row['poster'] !== null): ?><img src="/media/videos/<?= e($row['poster']) ?>" alt="" loading="lazy" width="640" height="360"><?php endif; ?>
                            <span class="video-play" aria-hidden="true"><?= $v->icon('play', 26) ?></span>
                            <?php if ($row['duration'] !== ''): ?><span class="video-length"><?= e($row['duration']) ?></span><?php endif; ?>
                        </span>
                        <h3><?= e($row['title']) ?></h3>
                        <?php if ($row['description'] !== ''): ?><p class="muted"><?= e(mb_strimwidth((string) $row['description'], 0, 140, '…')) ?></p><?php endif; ?>
                    </a>
                <?php endforeach; ?>
            </div>
        <?php endforeach; ?>
    </div>
</section>
