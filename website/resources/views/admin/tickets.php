<?php
/**
 * @var \NimVorSite\Core\View $v
 * @var array{rows: list<array<string, mixed>>, total: int, page: int, pages: int} $result
 * @var array<string, string> $filters
 * @var array<string, int> $counts
 * @var array{waiting: int, unassigned: int, first_response_seconds: ?int, answered_30d: int} $stats
 * @var list<array<string, mixed>> $staff
 * @var int $me
 */
use NimVorSite\Repositories\Tickets;

$v->layout('layouts/app');
$v->share('area', 'admin');
$v->set('title', 'Tickets');
$v->set('script', 'js/tickets.js');
$label = static fn (string $s): string => ucfirst(str_replace('_', ' ', $s));
$qs = static fn (array $over): string => '?' . http_build_query(array_filter($over + [], static fn (mixed $x): bool => $x !== '' && $x !== null));
$base = $filters;
$resp = $stats['first_response_seconds'];
$respText = $resp === null ? 'N/A' : ($resp < 3600 ? max(1, (int) round($resp / 60)) . ' min' : ($resp < 172800 ? round($resp / 3600, 1) . ' h' : round($resp / 86400, 1) . ' days'));
$back = '/admin/tickets' . $qs($base + ['page' => $result['page'] > 1 ? (string) $result['page'] : '']);
?>
<div class="app-top"><div><h1>Tickets</h1><p class="muted mb-0">Conversations from the contact form and from customer accounts.</p></div><div class="cta-row"><a class="btn btn-outline btn-sm" href="/admin/tickets/canned">Canned replies</a><a class="btn btn-outline btn-sm" href="/admin/reports/tickets.csv">Download CSV</a></div></div>
<div class="tiles">
    <div class="tile"><small>Waiting for us</small><strong><?= (int) $stats['waiting'] ?></strong></div>
    <div class="tile"><small>Active, no owner</small><strong><?= (int) $stats['unassigned'] ?></strong></div>
    <div class="tile"><small>First reply, 30 days</small><strong><?= e($respText) ?></strong></div>
    <div class="tile"><small>Answered, 30 days</small><strong><?= (int) $stats['answered_30d'] ?></strong></div>
</div>
<nav class="tabs" aria-label="Status">
    <?php foreach (['active' => 'Active', 'open' => 'Open', 'answered' => 'Answered', 'on_hold' => 'On hold', 'resolved' => 'Resolved', 'closed' => 'Closed', 'all' => 'All'] as $k => $text): ?>
        <a href="/admin/tickets<?= e($qs(['status' => $k === 'active' ? '' : $k] + array_diff_key($base, ['status' => 1, 'page' => 1]))) ?>"<?= $filters['status'] === $k ? ' aria-current="page"' : '' ?>><?= e($text) ?><?php if ($k !== 'all'): ?> <span class="muted"><?= (int) ($counts[$k] ?? 0) ?></span><?php endif; ?></a>
    <?php endforeach; ?>
</nav>
<form class="filters" method="get" action="/admin/tickets">
    <input type="hidden" name="status" value="<?= e($filters['status'] === 'active' ? '' : $filters['status']) ?>">
    <input class="input" type="search" name="q" value="<?= e($filters['q']) ?>" placeholder="Reference, subject, name or email" aria-label="Search tickets">
    <select class="select" name="priority" aria-label="Priority"><option value="">Any priority</option><?php foreach (Tickets::PRIORITIES as $p): ?><option value="<?= e($p) ?>"<?= $filters['priority'] === $p ? ' selected' : '' ?>><?= e($label($p)) ?></option><?php endforeach; ?></select>
    <select class="select" name="category" aria-label="Category"><option value="">Any category</option><?php foreach (Tickets::CATEGORIES as $c): ?><option value="<?= e($c) ?>"<?= $filters['category'] === $c ? ' selected' : '' ?>><?= e($label($c)) ?></option><?php endforeach; ?></select>
    <select class="select" name="assignee" aria-label="Owner"><option value="">Any owner</option><option value="none"<?= $filters['assignee'] === 'none' ? ' selected' : '' ?>>Nobody</option><?php foreach ($staff as $s): ?><option value="<?= (int) $s['id'] ?>"<?= $filters['assignee'] === (string) $s['id'] ? ' selected' : '' ?>><?= e($s['name']) ?><?= (int) $s['id'] === $me ? ' (me)' : '' ?></option><?php endforeach; ?></select>
    <select class="select" name="sort" aria-label="Order"><option value="">Priority, newest first</option><option value="oldest"<?= $filters['sort'] === 'oldest' ? ' selected' : '' ?>>Longest waiting first</option></select>
    <button class="btn btn-outline btn-sm" type="submit">Filter</button>
