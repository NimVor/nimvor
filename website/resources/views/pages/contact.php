<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/site');
$v->set('title', 'Contact us');
$v->set('description', 'Write to the NimVor project.');
?>
<?= $v->partial('partials/page-head', ['title' => 'Contact us', 'intro' => 'Questions, sponsorship, press. For a bug, a GitHub issue is faster.', 'crumbs' => [['Support', '/support'], ['Contact us', '']]]) ?>
<section class="section">
    <div class="container split">
        <form class="card form" method="post" action="/support/contact" novalidate data-once>
            <?= $v->csrf() ?>
            <?php if (!empty($errors)): ?><div class="alert alert-error" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
            <div class="field"><label for="name">Your name</label><input class="input" id="name" name="name" value="<?= e($old['name'] ?? '') ?>" maxlength="120" autocomplete="name" required><?= $v->error($errors, 'name') ?></div>
            <div class="field"><label for="email">Email</label><input class="input" id="email" type="email" name="email" value="<?= e($old['email'] ?? '') ?>" maxlength="190" autocomplete="email" required><?= $v->error($errors, 'email') ?></div>
            <div class="field"><label for="subject">Subject</label><input class="input" id="subject" name="subject" value="<?= e($old['subject'] ?? '') ?>" maxlength="200" required><?= $v->error($errors, 'subject') ?></div>
            <div class="field"><label for="message">Message</label><textarea class="textarea" id="message" name="message" maxlength="5000" required><?= e($old['message'] ?? '') ?></textarea><?= $v->error($errors, 'message') ?></div>
            <div class="hp" aria-hidden="true"><label>Leave this empty<input name="website" tabindex="-1" autocomplete="off"></label></div>
            <button class="btn btn-primary" type="submit">Send message</button>
        </form>
        <div class="prose">
            <h2 class="mt-0">Other ways</h2>
            <ul>
                <li>Bugs and feature requests: <a href="https://github.com/NimVor/nimvor/issues" rel="noopener">GitHub issues</a>.</li>
                <li>Vulnerabilities: <a href="https://github.com/NimVor/nimvor/security/policy" rel="noopener">private reporting on GitHub</a>.</li>
                <li>Paid work: <a href="/resources/build-services">Build services</a>.</li>
            </ul>
            <p class="muted">We use your name and email only to answer you. See the <a href="/support/privacy">privacy policy</a>.</p>
        </div>
    </div>
</section>
