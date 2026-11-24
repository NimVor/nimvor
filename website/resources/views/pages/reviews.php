<?php
/**
 * @var \NimVorSite\Core\View $v
 * @var list<array<string, mixed>> $list
 * @var array{count: int, average: float} $summary
 * @var string $trustpilot
 * @var array<string, string> $errors
 * @var array<string, mixed> $old
 */
$v->layout('layouts/site');
$v->set('title', 'Reviews');
$v->set('description', 'What people say about NimVor, and how to review it.');
?>
<?= $v->partial('partials/page-head', ['title' => 'Reviews', 'intro' => 'What people who use NimVor say about it.', 'crumbs' => [['Resources', '/resources'], ['Reviews', '']]]) ?>
<section class="section">
    <div class="container">
        <?php if ($summary['count'] > 0): ?>
            <p class="rating-sum lead"><span class="stars" aria-hidden="true"><?= e(str_repeat('★', (int) round($summary['average']))) ?><?= e(str_repeat('☆', 5 - (int) round($summary['average']))) ?></span>
                <strong><?= e(number_format($summary['average'], 1)) ?></strong> out of 5, from <?= (int) $summary['count'] ?> review<?= $summary['count'] === 1 ? '' : 's' ?></p>
        <?php endif; ?>
        <?php if ($list === []): ?>
            <div class="card empty">No reviews have been published yet. Be the first to write one.</div>
        <?php else: ?>
            <div class="grid grid-3">
                <?php foreach ($list as $r): ?>
                    <figure class="card review">
                        <div class="stars" role="img" aria-label="<?= (int) $r['rating'] ?> out of 5"><?= e(str_repeat('★', (int) $r['rating'])) ?><span class="off"><?= e(str_repeat('★', 5 - (int) $r['rating'])) ?></span></div>
                        <?php if ($r['title'] !== ''): ?><h3><?= e($r['title']) ?></h3><?php endif; ?>
                        <blockquote><p><?= nl2br(e((string) $r['body'])) ?></p></blockquote>
                        <figcaption><strong><?= e($r['name']) ?></strong><?= $r['role'] !== '' ? ', ' . e($r['role']) : '' ?>
                            <span class="muted"> · <?= e(substr((string) $r['created_at'], 0, 10)) ?><?= $r['source'] === 'trustpilot' ? ' · Trustpilot' : '' ?></span>
                            <?php if ($r['source'] === 'trustpilot' && $r['url'] !== ''): ?> <a href="<?= e($r['url']) ?>" target="_blank" rel="noopener noreferrer">View</a><?php endif; ?></figcaption>
                    </figure>
                <?php endforeach; ?>
            </div>
        <?php endif; ?>
    </div>
</section>
<section class="section section-soft" id="write">
    <div class="container split">
        <div>
            <span class="eyebrow">Your turn</span>
            <h2>Review NimVor</h2>
            <p class="lead">Tell others what worked and what did not. We read every review before it is shown, and we never edit what you wrote.</p>
            <?php if ($trustpilot !== ''): ?><p><a class="btn btn-outline" href="<?= e($trustpilot) ?>" target="_blank" rel="noopener noreferrer"><?= $v->icon('star', 18) ?>Review us on Trustpilot</a></p><?php endif; ?>
        </div>
        <form class="card form" method="post" action="/reviews" novalidate data-once>
            <?= $v->csrf() ?>
            <?php if (!empty($errors)): ?><div class="alert alert-error" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
            <div class="field"><label for="name">Your name</label><input class="input" id="name" name="name" value="<?= e($old['name'] ?? '') ?>" maxlength="120" autocomplete="name" required><?= $v->error($errors, 'name') ?></div>
            <div class="field"><label for="role">Your role or company <span class="muted">(optional)</span></label><input class="input" id="role" name="role" value="<?= e($old['role'] ?? '') ?>" maxlength="120"><?= $v->error($errors, 'role') ?></div>
            <div class="field"><label for="rating">Rating</label>
                <select class="select" id="rating" name="rating" required>
                    <option value="">Choose</option>
                    <?php foreach ([5 => '5 – excellent', 4 => '4 – good', 3 => '3 – fine', 2 => '2 – poor', 1 => '1 – bad'] as $n => $label): ?><option value="<?= $n ?>"<?= (string) ($old['rating'] ?? '') === (string) $n ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                </select><?= $v->error($errors, 'rating') ?></div>
            <div class="field"><label for="title">Headline <span class="muted">(optional)</span></label><input class="input" id="title" name="title" value="<?= e($old['title'] ?? '') ?>" maxlength="160"><?= $v->error($errors, 'title') ?></div>
            <div class="field"><label for="body">Your review</label><textarea class="textarea" id="body" name="body" maxlength="2000" required><?= e($old['body'] ?? '') ?></textarea><?= $v->error($errors, 'body') ?></div>
            <div class="hp" aria-hidden="true"><label>Leave this empty<input name="website" tabindex="-1" autocomplete="off"></label></div>
            <?= $v->partial('partials/captcha', ['errors' => $errors ?? []]) ?>
            <button class="btn btn-primary" type="submit">Send review</button>
        </form>
    </div>
</section>
