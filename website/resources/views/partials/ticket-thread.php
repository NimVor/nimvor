<?php
/**
 * What the customer sees of a ticket: the conversation, a box to answer and the close button.
 *
 * @var \NimVorSite\Core\View $v
 * @var array<string, mixed> $ticket
 * @var list<array<string, mixed>> $replies
 * @var string $base the ticket's own address (the form posts to it)
 * @var bool $mayReply
 * @var array<string, string> $errors
 * @var array<string, mixed> $old
 */
$errors ??= [];
$old ??= [];
?>
<div class="ticket-meta">
    <?= $v->partial('partials/ticket-badge', ['status' => (string) $ticket['status']]) ?>
    <span class="muted">Ticket <strong><?= e($ticket['ref']) ?></strong> · opened <?= e(substr((string) $ticket['created_at'], 0, 16)) ?> UTC · last activity <?= e(substr((string) $ticket['updated_at'], 0, 16)) ?> UTC</span>
</div>
<div class="thread">
    <?php foreach ($replies as $r): ?>
        <article class="msg msg-<?= e($r['author']) ?>">
            <header><strong><?= $r['author'] === 'staff' ? e($r['author_name'] ?: 'NimVor') . ' <span class="badge">team</span>' : e($ticket['name']) ?></strong><span class="muted"><?= e(substr((string) $r['created_at'], 0, 16)) ?> UTC</span></header>
            <div class="msg-body"><?= nl2br(e($r['body']), false) ?></div>
        </article>
    <?php endforeach; ?>
</div>
<?php if ($mayReply): ?>
    <form class="card form" method="post" action="<?= e($base) ?>/reply" novalidate data-once>
        <?= $v->csrf() ?>
        <h2 class="h-card mb-0"><?= $ticket['status'] === 'closed' ? 'Reopen with a reply' : 'Add a reply' ?></h2>
        <div class="field"><label for="body">Your message</label><textarea class="textarea" id="body" name="body" maxlength="5000" required><?= e($old['body'] ?? '') ?></textarea><?= $v->error($errors, 'body') ?></div>
        <div><button class="btn btn-primary" type="submit">Send reply</button></div>
    </form>
    <?php if ($ticket['status'] !== 'closed'): ?>
        <form class="inline-form mt-2" method="post" action="<?= e($base) ?>/close" data-confirm="Close this ticket? You can still reopen it by replying for <?= (int) \NimVorSite\Services\TicketService::REOPEN_DAYS ?> days."><?= $v->csrf() ?><button class="btn btn-outline btn-sm" type="submit">Close the ticket</button></form>
    <?php endif; ?>
<?php else: ?>
    <p class="muted">This ticket was closed a while ago. <a href="/support/contact">Open a new one</a> and mention <?= e($ticket['ref']) ?>.</p>
<?php endif; ?>
