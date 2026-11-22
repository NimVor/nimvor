<?php
/**
 * @var \NimVorSite\Core\View $v
 * @var list<array<string, mixed>> $canned
 * @var array<string, string> $errors
 * @var array<string, mixed> $old
 */
$errors ??= [];
$old ??= [];
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Canned replies');
?>
<div class="app-top"><div><h1>Canned replies</h1><p class="muted mb-0">Answers you give often. They appear in the reply box of every ticket; choosing one only fills the text, you can change it before sending.</p></div><a class="btn btn-outline btn-sm" href="/admin/tickets">&larr; Tickets</a></div>
<form class="panel form" method="post" action="/admin/tickets/canned" novalidate data-once>
    <?= $v->csrf() ?><h2 class="mb-0">Add a canned reply</h2>
    <div class="field"><label for="title">Title</label><input class="input" id="title" name="title" value="<?= e($old['title'] ?? '') ?>" maxlength="120" required><?= $v->error($errors, 'title') ?></div>
    <div class="field"><label for="body">Text</label><textarea class="textarea" id="body" name="body" maxlength="5000" rows="5" required><?= e($old['body'] ?? '') ?></textarea><?= $v->error($errors, 'body') ?></div>
    <div><button class="btn btn-primary" type="submit">Add</button></div>
</form>
<?php foreach ($canned as $c): ?>
    <article class="panel">
        <div class="app-top mb-0"><h2 class="mb-0"><?= e($c['title']) ?></h2>
            <form class="inline-form" method="post" action="/admin/tickets/canned/<?= (int) $c['id'] ?>/delete" data-confirm="Delete this canned reply?"><?= $v->csrf() ?><button class="btn btn-danger btn-sm" type="submit">Delete</button></form></div>
        <p class="mt-2 mb-0"><?= nl2br(e($c['body']), false) ?></p>
    </article>
<?php endforeach; ?>
