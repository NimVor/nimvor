<?php
/** @var \NimVorSite\Core\View $v */
$v->layout('layouts/site');
$v->set('title', 'Community & source');
$v->set('description', 'NimVor is open source under the MIT licence. Read the code, report a problem, or contribute.');
$gh = 'https://github.com/NimVor/nimvor';
?>
<?= $v->partial('partials/page-head', ['title' => 'Community & source', 'intro' => 'NimVor is built in the open. Everything, from the installer to the agent, is on GitHub.', 'crumbs' => [['Resources', '/resources'], ['Community & source', '']]]) ?>
<section class="section">
    <div class="container grid grid-3">
        <a class="card card-link" href="<?= e($gh) ?>" rel="noopener"><div class="card-icon"><?= $v->icon('git') ?></div><h3>Source code</h3><p class="muted">The panel (PHP), the agent (Go), the installer and the documentation, under the MIT licence.</p></a>
        <a class="card card-link" href="<?= e($gh) ?>/issues" rel="noopener"><div class="card-icon"><?= $v->icon('chat') ?></div><h3>Report a problem</h3><p class="muted">Open an issue with the version, your system and what you expected. Search first: it may be known.</p></a>
        <a class="card card-link" href="<?= e($gh) ?>/pulls" rel="noopener"><div class="card-icon"><?= $v->icon('code') ?></div><h3>Contribute</h3><p class="muted">Pull requests are welcome. Keep each change small, with a test, and describe how it is validated and rolled back.</p></a>
        <a class="card card-link" href="<?= e($gh) ?>/blob/main/CONTRIBUTING.md" rel="noopener"><div class="card-icon"><?= $v->icon('book') ?></div><h3>Contributing guide</h3><p class="muted">The engineering rules every change follows, and how to build and test locally.</p></a>
        <a class="card card-link" href="<?= e($gh) ?>/blob/main/CODE_OF_CONDUCT.md" rel="noopener"><div class="card-icon"><?= $v->icon('users') ?></div><h3>Code of conduct</h3><p class="muted">How we treat each other in the project.</p></a>
        <a class="card card-link" href="<?= e($gh) ?>/security/policy" rel="noopener"><div class="card-icon"><?= $v->icon('shield') ?></div><h3>Security reports</h3><p class="muted">Do not open a public issue for a vulnerability. Use GitHub's private reporting.</p></a>
    </div>
</section>
<section class="section section-soft">
    <div class="container narrow prose">
        <h2 class="mt-0">Before you contribute</h2>
        <ul>
            <li>NimVor is original work. Do not copy code, interface or wording from other control panels.</li>
            <li>A new capability is a new named agent action, never a general way to run commands.</li>
            <li>Every host change is detected, backed up, validated, applied, verified and rolled back on failure.</li>
        </ul>
        <p>Want help from the people who build NimVor, rather than the community? See <a href="/resources/build-services">Build services</a>.</p>
    </div>
</section>
