<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $posts */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Blog');
?>
<div class="app-top"><div><h1>Blog</h1><p class="muted mb-0">Posts appear on the public blog once they are published.</p></div><a class="btn btn-primary" href="/admin/posts/new">New post</a></div>
<?php if ($posts === []): ?><div class="panel empty">No posts yet.</div><?php else: ?>
<div class="table-wrap"><table class="data"><thead><tr><th>Title</th><th>Status</th><th>Published</th><th>Updated</th><th></th></tr></thead><tbody>
    <?php foreach ($posts as $p): ?>
        <tr><td><?= e($p['title']) ?><div class="muted">/resources/blog/<?= e($p['slug']) ?></div></td>
            <td><span class="badge <?= $p['status'] === 'published' ? 'badge-ok' : 'badge-off' ?>"><?= e($p['status']) ?></span></td>
            <td><?= e($p['published_at'] === null ? '—' : substr((string) $p['published_at'], 0, 10)) ?></td><td><?= e(substr((string) $p['updated_at'], 0, 10)) ?></td>
            <td class="cols-actions"><a class="btn btn-outline btn-sm" href="/admin/posts/<?= (int) $p['id'] ?>/edit">Edit</a>
                <form class="inline-form" method="post" action="/admin/posts/<?= (int) $p['id'] ?>/delete" data-confirm="Delete this post? This cannot be undone."><?= $v->csrf() ?><button class="btn btn-danger btn-sm" type="submit">Delete</button></form></td></tr>
    <?php endforeach; ?>
</tbody></table></div>
<?php endif; ?>
