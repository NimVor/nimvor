<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Support');
?>
<?= $v->partial('partials/page-head', ['title' => 'Support', 'intro' => 'Where to go depending on what you need.', 'crumbs' => [['Support', '']]]) ?>
<section class="section">
    <div class="container grid grid-3">
        <a class="card card-link" href="/resources/documentation"><div class="card-icon"><?= $v->icon('book') ?></div><h3>Read the documentation</h3><p class="muted">Installation, administration and troubleshooting guides.</p></a>
        <a class="card card-link" href="https://github.com/NimVor/nimvor/issues" rel="noopener"><div class="card-icon"><?= $v->icon('git') ?></div><h3>Report a bug</h3><p class="muted">Open an issue on GitHub. This is the fastest way to get a fix.</p></a>
        <a class="card card-link" href="/support/contact"><div class="card-icon"><?= $v->icon('chat') ?></div><h3>Contact us</h3><p class="muted">For anything else: sponsorship, press, or a question that is not a bug.</p></a>
        <a class="card card-link" href="https://github.com/NimVor/nimvor/security/policy" rel="noopener"><div class="card-icon"><?= $v->icon('shield') ?></div><h3>Security problem</h3><p class="muted">Report a vulnerability privately through GitHub. Please do not post it publicly.</p></a>
        <a class="card card-link" href="/resources/build-services"><div class="card-icon"><?= $v->icon('tool') ?></div><h3>Paid help</h3><p class="muted">Migrations, installs, reviews and custom work.</p></a>
        <a class="card card-link" href="/support/privacy"><div class="card-icon"><?= $v->icon('lock') ?></div><h3>Privacy policy</h3><p class="muted">What this website stores about you.</p></a>
    </div>
</section>
