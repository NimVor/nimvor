<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<string> $categories */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$errors ??= [];
$v->layout('layouts/app');
$v->share('area', 'account');
$v->set('title', 'New ticket');
$labels = ['general' => 'A general question', 'support' => 'Help with NimVor', 'sales' => 'Paid work and build services', 'sponsorship' => 'Sponsorship', 'press' => 'Press', 'security' => 'A security problem', 'other' => 'Something else'];
?>
<div class="app-top"><div><h1>New ticket</h1><p class="muted mb-0">For bugs and feature requests a <a href="https://github.com/NimVor/nimvor/issues" rel="noopener">GitHub issue</a> is faster.</p></div></div>
<form class="panel form" method="post" action="/account/tickets/new" novalidate data-once>
    <?= $v->csrf() ?>
    <div class="field"><label for="category">What is it about?</label><select class="select" id="category" name="category"><?php foreach ($categories as $c): ?><option value="<?= e($c) ?>"<?= ($old['category'] ?? 'support') === $c ? ' selected' : '' ?>><?= e($labels[$c] ?? $c) ?></option><?php endforeach; ?></select><?= $v->error($errors, 'category') ?></div>
    <div class="field"><label for="subject">Subject</label><input class="input" id="subject" name="subject" value="<?= e($old['subject'] ?? '') ?>" maxlength="200" required><?= $v->error($errors, 'subject') ?></div>
    <div class="field"><label for="message">Message</label><textarea class="textarea" id="message" name="message" maxlength="5000" required><?= e($old['message'] ?? '') ?></textarea><?= $v->error($errors, 'message') ?></div>
    <div><button class="btn btn-primary" type="submit">Open the ticket</button></div>
</form>
