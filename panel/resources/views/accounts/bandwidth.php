<?php
/** @var \WhaCP\View\View $this */
/** @var list<array{id: int, username: string, role: string, plan: string, used: int, limit: int, excess: int, suspended: bool}> $rows */
/** @var bool $all */
/** @var string $month */
$admin = $this->serverScope();
?>
<?php $this->start(); ?>Bandwidth<?php $this->end('title'); ?>
<?php $this->start(); ?>Monthly bandwidth of accounts with a limit — <?= e($month) ?><?php $this->end('subtitle'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('users')) ?>"><i class="bi bi-arrow-left"></i> Users</a><?php $this->end('actions'); ?>

<nav class="wcp-pagetabs" aria-label="Show">
    <a class="<?= !$all ? 'active' : '' ?>" href="<?= e($this->url('accounts.bandwidth')) ?>">Over the limit</a>
    <a class="<?= $all ? 'active' : '' ?>" href="<?= e($this->url('accounts.bandwidth', [], ['show' => 'all'])) ?>">All with a limit</a>
</nav>

<div class="wcp-panel" data-wcp-dt id="bw-table">
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Account</th><th>Plan</th><th class="text-end">Used (MB)</th><th class="text-end">Limit (MB)</th><th class="text-end">Over by (MB)</th><th>Status</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($rows as $r): ?>
                <tr>
                    <td><a class="fw-semibold" href="<?= e($this->url('users.edit', ['id' => $r['id']])) ?>"><?= e($r['username']) ?></a> <span class="small text-body-secondary"><?= e($r['role']) ?></span></td>
                    <td class="small"><?= e($r['plan']) ?></td>
                    <td class="text-end"><?= e(number_format($r['used'])) ?></td>
                    <td class="text-end"><?= e(number_format($r['limit'])) ?></td>
                    <td class="text-end <?= $r['excess'] > 0 ? 'wcp-bad fw-semibold' : '' ?>"><?= $r['excess'] > 0 ? e(number_format($r['excess'])) : '—' ?></td>
                    <td><?= $r['suspended'] ? '<span class="wcp-state is-error">Suspended</span>' : '<span class="wcp-state is-ok">Active</span>' ?></td>
                    <td class="text-end text-nowrap">
                        <?php if ($admin): ?>
                            <form class="d-inline" method="post" action="<?= e($this->url('users.bandwidth.reset', ['id' => $r['id']])) ?>" data-wcp-confirm="Reset <?= e($r['username']) ?>'s bandwidth for <?= e($month) ?> to 0? The usage counted so far this month (<?= e(number_format($r['used'])) ?> MB) is discarded; earlier months are not affected."><?= $this->csrfField() ?><input type="hidden" name="return_to" value="bandwidth"><button class="btn btn-sm btn-outline-secondary">Reset</button></form>
                            <?php if (!$r['suspended']): ?>
                                <form class="d-inline" method="post" action="<?= e($this->url('users.suspend', ['id' => $r['id']])) ?>" data-wcp-confirm="Suspend <?= e($r['username']) ?>? Its websites go offline and email logins, databases, FTP/SFTP, cron jobs and workers stop until you unsuspend it. Nothing is deleted."><?= $this->csrfField() ?><input type="hidden" name="reason" value="Bandwidth limit exceeded (<?= e($month) ?>)"><button class="btn btn-sm btn-outline-danger">Suspend</button></form>
                            <?php else: ?>
                                <form class="d-inline" method="post" action="<?= e($this->url('users.unsuspend', ['id' => $r['id']])) ?>" data-wcp-confirm="Unsuspend <?= e($r['username']) ?>?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-success">Unsuspend</button></form>
                            <?php endif; ?>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            <?php if ($rows === []): ?><tr><td colspan="7" class="text-center py-4 wcp-muted"><?= $all ? 'No account has a bandwidth limit.' : 'No account is over its bandwidth limit this month.' ?></td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
<p class="small text-body-secondary mt-2">A reseller's limit covers its customers' websites too. Usage is counted from the websites' access logs every few minutes.</p>
