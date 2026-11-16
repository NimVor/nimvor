<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $video */
/** @var list<array<string, mixed>> $more */
$v->layout('layouts/site');
$v->set('title', (string) $video['title']);
$v->set('description', $video['description'] !== '' ? mb_strimwidth((string) $video['description'], 0, 160, '…') : 'A NimVor video walkthrough.');
?>
<?= $v->partial('partials/page-head', ['title' => (string) $video['title'], 'intro' => (string) $video['category'] . ($video['duration'] !== '' ? ' · ' . $video['duration'] : ''), 'crumbs' => [['Resources', '/resources'], ['Videos', '/resources/videos'], [(string) $video['title'], '']]]) ?>
<section class="section">
    <div class="container video-page">
        <div class="video-frame">
            <?php if ($video['source'] === 'youtube'): ?>
                <iframe src="<?= e(\NimVorSite\Support\YouTube::embedUrl((string) $video['youtube_id'])) ?>" title="<?= e($video['title']) ?>" loading="lazy" allow="fullscreen; picture-in-picture" allowfullscreen referrerpolicy="strict-origin-when-cross-origin"></iframe>
            <?php else: ?>
                <video class="tour-video" controls preload="metadata" playsinline<?= $video['poster'] !== null ? ' poster="/media/videos/' . e($video['poster']) . '"' : '' ?>>
                    <source src="/media/videos/<?= e($video['file']) ?>" type="<?= e($video['mime']) ?>">
                    Your browser cannot play this video.
                </video>
            <?php endif; ?>
        </div>
        <?php if ($video['description'] !== ''): ?>
            <?php foreach (preg_split('/\n{2,}/', (string) $video['description']) ?: [] as $para): ?><p class="lead"><?= nl2br(e($para)) ?></p><?php endforeach; ?>
        <?php endif; ?>
        <?php if ($video['source'] === 'youtube'): ?>
            <p class="muted">This video is played from YouTube. YouTube only learns that you opened it once you press play. <a href="<?= e(\NimVorSite\Support\YouTube::watchUrl((string) $video['youtube_id'])) ?>" rel="noopener">Watch it on YouTube</a>.</p>
        <?php endif; ?>
        <?php if ($more !== []): ?>
            <h2 class="mt-3">More videos</h2>
            <div class="grid grid-4">
                <?php foreach ($more as $row): ?>
                    <a class="card card-link" href="/resources/videos/<?= e($row['slug']) ?>"><span class="badge"><?= e($row['category']) ?></span><h3><?= e($row['title']) ?></h3><?php if ($row['duration'] !== ''): ?><p class="muted"><?= e($row['duration']) ?></p><?php endif; ?></a>
                <?php endforeach; ?>
            </div>
        <?php endif; ?>
        <p><a class="btn btn-outline" href="/resources/videos">All videos</a></p>
    </div>
</section>
