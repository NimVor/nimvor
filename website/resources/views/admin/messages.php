<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $messages */
/** @var list<string> $statuses */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Messages');
?>
<div class="app-top"><div><h1>Messages</h1><p class="muted mb-0">Sent from the contact form.</p></div><a class="btn btn-outline btn-sm" href="/admin/reports/messages.csv">Download CSV</a></div>
<?php if ($messages === []): ?><div class="panel empty">No messages yet.</div><?php else: ?>
    <?php foreach ($messages as $m): ?>
        <article class="panel">
            <div class="app-top mb-0"><div><h2 class="mb-0"><?= e($m['subject']) ?></h2><div class="muted"><?= e($m['name']) ?> · <a href="mailto:<?= e($m['email']) ?>"><?= e($m['email']) ?></a> · <?= e(substr((string) $m['created_at'], 0, 16)) ?> UTC</div></div>
                <form class="inline-form" method="post" action="/admin/messages/<?= (int) $m['id'] ?>/status"><?= $v->csrf() ?>
                    <select class="select" name="status" aria-label="Status"><?php foreach ($statuses as $s): ?><option value="<?= e($s) ?>"<?= $m['status'] === $s ? ' selected' : '' ?>><?= e($s) ?></option><?php endforeach; ?></select>
                    <button class="btn btn-outline btn-sm" type="submit">Save</button></form></div>
            <p class="mt-2 mb-0"><?= nl2br(e($m['message']), false) ?></p>
        </article>
    <?php endforeach; ?>
<?php endif; ?>
