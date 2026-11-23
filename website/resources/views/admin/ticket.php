<?php
/**
 * @var \NimVorSite\Core\View $v
 * @var array<string, mixed> $ticket
 * @var list<array<string, mixed>> $replies
 * @var list<array<string, mixed>> $staff
 * @var list<array<string, mixed>> $canned
 * @var string $guestUrl
 * @var array<string, string> $errors
 * @var array<string, mixed> $old
 */
use NimVorSite\Repositories\Tickets;

$errors ??= [];
$old ??= [];
$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Ticket ' . $ticket['ref']);
$v->set('script', 'js/tickets.js');
$label = static fn (string $s): string => ucfirst(str_replace('_', ' ', $s));
?>
<div class="app-top"><div><h1><?= e($ticket['subject']) ?></h1><p class="muted mb-0"><a href="/admin/tickets">&larr; All tickets</a> · <?= e($ticket['ref']) ?></p></div><div><?= $v->partial('partials/ticket-badge', ['status' => (string) $ticket['status'], 'priority' => (string) $ticket['priority']]) ?></div></div>
<div class="ticket-admin">
    <div>
        <div class="thread">
            <?php foreach ($replies as $r): ?>
                <?php if ($r['author'] === 'system'): ?>
                    <div class="msg msg-system"><?= e($r['body']) ?> <span class="muted"><?= e(substr((string) $r['created_at'], 0, 16)) ?> UTC</span></div>
                <?php else: ?>
                    <article class="msg msg-<?= e($r['author']) ?><?= (int) $r['internal'] === 1 ? ' msg-internal' : '' ?>">
                        <header><strong><?= $r['author'] === 'staff' ? e($r['author_name'] ?: 'Team') : e($ticket['name']) ?></strong><?= (int) $r['internal'] === 1 ? ' <span class="badge badge-warn">internal note</span>' : '' ?><span class="muted"><?= e(substr((string) $r['created_at'], 0, 16)) ?> UTC</span></header>
                        <div class="msg-body"><?= nl2br(e($r['body']), false) ?></div>
                    </article>
                <?php endif; ?>
            <?php endforeach; ?>
        </div>
        <form class="panel form" method="post" action="/admin/tickets/<?= (int) $ticket['id'] ?>/reply" novalidate data-once>
            <?= $v->csrf() ?>
            <h2 class="mb-0">Answer</h2>
            <?php if ($canned !== []): ?>
                <div class="field"><label for="canned">Insert a canned reply</label>
                    <select class="select" id="canned" data-canned><option value="">Choose…</option><?php foreach ($canned as $c): ?><option value="<?= (int) $c['id'] ?>" data-body="<?= e($c['body']) ?>"><?= e($c['title']) ?></option><?php endforeach; ?></select></div>
            <?php endif; ?>
            <div class="field"><label for="body">Message</label><textarea class="textarea" id="body" name="body" maxlength="10000" rows="7" required><?= e($old['body'] ?? '') ?></textarea><?= $v->error($errors, 'body') ?></div>
            <div class="field-row">
                <label class="check"><input type="radio" name="mode" value="reply" checked> Reply to the customer (they get it by email when mail is set up)</label>
                <label class="check"><input type="radio" name="mode" value="note"> Internal note (only staff see it)</label>
            </div>
            <div class="field"><label for="after">Then set the status to</label>
                <select class="select" id="after" name="status"><?php foreach (['answered' => 'Answered (waiting for the customer)', 'on_hold' => 'On hold', 'resolved' => 'Resolved', 'closed' => 'Closed'] as $k => $text): ?><option value="<?= e($k) ?>"<?= ($old['status'] ?? 'answered') === $k ? ' selected' : '' ?>><?= e($text) ?></option><?php endforeach; ?></select><?= $v->error($errors, 'status') ?></div>
            <div><button class="btn btn-primary" type="submit">Send</button></div>
        </form>
    </div>
    <aside>
        <form class="panel form" method="post" action="/admin/tickets/<?= (int) $ticket['id'] ?>/update" data-once>
            <?= $v->csrf() ?>
            <h2 class="mb-0">Details</h2>
            <div class="field"><label for="d-status">Status</label><select class="select" id="d-status" name="status"><?php foreach (Tickets::STATUSES as $s): ?><option value="<?= e($s) ?>"<?= $ticket['status'] === $s ? ' selected' : '' ?>><?= e($label($s)) ?></option><?php endforeach; ?></select></div>
            <div class="field"><label for="d-priority">Priority</label><select class="select" id="d-priority" name="priority"><?php foreach (Tickets::PRIORITIES as $s): ?><option value="<?= e($s) ?>"<?= $ticket['priority'] === $s ? ' selected' : '' ?>><?= e($label($s)) ?></option><?php endforeach; ?></select></div>
            <div class="field"><label for="d-category">Category</label><select class="select" id="d-category" name="category"><?php foreach (Tickets::CATEGORIES as $s): ?><option value="<?= e($s) ?>"<?= $ticket['category'] === $s ? ' selected' : '' ?>><?= e($label($s)) ?></option><?php endforeach; ?></select></div>
            <div class="field"><label for="d-assignee">Owner</label><select class="select" id="d-assignee" name="assignee"><option value="none">Nobody</option><?php foreach ($staff as $s): ?><option value="<?= (int) $s['id'] ?>"<?= (string) $ticket['assigned_to'] === (string) $s['id'] ? ' selected' : '' ?>><?= e($s['name']) ?></option><?php endforeach; ?></select></div>
            <div><button class="btn btn-outline btn-sm" type="submit">Save details</button></div>
        </form>
        <div class="panel">
            <h2 class="mb-0">Customer</h2>
            <p class="mb-0"><?= e($ticket['name']) ?><br><a href="mailto:<?= e($ticket['email']) ?>"><?= e($ticket['email']) ?></a></p>
            <p class="muted mb-0"><?= $ticket['user_id'] !== null ? 'Has an account.' : 'No account: they follow the ticket by its private link.' ?></p>
            <p class="muted mb-0">Opened <?= e(substr((string) $ticket['created_at'], 0, 16)) ?> UTC<?= $ticket['first_response_at'] !== null ? '; first reply after ' . e((string) max(1, (int) round((strtotime($ticket['first_response_at'] . ' UTC') - strtotime($ticket['created_at'] . ' UTC')) / 60))) . ' min' : '; not answered yet' ?>.</p>
            <form class="mt-3" method="post" action="/admin/tickets/<?= (int) $ticket['id'] ?>/delete" data-confirm="Delete ticket <?= e($ticket['ref']) ?> and all its messages for good? This cannot be undone."><?= $v->csrf() ?><button class="btn btn-danger btn-sm" type="submit">Delete this ticket</button></form>
            <details><summary class="muted">The customer's private link</summary><p class="mb-0 small"><code><?= e($guestUrl) ?></code></p></details>
        </div>
    </aside>
</div>
