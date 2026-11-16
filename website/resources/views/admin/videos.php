<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $videos */
/** @var list<string> $categories */
/** @var int $maxMb */
/** @var string $phpMax */
/** @var array<string, string> $errors */
/** @var array<string, mixed> $old */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Videos');
$source = $old['source'] ?? 'youtube';
?>
<div class="app-top"><div><h1>Videos</h1><p class="muted mb-0">Shown on the public Videos page. Paste a YouTube link, or upload a file to play from this site.</p></div></div>
<form class="panel form" method="post" action="/admin/videos" enctype="multipart/form-data" novalidate data-once>
    <?= $v->csrf() ?><h2 class="mb-0">Add a video</h2>
    <?php if (!empty($errors)): ?><div class="alert alert-error mb-0" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
    <div class="field"><label for="title">Title</label><input class="input" id="title" name="title" value="<?= e($old['title'] ?? '') ?>" maxlength="160" required><?= $v->error($errors, 'title') ?></div>
    <div class="field"><label for="description">Description <span class="muted">(optional)</span></label><textarea class="textarea" id="description" name="description" rows="4" maxlength="2000"><?= e($old['description'] ?? '') ?></textarea><?= $v->error($errors, 'description') ?></div>
    <div class="field"><label for="category">Category</label><input class="input" id="category" name="category" value="<?= e($old['category'] ?? 'Getting started') ?>" maxlength="40" list="video-categories" required>
        <datalist id="video-categories"><?php foreach ($categories as $c): ?><option value="<?= e($c) ?>"><?php endforeach; ?></datalist><?= $v->error($errors, 'category') ?></div>
    <fieldset class="field"><legend>Where is the video?</legend>
        <label class="check"><input type="radio" name="source" value="youtube" data-video-source<?= $source === 'youtube' ? ' checked' : '' ?>> On YouTube</label>
        <label class="check"><input type="radio" name="source" value="upload" data-video-source<?= $source === 'upload' ? ' checked' : '' ?>> Upload a file</label><?= $v->error($errors, 'source') ?></fieldset>
    <div class="field" data-video-pane="youtube"><label for="youtube_url">YouTube link</label><input class="input" id="youtube_url" name="youtube_url" value="<?= e($old['youtube_url'] ?? '') ?>" maxlength="200" placeholder="https://www.youtube.com/watch?v=…"><div class="hint">Watch, youtu.be, embed, shorts and live links all work. The video is played with youtube-nocookie.com.</div><?= $v->error($errors, 'youtube_url') ?></div>
    <div data-video-pane="upload">
        <div class="field"><label for="video">Video file</label><input class="input" id="video" name="video" type="file" accept="video/mp4,video/webm"><div class="hint">MP4 (H.264 and AAC plays everywhere) or WebM, up to <?= (int) $maxMb ?> MB here. PHP currently accepts <?= e($phpMax) ?> per upload: raise <code>upload_max_filesize</code> and <code>post_max_size</code> for larger files.</div><?= $v->error($errors, 'video') ?></div>
        <div class="field"><label for="poster">Cover picture <span class="muted">(optional)</span></label><input class="input" id="poster" name="poster" type="file" accept="image/jpeg,image/png,image/webp"><div class="hint">JPEG, PNG or WebP up to 2 MB, 16:9 works best. Without one the page shows a plain tile.</div><?= $v->error($errors, 'poster') ?></div>
    </div>
    <div class="field"><label for="duration">Length <span class="muted">(optional)</span></label><input class="input" id="duration" name="duration" value="<?= e($old['duration'] ?? '') ?>" maxlength="8" placeholder="4:30"><?= $v->error($errors, 'duration') ?></div>
    <div class="field"><label for="sort_order">Order inside its category</label><input class="input" id="sort_order" name="sort_order" value="<?= e($old['sort_order'] ?? '100') ?>" maxlength="4" inputmode="numeric"><div class="hint">Smaller numbers come first.</div><?= $v->error($errors, 'sort_order') ?></div>
    <div class="field"><label for="slug">Address <span class="muted">(optional)</span></label><input class="input" id="slug" name="slug" value="<?= e($old['slug'] ?? '') ?>" maxlength="100"><div class="hint">Made from the title when empty.</div><?= $v->error($errors, 'slug') ?></div>
    <label class="check"><input type="checkbox" name="published" value="1"<?= empty($errors) || ($old['published'] ?? '') === '1' ? ' checked' : '' ?>> Show on the Videos page now</label>
    <div><button class="btn btn-primary" type="submit">Add video</button></div>
</form>
<?php if ($videos !== []): ?>
<div class="table-wrap"><table class="data"><thead><tr><th>Title</th><th>Category</th><th>Source</th><th>Status</th><th></th></tr></thead><tbody>
    <?php foreach ($videos as $r): ?>
        <tr><td><?= e($r['title']) ?><?= $r['duration'] !== '' ? ' <span class="muted">' . e($r['duration']) . '</span>' : '' ?></td><td><?= e($r['category']) ?></td>
            <td><?= $r['source'] === 'youtube' ? 'YouTube' : 'File · ' . e(number_format(((int) $r['size_bytes']) / 1048576, 1)) . ' MB' ?></td>
            <td><?= (int) $r['published'] === 1 ? '<span class="badge badge-ok">Published</span>' : '<span class="badge badge-off">Draft</span>' ?></td>
            <td class="cols-actions">
                <a class="btn btn-outline btn-sm" href="/resources/videos/<?= e($r['slug']) ?>"<?= (int) $r['published'] === 1 ? '' : ' hidden' ?>>View</a>
                <a class="btn btn-outline btn-sm" href="/admin/videos/<?= (int) $r['id'] ?>/edit">Edit</a>
                <form class="inline-form" method="post" action="/admin/videos/<?= (int) $r['id'] ?>/publish"><?= $v->csrf() ?><button class="btn btn-outline btn-sm" type="submit"><?= (int) $r['published'] === 1 ? 'Hide' : 'Publish' ?></button></form>
                <form class="inline-form" method="post" action="/admin/videos/<?= (int) $r['id'] ?>/delete" data-confirm="Delete this video<?= $r['source'] === 'upload' ? ' and its file' : '' ?>?"><?= $v->csrf() ?><button class="btn btn-danger btn-sm" type="submit">Delete</button></form></td></tr>
    <?php endforeach; ?>
</tbody></table></div>
<?php endif; ?>
