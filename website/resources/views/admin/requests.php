<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $requests */
/** @var list<string> $statuses */
/** @var array<string, string> $services */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Build requests');
?>
<div class="app-top"><div><h1>Build requests</h1><p class="muted mb-0">Sent from the build-services form.</p></div><a class="btn btn-outline btn-sm" href="/admin/reports/requests.csv">Download CSV</a></div>
<?php if ($requests === []): ?><div class="panel empty">No requests yet.</div><?php else: ?>
    <?php foreach ($requests as $r): ?>
        <article class="panel">
            <div class="app-top mb-0"><div><h2 class="mb-0"><?= e($services[$r['service']] ?? $r['service']) ?></h2><div class="muted"><?= e($r['name']) ?> · <a href="mailto:<?= e($r['email']) ?>"><?= e($r['email']) ?></a> · <?= e(substr((string) $r['created_at'], 0, 16)) ?> UTC<?= $r['user_id'] !== null ? ' · account #' . (int) $r['user_id'] : '' ?></div></div>
                <form class="inline-form" method="post" action="/admin/requests/<?= (int) $r['id'] ?>/status"><?= $v->csrf() ?>
                    <select class="select" name="status" aria-label="Status"><?php foreach ($statuses as $s): ?><option value="<?= e($s) ?>"<?= $r['status'] === $s ? ' selected' : '' ?>><?= e(str_replace('_', ' ', $s)) ?></option><?php endforeach; ?></select>
                    <button class="btn btn-outline btn-sm" type="submit">Save</button></form></div>
            <p class="mt-2 mb-0"><?= nl2br(e($r['details']), false) ?></p>
        </article>
    <?php endforeach; ?>
<?php endif; ?>
