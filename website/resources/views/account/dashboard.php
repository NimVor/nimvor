<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $requests */
$v->layout('layouts/app');
$v->share('area', 'account');
$v->set('title', 'Dashboard');
$badge = static fn (string $s): string => match ($s) { 'new' => 'badge-new', 'quoted', 'in_progress' => 'badge-warn', 'done' => 'badge-ok', default => 'badge-off' };
?>
<div class="app-top"><div><h1>Hello, <?= e(explode(' ', (string) $user['name'])[0]) ?></h1><p class="muted mb-0">Your NimVor account at a glance.</p></div>
    <a class="btn btn-primary" href="/resources/build-services#request">New build request</a></div>
<div class="tiles">
    <div class="tile"><small>Open requests</small><strong><?= (int) $openRequests ?></strong></div>
    <div class="tile"><small>Messages sent</small><strong><?= (int) $messageCount ?></strong></div>
    <div class="tile"><small>Member since</small><strong><?= e(substr((string) $user['created_at'], 0, 10)) ?></strong></div>
</div>
<div class="panel">
    <h2>Recent build requests</h2>
    <?php if ($requests === []): ?>
        <p class="muted mb-0">You have no requests yet. <a href="/resources/build-services#request">Ask for a quote</a>.</p>
    <?php else: ?>
        <div class="table-wrap"><table class="data"><thead><tr><th>Service</th><th>Status</th><th>Sent</th></tr></thead><tbody>
            <?php foreach ($requests as $r): ?>
                <tr><td><?= e(\NimVorSite\Repositories\BuildRequests::SERVICES[$r['service']] ?? $r['service']) ?></td><td><span class="badge <?= $badge((string) $r['status']) ?>"><?= e(str_replace('_', ' ', (string) $r['status'])) ?></span></td><td><?= e(substr((string) $r['created_at'], 0, 10)) ?></td></tr>
            <?php endforeach; ?>
        </tbody></table></div>
    <?php endif; ?>
</div>
<div class="grid grid-3">
    <a class="card card-link" href="/resources/documentation#install"><h3>Install NimVor</h3><p class="muted mb-0">The one-line installer and its options.</p></a>
    <a class="card card-link" href="/resources/documentation"><h3>Documentation</h3><p class="muted mb-0">Guides for administrators.</p></a>
    <a class="card card-link" href="/support/contact"><h3>Contact us</h3><p class="muted mb-0">Ask a question.</p></a>
</div>
