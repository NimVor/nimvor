<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $queue */
$canManage = $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE);
$counts = (array) ($queue['counts'] ?? []);
$age = static function (int $t): string {
    $s = max(0, time() - $t);
    return $s < 3600 ? (int) ($s / 60) . ' min' : ($s < 172800 ? (int) ($s / 3600) . ' h' : (int) ($s / 86400) . ' d');
};
?>
<?= $this->partial('mail/admin/_tabs', get_defined_vars()) ?>
<?php if ($queue === null) { return; } ?>
<div class="wcp-panel mb-3">
    <div class="wcp-statusbar">
        <span class="fw-medium"><?= (int) $queue['total'] ?> message(s) in the queue</span>
        <?php foreach (['active' => 'Active', 'deferred' => 'Deferred', 'hold' => 'On hold', 'incoming' => 'Incoming'] as $k => $l): ?>
            <span class="small"><span class="wcp-muted"><?= e($l) ?></span> <span class="<?= $k === 'deferred' && ($counts[$k] ?? 0) > 0 ? 'wcp-bad' : '' ?>"><?= (int) ($counts[$k] ?? 0) ?></span></span>
        <?php endforeach; ?>
        <?php if ($canManage): ?>
            <form class="ms-auto" method="post" action="<?= e($this->url('mail.admin.queue.action')) ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="action" value="flush"><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat me-1"></i>Retry all deferred</button></form>
        <?php endif; ?>
    </div>
</div>

<div class="wcp-panel" data-wcp-dt id="queue-table">
    <form id="queue-form" method="post" action="<?= e($this->url('mail.admin.queue.action')) ?>"><?= $this->csrfField() ?></form>
    <div class="wcp-actionbar">
        <?php if ($canManage): ?>
            <button class="btn btn-sm btn-outline-primary" form="queue-form" name="action" value="retry">Deliver now</button>
            <button class="btn btn-sm btn-outline-secondary" form="queue-form" name="action" value="hold">Hold</button>
            <button class="btn btn-sm btn-outline-secondary" form="queue-form" name="action" value="release">Release</button>
            <button class="btn btn-sm btn-outline-danger" form="queue-form" name="action" value="delete" data-wcp-confirm-submit="Delete the selected messages? They are removed from the queue and never delivered.">Delete</button>
        <?php endif; ?>
        <input class="form-control form-control-sm ms-auto wcp-w-30" type="search" placeholder="Search sender, recipient, reason" aria-label="Search the queue" data-wcp-dt-search>
    </div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><?php if ($canManage): ?><th class="wcp-w-5"><span class="visually-hidden">Select</span></th><?php endif; ?><th>Queue ID</th><th>Sender</th><th>Recipients</th><th>Status</th><th class="text-end">Size</th><th class="text-end">Age</th><th></th></tr></thead>
            <tbody>
            <?php foreach ((array) $queue['messages'] as $m): $m = (array) $m; ?>
                <tr>
                    <?php if ($canManage): ?><td><input class="form-check-input" type="checkbox" name="ids[]" value="<?= e($m['id']) ?>" form="queue-form" aria-label="Select <?= e($m['id']) ?>"></td><?php endif; ?>
                    <td class="font-monospace small"><?= e($m['id']) ?></td>
                    <td class="small text-break"><?= $m['sender'] === '' ? '<span class="wcp-muted">(bounce)</span>' : e($m['sender']) ?></td>
                    <td class="small text-break"><?= e(implode(', ', array_slice((array) $m['recipients'], 0, 3))) ?><?= count((array) $m['recipients']) > 3 ? ' +' . (count((array) $m['recipients']) - 3) : '' ?></td>
                    <td class="small"><span class="wcp-state <?= $m['queue'] === 'deferred' ? 'is-warn' : ($m['queue'] === 'hold' ? 'is-off' : 'is-ok') ?>"><?= e(ucfirst((string) $m['queue'])) ?></span><?php if (($m['reason'] ?? '') !== ''): ?><div class="wcp-muted text-truncate wcp-w-100" title="<?= e($m['reason']) ?>"><?= e(mb_strimwidth((string) $m['reason'], 0, 90, '…')) ?></div><?php endif; ?></td>
                    <td class="text-end small"><?= e(format_bytes((int) $m['size'])) ?></td>
                    <td class="text-end small"><?= e($age((int) $m['arrival'])) ?></td>
                    <td class="text-end"><button class="btn btn-sm btn-outline-secondary" type="button" data-wcp-mail-headers="<?= e($this->url('mail.admin.message', ['id' => $m['id']])) ?>" aria-label="Headers of <?= e($m['id']) ?>"><i class="bi bi-envelope-open"></i></button></td>
                </tr>
            <?php endforeach; ?>
            <?php if ((array) $queue['messages'] === []): ?><tr><td colspan="8" class="text-center py-4 wcp-muted">The mail queue is empty.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
<div class="modal fade" id="wcp-headers" tabindex="-1" aria-labelledby="wcp-headers-title" aria-hidden="true">
    <div class="modal-dialog modal-lg"><div class="modal-content">
        <div class="modal-header"><h2 class="modal-title fs-5" id="wcp-headers-title">Message headers</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
        <div class="modal-body"><dl class="row small mb-0" data-wcp-headers-body><dd class="col-12 wcp-muted">Loading…</dd></dl></div>
    </div></div>
</div>
