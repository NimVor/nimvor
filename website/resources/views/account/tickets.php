<?php
/** @var \NimVorSite\Core\View $v */
/** @var list<array<string, mixed>> $tickets */
$v->layout('layouts/app');
$v->share('area', 'account');
$v->set('title', 'Tickets');
?>
<div class="app-top"><div><h1>Tickets</h1><p class="muted mb-0">Your conversations with the NimVor team.</p></div><a class="btn btn-primary" href="/account/tickets/new">New ticket</a></div>
<?php if ($tickets === []): ?>
    <div class="panel empty">No tickets yet. <a href="/account/tickets/new">Open one</a> when you need help.</div>
<?php else: ?>
    <div class="table-wrap"><table class="data"><thead><tr><th>Ticket</th><th>Subject</th><th>Status</th><th>Last activity</th></tr></thead><tbody>
        <?php foreach ($tickets as $t): ?>
            <tr><td><a href="/account/tickets/<?= e($t['ref']) ?>"><?= e($t['ref']) ?></a></td><td><?= e($t['subject']) ?></td><td><?= $v->partial('partials/ticket-badge', ['status' => (string) $t['status']]) ?></td><td><?= e(substr((string) $t['updated_at'], 0, 16)) ?> UTC</td></tr>
        <?php endforeach; ?>
    </tbody></table></div>
<?php endif; ?>
