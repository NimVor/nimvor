<?php /** @var \WhaCP\View\View $this */ ?>
<?php $this->start(); ?>Audit log<?php $this->end('title'); ?>
<?php $this->start(); ?><?= (int) $total ?> recorded event(s)<?php $this->end('subtitle'); ?>

<div class="card">
    <div class="card-header">
        <form class="row g-2" method="get" action="<?= e($this->url('audit')) ?>">
            <div class="col-md-3"><input class="form-control form-control-sm" name="action" value="<?= e($filters['action']) ?>" placeholder="Action prefix, e.g. user." aria-label="Action"></div>
            <div class="col-md-2"><input class="form-control form-control-sm" name="user" value="<?= e($filters['user']) ?>" placeholder="Username" aria-label="User"></div>
            <div class="col-md-2">
                <select class="form-select form-select-sm" name="result" aria-label="Result">
                    <option value="">Any result</option>
                    <?php foreach (['success', 'failure', 'denied'] as $r): ?>
                        <option value="<?= e($r) ?>"<?= $filters['result'] === $r ? ' selected' : '' ?>><?= e(ucfirst($r)) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
            <div class="col-md-3"><input class="form-control form-control-sm" name="q" value="<?= e($filters['q']) ?>" placeholder="Target, IP or details" aria-label="Search"></div>
            <div class="col-md-2 d-flex gap-2">
                <button class="btn btn-sm btn-outline-secondary" type="submit">Filter</button>
                <a class="btn btn-sm btn-link" href="<?= e($this->url('audit')) ?>">Reset</a>
            </div>
        </form>
    </div>
    <?php if ($rows === []): ?>
        <div class="card-body text-center py-5 text-body-secondary wcp-empty"><i class="bi bi-list-check display-6"></i><p class="mt-3 mb-0">No events match.</p></div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table table-sm table-hover align-middle mb-0 wcp-table">
                <thead><tr><th>Time (UTC)</th><th>User</th><th>Action</th><th>Target</th><th>Result</th><th>IP</th><th>Details</th></tr></thead>
                <tbody>
                <?php foreach ($rows as $r): ?>
                    <tr>
                        <td class="text-nowrap small"><?= e(gmdate('Y-m-d H:i:s', (int) strtotime((string) $r['created_at']))) ?></td>
                        <td><?= e($r['username'] !== '' ? $r['username'] : '—') ?></td>
                        <td><code><?= e($r['action']) ?></code></td>
                        <td class="small"><?= e(trim($r['target_type'] . ' ' . $r['target_id'])) ?></td>
                        <td><?= $this->statusBadge((string) $r['result']) ?></td>
                        <td class="small"><?= e($r['ip']) ?></td>
                        <td class="small"><?php if ($r['details'] !== '{}' && $r['details'] !== '[]'): ?><code class="wcp-break"><?= e($r['details']) ?></code><?php endif; ?></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
    <?php if ($pages > 1): ?>
        <div class="card-footer">
            <nav aria-label="Audit log pages"><ul class="pagination pagination-sm mb-0">
                <?php $q = array_filter($filters, static fn ($v) => $v !== ''); ?>
                <li class="page-item<?= $page <= 1 ? ' disabled' : '' ?>"><a class="page-link" href="<?= e($this->url('audit', [], $q + ['page' => $page - 1])) ?>">Previous</a></li>
                <li class="page-item disabled"><span class="page-link">Page <?= (int) $page ?> of <?= (int) $pages ?></span></li>
                <li class="page-item<?= $page >= $pages ? ' disabled' : '' ?>"><a class="page-link" href="<?= e($this->url('audit', [], $q + ['page' => $page + 1])) ?>">Next</a></li>
            </ul></nav>
        </div>
    <?php endif; ?>
</div>
