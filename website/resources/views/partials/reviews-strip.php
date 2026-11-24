<?php
/**
 * A small "what people say" band for the home page: up to three published reviews, or an invitation to write the first.
 *
 * @var \NimVorSite\Core\View $v
 * @var array{list: list<array<string, mixed>>, summary: array{count: int, average: float}, trustpilot: string} $reviews
 */
$list = $reviews['list'];
$sum = $reviews['summary'];
?>
<section class="section section-sage" id="reviews">
    <div class="container">
        <div class="section-head split-head">
            <div><span class="eyebrow">Reviews</span><h2>What people say about NimVor.</h2></div>
            <?php if ($sum['count'] > 0): ?>
                <p class="rating-sum"><span class="stars" aria-hidden="true"><?= e(str_repeat('★', (int) round($sum['average']))) ?><?= e(str_repeat('☆', 5 - (int) round($sum['average']))) ?></span>
                    <strong><?= e(number_format($sum['average'], 1)) ?></strong> from <?= (int) $sum['count'] ?> review<?= $sum['count'] === 1 ? '' : 's' ?></p>
            <?php endif; ?>
        </div>
        <?php if ($list === []): ?>
            <div class="card empty"><p>No reviews yet. Used NimVor? Tell others what you think.</p>
                <p class="cta-row flush"><a class="btn btn-primary" href="/reviews#write">Write a review</a><?php if ($reviews['trustpilot'] !== ''): ?><a class="btn btn-outline" href="<?= e($reviews['trustpilot']) ?>" target="_blank" rel="noopener noreferrer">Review us on Trustpilot</a><?php endif; ?></p></div>
        <?php else: ?>
            <div class="grid grid-3">
                <?php foreach ($list as $r): ?>
                    <figure class="card review">
                        <div class="stars" role="img" aria-label="<?= (int) $r['rating'] ?> out of 5"><?= e(str_repeat('★', (int) $r['rating'])) ?><span class="off"><?= e(str_repeat('★', 5 - (int) $r['rating'])) ?></span></div>
                        <?php if ($r['title'] !== ''): ?><h3><?= e($r['title']) ?></h3><?php endif; ?>
                        <blockquote><p><?= e(mb_strlen((string) $r['body']) > 220 ? rtrim(mb_substr((string) $r['body'], 0, 220)) . '…' : (string) $r['body']) ?></p></blockquote>
                        <figcaption><strong><?= e($r['name']) ?></strong><?= $r['role'] !== '' ? ', ' . e($r['role']) : '' ?></figcaption>
                    </figure>
                <?php endforeach; ?>
            </div>
            <p class="mt-3"><a href="/reviews">Read all reviews <?= $v->icon('arrow', 14) ?></a> · <a href="/reviews#write">Write a review</a></p>
        <?php endif; ?>
    </div>
</section>
