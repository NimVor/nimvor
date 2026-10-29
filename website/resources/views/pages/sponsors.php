<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $sponsors */
$v->layout('layouts/site');
$v->set('title', 'Sponsors');
$v->set('description', 'The people and companies that keep NimVor free and independent.');
$byTier = ['gold' => [], 'silver' => [], 'supporter' => []];
foreach ($sponsors as $s) {
    $byTier[$s['tier']][] = $s;
}
$labels = ['gold' => 'Gold sponsors', 'silver' => 'Silver sponsors', 'supporter' => 'Supporters'];
?>
<?= $v->partial('partials/page-head', ['title' => 'Sponsors', 'intro' => 'NimVor has no paywall and no hidden tier. Sponsors keep it that way.', 'crumbs' => [['Resources', '/resources'], ['Sponsors', '']]]) ?>
<section class="section">
    <div class="container">
        <?php if ($sponsors === []): ?>
            <div class="card empty"><h3>There are no sponsors yet</h3><p>Be the first: your name and link will appear here.</p></div>
        <?php else: ?>
            <?php foreach ($byTier as $tier => $list): if ($list === []) { continue; } ?>
                <h2 class="mt-3"><?= e($labels[$tier]) ?></h2>
                <div class="grid grid-3">
                    <?php foreach ($list as $s): ?>
                        <a class="card card-link sponsor" href="<?= e($s['url']) ?>" rel="sponsored noopener">
                            <span class="avatar" aria-hidden="true"><?= e(mb_strtoupper(mb_substr(ltrim((string) $s['name'], '@'), 0, 1))) ?></span>
                            <span><h3 class="mb-0"><?= e($s['name']) ?></h3><span class="muted"><?= e(parse_url((string) $s['url'], PHP_URL_HOST) ?: '') ?><?= ($s['source'] ?? '') === 'github' ? ' · GitHub Sponsors' : '' ?></span></span>
                        </a>
                    <?php endforeach; ?>
                </div>
            <?php endforeach; ?>
        <?php endif; ?>
    </div>
</section>
<section class="section section-soft">
    <div class="container split">
        <div>
            <span class="eyebrow">Why sponsor</span>
            <h2>Independent, and staying that way</h2>
            <p class="lead">Sponsorship pays for testing on real servers, security reviews and the time it takes to keep the documentation true.</p>
            <ul class="checks">
                <li><?= $v->icon('check', 18) ?><span>Your name and link on this page.</span></li>
                <li><?= $v->icon('check', 18) ?><span>A say in what gets tested next, through the tier you choose.</span></li>
                <li><?= $v->icon('check', 18) ?><span>No influence on security decisions: those follow the engineering rules.</span></li>
            </ul>
        </div>
        <div class="card">
            <h3>Become a sponsor</h3>
            <p class="muted">Sponsor through GitHub Sponsors and your name appears here automatically, or write to us for another way to contribute and the tier that fits.</p>
            <div class="cta-row"><a class="btn btn-primary" href="https://github.com/sponsors/NimVor" rel="noopener">Sponsor on GitHub</a><a class="btn btn-outline" href="/support/contact">Contact us</a></div>
        </div>
    </div>
</section>
