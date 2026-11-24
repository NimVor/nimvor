<?php
/**
 * @var \NimVorSite\Core\View $v
 * @var list<array<string, mixed>> $reviews
 * @var list<string> $sources
 * @var array<string, string> $errors
 * @var array<string, mixed> $old
 */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Reviews');
?>
<div class="app-top"><div><h1>Reviews</h1><p class="muted mb-0">Shown on the home page and on the Reviews page. A visitor&rsquo;s review waits here until you publish it.</p></div></div>
<?php if ($reviews === []): ?><div class="panel empty">No reviews yet.</div><?php else: ?>
<div class="table-wrap"><table class="data"><thead><tr><th>Review</th><th>Rating</th><th>From</th><th>Status</th><th></th></tr></thead><tbody>
    <?php foreach ($reviews as $r): ?>
        <tr>
            <td><strong><?= e($r['name']) ?></strong><?= $r['role'] !== '' ? ', ' . e($r['role']) : '' ?><?= $r['title'] !== '' ? '<div>' . e($r['title']) . '</div>' : '' ?><div class="muted"><?= e(mb_strlen((string) $r['body']) > 300 ? mb_substr((string) $r['body'], 0, 300) . '…' : (string) $r['body']) ?></div></td>
            <td><?= (int) $r['rating'] ?>/5</td>
            <td><?= e($r['source']) ?><div class="muted"><?= e(substr((string) $r['created_at'], 0, 10)) ?></div></td>
            <td><span class="badge <?= $r['status'] === 'published' ? 'badge-ok' : 'badge-off' ?>"><?= e($r['status']) ?></span></td>
            <td class="cols-actions">
                <?php foreach (['published' => 'Publish', 'hidden' => 'Hide'] as $st => $label): if ($r['status'] === $st) { continue; } ?>
                    <form class="inline-form" method="post" action="/admin/reviews/<?= (int) $r['id'] ?>/status" data-once><?= $v->csrf() ?><input type="hidden" name="status" value="<?= e($st) ?>"><button class="btn btn-outline btn-sm" type="submit"><?= e($label) ?></button></form>
                <?php endforeach; ?>
                <form class="inline-form" method="post" action="/admin/reviews/<?= (int) $r['id'] ?>/delete" data-confirm="Delete this review? This cannot be undone."><?= $v->csrf() ?><button class="btn btn-danger btn-sm" type="submit">Delete</button></form>
            </td>
        </tr>
    <?php endforeach; ?>
</tbody></table></div>
<?php endif; ?>
<form class="panel form" method="post" action="/admin/reviews" novalidate data-once>
    <?= $v->csrf() ?>
    <?php if (!empty($errors)): ?><div class="alert alert-error mb-0" role="alert">Please correct the highlighted fields.</div><?php endif; ?>
    <h2 class="mb-0">Add a review</h2>
    <p class="muted mb-0">For a review copied from Trustpilot or sent to you by a customer, with their permission. It is shown at once. Only add what was really written.</p>
    <div class="field"><label for="name">Name</label><input class="input" id="name" name="name" value="<?= e($old['name'] ?? '') ?>" maxlength="120" required><?= $v->error($errors, 'name') ?></div>
    <div class="field"><label for="role">Role or company (optional)</label><input class="input" id="role" name="role" value="<?= e($old['role'] ?? '') ?>" maxlength="120"><?= $v->error($errors, 'role') ?></div>
    <div class="field"><label for="rating">Rating</label><select class="select" id="rating" name="rating" required><?php foreach ([5, 4, 3, 2, 1] as $n): ?><option value="<?= $n ?>"<?= (string) ($old['rating'] ?? '5') === (string) $n ? ' selected' : '' ?>><?= $n ?> star<?= $n === 1 ? '' : 's' ?></option><?php endforeach; ?></select><?= $v->error($errors, 'rating') ?></div>
    <div class="field"><label for="title">Headline (optional)</label><input class="input" id="title" name="title" value="<?= e($old['title'] ?? '') ?>" maxlength="160"><?= $v->error($errors, 'title') ?></div>
    <div class="field"><label for="body">Review</label><textarea class="textarea" id="body" name="body" maxlength="2000" required><?= e($old['body'] ?? '') ?></textarea><?= $v->error($errors, 'body') ?></div>
    <div class="field"><label for="source">Where it comes from</label><select class="select" id="source" name="source"><option value="trustpilot"<?= ($old['source'] ?? '') === 'trustpilot' ? ' selected' : '' ?>>Trustpilot</option><option value="manual"<?= ($old['source'] ?? '') === 'manual' ? ' selected' : '' ?>>A customer, directly</option></select><?= $v->error($errors, 'source') ?></div>
    <div class="field"><label for="url">Link to the original (optional)</label><input class="input" id="url" name="url" value="<?= e($old['url'] ?? '') ?>" maxlength="240" placeholder="https://" inputmode="url"><?= $v->error($errors, 'url') ?></div>
    <div><button class="btn btn-primary" type="submit">Add review</button></div>
</form>
