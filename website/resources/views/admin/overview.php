<?php
/** @var \NimVorSite\Core\View $v */
/** @var array<string, mixed> $report */
/** @var list<array<string, mixed>> $recentMessages */
/** @var list<array<string, mixed>> $recentRequests */
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Admin overview');
$t = $report['totals'];
?>
<div class="app-top"><div><h1>Overview</h1><p class="muted mb-0">The site at a glance. Figures refresh every minute.</p></div><a class="btn btn-outline" href="/admin/reports">Full reports</a></div>
<div class="tiles">
    <div class="tile"><small>Users</small><strong><?= (int) $t['users'] ?></strong></div>
    <div class="tile"><small>New messages</small><strong><?= (int) $report['messages']['new'] ?></strong></div>
    <div class="tile"><small>New requests</small><strong><?= (int) $report['requests']['new'] ?></strong></div>
    <div class="tile"><small>Visits, 14 days</small><strong><?= (int) array_sum($report['visits']) ?></strong></div>
</div>
<div class="grid grid-2">
    <div class="panel"><h2>Latest messages</h2>
        <?php if ($recentMessages === []): ?><p class="muted mb-0">None yet.</p><?php else: ?>
            <div class="stack"><?php foreach ($recentMessages as $m): ?><div><strong><?= e($m['subject']) ?></strong><div class="muted"><?= e($m['name']) ?> · <?= e(substr((string) $m['created_at'], 0, 10)) ?> · <?= e($m['status']) ?></div></div><?php endforeach; ?></div>
        <?php endif; ?>
        <p class="mt-2 mb-0"><a href="/admin/messages">All messages</a></p>
    </div>
    <div class="panel"><h2>Latest build requests</h2>
        <?php if ($recentRequests === []): ?><p class="muted mb-0">None yet.</p><?php else: ?>
            <div class="stack"><?php foreach ($recentRequests as $r): ?><div><strong><?= e(\NimVorSite\Repositories\BuildRequests::SERVICES[$r['service']] ?? $r['service']) ?></strong><div class="muted"><?= e($r['name']) ?> · <?= e(substr((string) $r['created_at'], 0, 10)) ?> · <?= e(str_replace('_', ' ', (string) $r['status'])) ?></div></div><?php endforeach; ?></div>
        <?php endif; ?>
        <p class="mt-2 mb-0"><a href="/admin/requests">All requests</a></p>
    </div>
</div>
