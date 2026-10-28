<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed>|null $post */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', $post === null ? 'New post' : 'Edit post');
$action = $post === null ? '/admin/posts/new' : '/admin/posts/' . (int) $post['id'] . '/edit';
?>
<div class="app-top"><div><h1><?= $post === null ? 'New post' : 'Edit post' ?></h1></div><a class="btn btn-outline btn-sm" href="/admin/posts">Back</a></div>
<form class="panel form" method="post" action="<?= e($action) ?>" novalidate data-once>
    <?= $v->csrf() ?>
    <?php if (!empty($errors)): ?><div class="alert alert-error mb-0" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
    <div class="field"><label for="title">Title</label><input class="input" id="title" name="title" value="<?= e($old['title'] ?? '') ?>" maxlength="200" required><?= $v->error($errors, 'title') ?></div>
    <div class="field"><label for="slug">Address</label><input class="input" id="slug" name="slug" value="<?= e($old['slug'] ?? '') ?>" maxlength="100"><div class="hint">Leave empty to make it from the title. Letters, digits and hyphens.</div><?= $v->error($errors, 'slug') ?></div>
    <div class="field"><label for="excerpt">Summary</label><input class="input" id="excerpt" name="excerpt" value="<?= e($old['excerpt'] ?? '') ?>" maxlength="400"><?= $v->error($errors, 'excerpt') ?></div>
    <div class="field"><label for="body">Text</label><textarea class="textarea" id="body" name="body" rows="16" required><?= e($old['body'] ?? '') ?></textarea>
        <div class="hint">Blank line between paragraphs. <code>## Heading</code>, <code>- list item</code>, <code>**bold**</code>, <code>`code`</code> and <code>[text](https://…)</code> work. Everything else is shown as typed.</div><?= $v->error($errors, 'body') ?></div>
    <div class="field"><label for="status">Status</label>
        <select class="select" id="status" name="status"><option value="draft"<?= ($old['status'] ?? 'draft') === 'draft' ? ' selected' : '' ?>>Draft</option><option value="published"<?= ($old['status'] ?? '') === 'published' ? ' selected' : '' ?>>Published</option></select><?= $v->error($errors, 'status') ?></div>
    <div><button class="btn btn-primary" type="submit">Save post</button></div>
</form>
