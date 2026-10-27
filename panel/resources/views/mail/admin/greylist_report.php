<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $report */
/** @var array{search: string, status: string, sort: string, dir: string, page: int, per: int} $q */
use WhaCP\Http\Controllers\MailGreylistController as G;

$canManage = $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE);
$time = static fn (int $t): string => $t > 0 ? date('Y-m-d H:i', $t) : '—';
$link = function (array $change) use ($q): string {
    $p = array_filter(array_replace($q, $change), static fn ($v): bool => $v !== '' && $v !== null);
    return $this->url('mail.greylist.report', [], $p);
};
$cols = ['ip' => 'Sender IP address', 'from' => 'From address', 'to' => 'To address', 'deferred' => 'Deferred', 'accepted' => 'Accepted', 'created' => 'Create time', 'block_expire' => 'Block expire time', 'must_retry' => 'Must retry time', 'record_expire' => 'Record expire time'];
$badge = ['deferring' => 'is-warn', 'waiting' => 'is-ok', 'passed' => 'is-ok', 'expired' => 'is-off'];
?>
<?= $this->partial('mail/admin/_tabs', get_defined_vars()) ?>
<?php if ($report === null) { return; } ?>
<?php
$total = (int) ($report['total'] ?? 0);
$pages = max(1, (int) ceil($total / $q['per']));
$counts = (array) ($report['counts'] ?? []);
?>
<?php if (empty($report['running'])): ?>
    <div class="alert alert-info">The greylisting service is not running, so there are no records to show. Turn greylisting on under <a href="<?= e($this->url('mail.greylist')) ?>">Greylisting</a>.</div>
<?php endif; ?>
<div class="wcp-panel mb-3">
    <div class="wcp-statusbar">
        <span class="fw-medium"><?= array_sum(array_map('intval', $counts)) ?> record(s)</span>
        <?php foreach (G::STATUSES as $k => $l): ?><span class="small"><span class="wcp-muted"><?= e($l) ?></span> <?= (int) ($counts[$k] ?? 0) ?></span><?php endforeach; ?>
        <?php if ($canManage && !empty($report['running'])): ?>
            <form class="ms-auto" method="post" action="<?= e($this->url('mail.greylist.cleanup')) ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="op" value="purge"><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-trash3 me-1"></i>Remove expired</button></form>
            <form method="post" action="<?= e($this->url('mail.greylist.cleanup')) ?>" data-wcp-confirm="Remove all greylisting records? Every sender is greylisted again on its next message."><?= $this->csrfField() ?><input type="hidden" name="op" value="delete_all"><button class="btn btn-sm btn-outline-danger">Remove all</button></form>
        <?php endif; ?>
    </div>
</div>

