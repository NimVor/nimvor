<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, string> $services */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/site');
$v->set('title', 'Build services');
$v->set('description', 'Paid help from people who know NimVor: migrations, hardened installs, security reviews and custom features.');
$icons = ['migration' => 'server', 'install' => 'shield', 'custom' => 'code', 'review' => 'file', 'support' => 'users'];
?>
<?= $v->partial('partials/page-head', ['title' => 'Build services', 'intro' => 'NimVor is free. If you would rather have someone do the work, or need something built, ask us.', 'crumbs' => [['Resources', '/resources'], ['Build services', '']]]) ?>
<section class="section">
    <div class="container">
        <div class="grid grid-3">
            <?php foreach ($services as $key => $label): ?>
                <div class="card"><div class="card-icon"><?= $v->icon($icons[$key] ?? 'tool') ?></div><h3><?= e($label) ?></h3>
                    <p class="muted"><?= e([
                        'migration' => 'Move websites, databases and mail from an existing panel or a bare server, with a plan to switch over and to go back.',
                        'install' => 'A new server installed, configured and hardened to your needs, with backups set up and tested.',
                        'custom' => 'A feature, an integration or a plugin built for you. It is written to the same rules as NimVor itself.',
                        'review' => 'A look at an existing server or NimVor installation, with a written list of what to fix first.',
                        'support' => 'A named contact for questions and incidents after launch.',
                    ][$key] ?? '') ?></p></div>
            <?php endforeach; ?>
        </div>
    </div>
</section>
<section class="section section-soft" id="request">
    <div class="container split">
        <div>
            <span class="eyebrow">How it works</span>
            <h2>Tell us what you need</h2>
            <div class="steps mt-3">
                <div class="step"><div><h3>Send a request</h3><p class="muted">Describe your servers and what you want done.</p></div></div>
                <div class="step"><div><h3>We reply by email</h3><p class="muted">Usually with questions, then a scope and a quote for the work.</p></div></div>
                <div class="step"><div><h3>You decide</h3><p class="muted">There is no obligation. Work starts only when you accept the quote.</p></div></div>
            </div>
            <p class="muted mt-3">Prices depend on the work and are quoted per project. <?= ($user ?? null) === null ? 'Have an account? <a href="/login?next=%2Fresources%2Fbuild-services">Log in</a> to track your requests.' : 'You can follow this request under <a href="/account/requests">your account</a>.' ?></p>
        </div>
        <form class="card form" method="post" action="/resources/build-services" novalidate data-once>
            <?= $v->csrf() ?>
            <?php if (!empty($errors)): ?><div class="alert alert-error" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
            <div class="field"><label for="name">Your name</label><input class="input" id="name" name="name" value="<?= e($old['name'] ?? '') ?>" maxlength="120" autocomplete="name" required><?= $v->error($errors, 'name') ?></div>
            <div class="field"><label for="email">Email</label><input class="input" id="email" type="email" name="email" value="<?= e($old['email'] ?? '') ?>" maxlength="190" autocomplete="email" required><?= $v->error($errors, 'email') ?></div>
            <div class="field"><label for="service">What do you need?</label>
                <select class="select" id="service" name="service" required>
                    <option value="">Choose a service</option>
                    <?php foreach ($services as $key => $label): ?><option value="<?= e($key) ?>"<?= ($old['service'] ?? '') === $key ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                </select><?= $v->error($errors, 'service') ?></div>
            <div class="field"><label for="details">Details</label><textarea class="textarea" id="details" name="details" maxlength="5000" required><?= e($old['details'] ?? '') ?></textarea><div class="hint">Operating system, number of sites, deadlines: anything that helps us scope it.</div><?= $v->error($errors, 'details') ?></div>
            <div class="hp" aria-hidden="true"><label>Leave this empty<input name="website" tabindex="-1" autocomplete="off"></label></div>
            <button class="btn btn-primary" type="submit">Send request</button>
        </form>
    </div>
</section>
