<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $messages */
$v->layout('layouts/app');
$v->share('area', 'account');
$v->set('title', 'Messages');
?>
<div class="app-top"><div><h1>Messages</h1><p class="muted mb-0">Messages you sent while signed in.</p></div><a class="btn btn-primary" href="/support/contact">New message</a></div>
<?php if ($messages === []): ?>
    <div class="panel empty">Nothing here yet.</div>
<?php else: ?>
    <div class="table-wrap"><table class="data"><thead><tr><th>Subject</th><th>Status</th><th>Sent</th></tr></thead><tbody>
        <?php foreach ($messages as $m): ?>
            <tr><td><?= e($m['subject']) ?></td><td><span class="badge <?= $m['status'] === 'new' ? 'badge-new' : 'badge-off' ?>"><?= e($m['status']) ?></span></td><td><?= e(substr((string) $m['created_at'], 0, 10)) ?></td></tr>
        <?php endforeach; ?>
    </tbody></table></div>
<?php endif; ?>