<div class="wcp-panel">
    <form id="gl-sel" method="post" action="<?= e($this->url('mail.greylist.cleanup')) ?>"><?= $this->csrfField() ?><input type="hidden" name="op" value="delete"></form>
    <form class="wcp-actionbar" method="get" action="<?= e($this->url('mail.greylist.report')) ?>">
        <?php if ($canManage): ?><button class="btn btn-sm btn-outline-danger" form="gl-sel" data-wcp-confirm-submit="Remove the selected records? Those senders are greylisted again on their next message.">Remove selected</button><?php endif; ?>
        <div class="wcp-actionbar-end">
            <input type="hidden" name="sort" value="<?= e($q['sort']) ?>"><input type="hidden" name="dir" value="<?= e($q['dir']) ?>">
            <select class="form-select form-select-sm w-auto" name="status" aria-label="Status">
                <option value="">All records</option>
                <?php foreach (G::STATUSES as $k => $l): ?><option value="<?= e($k) ?>"<?= $q['status'] === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?>
            </select>
            <select class="form-select form-select-sm w-auto" name="per" aria-label="Rows per page"><?php foreach ([25, 50, 100, 250] as $n): ?><option value="<?= $n ?>"<?= $q['per'] === $n ? ' selected' : '' ?>><?= $n ?> per page</option><?php endforeach; ?></select>
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" name="search" value="<?= e($q['search']) ?>" placeholder="IP address or email address" aria-label="Search records"><i class="bi bi-search" aria-hidden="true"></i></div>
            <button class="btn btn-sm btn-outline-secondary">Show</button>
        </div>
    </form>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle small">
            <thead><tr>
                <?php if ($canManage): ?><th class="wcp-w-5"><span class="visually-hidden">Select</span></th><?php endif; ?>
                <?php foreach ($cols as $k => $l): $active = $q['sort'] === $k; $next = $active && $q['dir'] === 'asc' ? 'desc' : 'asc'; ?>
                    <th<?= $active ? ' aria-sort="' . ($q['dir'] === 'asc' ? 'ascending' : 'descending') . '"' : '' ?>><a class="link-body-emphasis text-decoration-none text-nowrap" href="<?= e($link(['sort' => $k, 'dir' => $next, 'page' => 1])) ?>"><?= e($l) ?><?php if ($active): ?> <i class="bi bi-caret-<?= $q['dir'] === 'asc' ? 'up' : 'down' ?>-fill" aria-hidden="true"></i><?php endif; ?></a></th>
                <?php endforeach; ?>
                <th>Status</th>
            </tr></thead>
            <tbody>
            <?php foreach ((array) ($report['rows'] ?? []) as $r): $r = (array) $r; ?>
                <tr>
                    <?php if ($canManage): ?><td><input class="form-check-input" type="checkbox" form="gl-sel" name="sel[]" value="<?= e((string) json_encode([$r['ip'], $r['from'], $r['to']])) ?>" aria-label="Select <?= e($r['ip'] . ' ' . $r['from'] . ' ' . $r['to']) ?>"></td><?php endif; ?>
                    <td class="font-monospace"><?= e((string) $r['ip']) ?></td>
                    <td class="text-break"><?= e((string) $r['from']) ?></td>
                    <td class="text-break"><?= e((string) $r['to']) ?></td>
                    <td class="text-end"><?= (int) $r['deferred'] ?></td>
                    <td class="text-end"><?= (int) $r['accepted'] ?></td>
                    <td class="text-nowrap"><?= e($time((int) $r['created'])) ?></td>
                    <td class="text-nowrap"><?= e($time((int) $r['block_expire'])) ?></td>
                    <td class="text-nowrap"><?= e($time((int) $r['must_retry'])) ?></td>
                    <td class="text-nowrap"><?= e($time((int) $r['record_expire'])) ?></td>
                    <td class="text-nowrap"><span class="wcp-state <?= e($badge[$r['status']] ?? 'is-off') ?>"><?= e(G::STATUSES[$r['status']] ?? (string) $r['status']) ?></span></td>
                </tr>
            <?php endforeach; ?>
            <?php if (($report['rows'] ?? []) === []): ?><tr><td colspan="<?= $canManage ? 11 : 10 ?>" class="text-center text-body-secondary py-4">No records<?= $q['search'] !== '' || $q['status'] !== '' ? ' match' : ' yet' ?>.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
    <?php if ($total > 0): ?>
        <nav class="wcp-pager" aria-label="Pages">
            <a class="btn btn-sm btn-outline-secondary<?= $q['page'] <= 1 ? ' disabled' : '' ?>" href="<?= e($link(['page' => max(1, $q['page'] - 1)])) ?>" aria-label="Previous page"<?= $q['page'] <= 1 ? ' aria-disabled="true" tabindex="-1"' : '' ?>>‹</a>
            <span>Page <?= min($q['page'], $pages) ?> of <?= $pages ?></span>
            <a class="btn btn-sm btn-outline-secondary<?= $q['page'] >= $pages ? ' disabled' : '' ?>" href="<?= e($link(['page' => min($pages, $q['page'] + 1)])) ?>" aria-label="Next page"<?= $q['page'] >= $pages ? ' aria-disabled="true" tabindex="-1"' : '' ?>>›</a>
            <span class="wcp-pager-total"><?= $total ?> record(s)</span>
        </nav>
    <?php endif; ?>
    <p class="small text-body-secondary mt-2 mb-0">Deferring: retries are refused until the block expire time. Retry accepted: the next attempt before the must-retry time is accepted. Passed: mail is accepted until the record expire time, which each message renews. Expired records are removed automatically every hour.</p>
</div>