</form>
<?php if ($result['rows'] === []): ?>
    <div class="panel empty">No tickets match.</div>
<?php else: ?>
    <form method="post" action="/admin/tickets/bulk" data-bulk>
        <?= $v->csrf() ?><input type="hidden" name="back" value="<?= e($back) ?>">
        <div class="table-wrap"><table class="data"><thead><tr><th><input type="checkbox" data-select-all aria-label="Select all"></th><th>Ticket</th><th>Subject</th><th>From</th><th>Status</th><th>Owner</th><th>Last activity</th></tr></thead><tbody>
            <?php foreach ($result['rows'] as $t): ?>
                <tr>
                    <td><input type="checkbox" name="ids[]" value="<?= (int) $t['id'] ?>" aria-label="Select <?= e($t['ref']) ?>"></td>
                    <td><a href="/admin/tickets/<?= (int) $t['id'] ?>"><?= e($t['ref']) ?></a></td>
                    <td><a href="/admin/tickets/<?= (int) $t['id'] ?>"><?= e($t['subject']) ?></a><div class="muted"><?= e($label((string) $t['category'])) ?> · <?= (int) $t['replies'] ?> message<?= (int) $t['replies'] === 1 ? '' : 's' ?></div></td>
                    <td><?= e($t['name']) ?><div class="muted"><?= e($t['email']) ?></div></td>
                    <td><?= $v->partial('partials/ticket-badge', ['status' => (string) $t['status'], 'priority' => (string) $t['priority']]) ?></td>
                    <td><?= $t['assignee_name'] !== null ? e($t['assignee_name']) : '<span class="muted">nobody</span>' ?></td>
                    <td><?= e(substr((string) $t['updated_at'], 0, 16)) ?> UTC</td>
                </tr>
            <?php endforeach; ?>
        </tbody></table></div>
        <div class="bulk-bar">
            <select class="select" name="action" aria-label="Action for the selected tickets">
                <option value="">With the selected…</option><option value="assign_me">Assign to me</option><option value="resolve">Mark resolved</option><option value="close">Close</option>
                <option value="high">Priority: high</option><option value="normal">Priority: normal</option><option value="low">Priority: low</option>
            </select>
            <button class="btn btn-outline btn-sm" type="submit">Apply</button>
            <span class="muted"><?= (int) $result['total'] ?> ticket<?= $result['total'] === 1 ? '' : 's' ?>, page <?= (int) $result['page'] ?> of <?= (int) $result['pages'] ?></span>
        </div>
    </form>
    <?php if ($result['pages'] > 1): ?>
        <nav class="pager" aria-label="Pages">
            <?php if ($result['page'] > 1): ?><a href="/admin/tickets<?= e($qs($base + ['page' => (string) ($result['page'] - 1)])) ?>">&larr; Newer</a><?php endif; ?>
            <?php if ($result['page'] < $result['pages']): ?><a href="/admin/tickets<?= e($qs($base + ['page' => (string) ($result['page'] + 1)])) ?>">Older &rarr;</a><?php endif; ?>
        </nav>
    <?php endif; ?>
<?php endif; ?>
