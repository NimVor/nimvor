<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $video */
/** @var list<string> $categories */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Edit video');
$published = !empty($errors) ? ($old['published'] ?? '') === '1' : (int) $video['published'] === 1;
?>
<div class="app-top"><div><h1>Edit video</h1><p class="muted mb-0"><?= $video['source'] === 'youtube' ? 'On YouTube (' . e($video['youtube_id']) . ')' : 'Uploaded file, ' . e(number_format(((int) $video['size_bytes']) / 1048576, 1)) . ' MB. To change the file, delete this video and add it again.' ?></p></div><a class="btn btn-outline btn-sm" href="/admin/videos">Back</a></div>
<form class="panel form" method="post" action="/admin/videos/<?= (int) $video['id'] ?>/edit" enctype="multipart/form-data" novalidate data-once>
    <?= $v->csrf() ?>
    <?php if (!empty($errors)): ?><div class="alert alert-error mb-0" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
    <div class="field"><label for="title">Title</label><input class="input" id="title" name="title" value="<?= e($old['title'] ?? '') ?>" maxlength="160" required><?= $v->error($errors, 'title') ?></div>
    <div class="field"><label for="description">Description</label><textarea class="textarea" id="description" name="description" rows="4" maxlength="2000"><?= e($old['description'] ?? '') ?></textarea><?= $v->error($errors, 'description') ?></div>
    <div class="field"><label for="category">Category</label><input class="input" id="category" name="category" value="<?= e($old['category'] ?? '') ?>" maxlength="40" list="video-categories" required>
        <datalist id="video-categories"><?php foreach ($categories as $c): ?><option value="<?= e($c) ?>"><?php endforeach; ?></datalist><?= $v->error($errors, 'category') ?></div>
    <div class="field"><label for="duration">Length</label><input class="input" id="duration" name="duration" value="<?= e($old['duration'] ?? '') ?>" maxlength="8" placeholder="4:30"><?= $v->error($errors, 'duration') ?></div>
    <div class="field"><label for="sort_order">Order inside its category</label><input class="input" id="sort_order" name="sort_order" value="<?= e($old['sort_order'] ?? '100') ?>" maxlength="4" inputmode="numeric"><?= $v->error($errors, 'sort_order') ?></div>
    <div class="field"><label for="slug">Address</label><input class="input" id="slug" name="slug" value="<?= e($old['slug'] ?? '') ?>" maxlength="100"><?= $v->error($errors, 'slug') ?></div>
    <?php if ($video['source'] === 'upload'): ?>
        <div class="field"><label for="poster">New cover picture</label><input class="input" id="poster" name="poster" type="file" accept="image/jpeg,image/png,image/webp"><?= $v->error($errors, 'poster') ?>
            <?php if ($video['poster'] !== null): ?><label class="check"><input type="checkbox" name="remove_poster" value="1"> Remove the current cover picture</label><?php endif; ?></div>
    <?php endif; ?>
    <label class="check"><input type="checkbox" name="published" value="1"<?= $published ? ' checked' : '' ?>> Show on the Videos page</label>
    <div><button class="btn btn-primary" type="submit">Save video</button></div>
</form>
