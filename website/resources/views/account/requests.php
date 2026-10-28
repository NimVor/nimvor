<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $requests */
$v->layout('layouts/app');
$v->share('area', 'account');
$v->set('title', 'Build requests');
$badge = static fn (string $s): string => match ($s) { 'new' => 'badge-new', 'quoted', 'in_progress' => 'badge-warn', 'done' => 'badge-ok', default => 'badge-off' };
?>
<div class="app-top"><div><h1>Build requests</h1><p class="muted mb-0">Requests you sent while signed in.</p></div><a class="btn btn-primary" href="/resources/build-services#request">New request</a></div>
<?php if ($requests === []): ?>
    <div class="panel empty">Nothing here yet.</div>
<?php else: ?>
    <div class="table-wrap"><table class="data"><thead><tr><th>#</th><th>Service</th><th>Details</th><th>Status</th><th>Sent</th></tr></thead><tbody>
        <?php foreach ($requests as $r): ?>
            <tr><td><?= (int) $r['id'] ?></td><td><?= e(\NimVorSite\Repositories\BuildRequests::SERVICES[$r['service']] ?? $r['service']) ?></td><td><?= e(mb_strimwidth((string) $r['details'], 0, 140, '…')) ?></td><td><span class="badge <?= $badge((string) $r['status']) ?>"><?= e(str_replace('_', ' ', (string) $r['status'])) ?></span></td><td><?= e(substr((string) $r['created_at'], 0, 10)) ?></td></tr>
        <?php endforeach; ?>
    </tbody></table></div>
<?php endif; ?>
