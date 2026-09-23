<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $sites */
?>
<?php $this->start(); ?>Websites<?php $this->end('title'); ?>
<?php $this->start(); ?><?= (int) $total ?> existing website(s) discovered on this server<?php $this->end('subtitle'); ?>

<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<?php foreach ($warnings as $w): ?>
    <div class="alert alert-warning py-2 small"><i class="bi bi-exclamation-triangle me-1"></i><?= e($w) ?></div>
<?php endforeach; ?>

<div class="card">
    <div class="card-header">
        <form class="row g-2 align-items-center" method="get" action="<?= e($this->url('websites')) ?>">
            <div class="col-sm-6 col-md-5">
                <div class="input-group input-group-sm">
                    <span class="input-group-text"><i class="bi bi-search"></i></span>
                    <input class="form-control" type="search" name="q" value="<?= e($q) ?>" placeholder="Search domain, alias or path" aria-label="Search websites">
                </div>
            </div>
            <div class="col-sm-4 col-md-3">
                <select class="form-select form-select-sm" name="status" aria-label="Filter" data-wcp-autosubmit>
                    <?php foreach (['' => 'All websites', 'enabled' => 'Enabled', 'disabled' => 'Disabled (not enabled in Nginx)', 'https' => 'With HTTPS', 'nohttps' => 'Without HTTPS'] as $v => $label): ?>
                        <option value="<?= e($v) ?>"<?= $status === $v ? ' selected' : '' ?>><?= e($label) ?></option>
                    <?php endforeach; ?>
                </select>
            </div>
            <div class="col-auto"><button class="btn btn-sm btn-outline-secondary" type="submit">Filter</button></div>
        </form>
    </div>
    <?php if ($sites === []): ?>
        <div class="card-body text-center py-5 wcp-empty">
            <i class="bi bi-globe2 display-6 text-body-secondary"></i>
            <p class="mt-3 mb-0 text-body-secondary">
                <?= $total === 0 ? 'No websites were found in the Nginx configuration.' : 'No websites match your filter.' ?>
            </p>
        </div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table table-hover align-middle mb-0 wcp-table">
                <thead>
                <tr>
                    <th>Domain</th>
                    <th>Status</th>
                    <th>Type</th>
                    <th>PHP</th>
                    <th>SSL</th>
                    <th class="d-none d-xl-table-cell">Document root</th>
                    <th></th>
                </tr>
                </thead>
                <tbody>
                <?php foreach ($sites as $s): ?>
                    <tr>
                        <td>
                            <a class="fw-semibold" href="<?= e($this->url('websites.show', ['id' => (string) $s['id']])) ?>"><?= e($s['domain']) ?></a>
                            <?php if (!empty($s['aliases'])): ?>
                                <div class="small text-body-secondary">+ <?= e(implode(', ', array_slice($s['aliases'], 0, 3))) ?><?= count($s['aliases']) > 3 ? ' …' : '' ?></div>
                            <?php endif; ?>
                            <?php if (!empty($s['managed_by'])): ?>
                                <span class="badge text-bg-info-subtle border border-info-subtle text-info-emphasis mt-1" title="Detected from file locations">managed by <?= e($s['managed_by']) ?></span>
                            <?php endif; ?>
                        </td>
                        <td><?= !empty($s['enabled']) ? $this->statusBadge('enabled') : $this->statusBadge('disabled') ?></td>
                        <td><span class="badge text-bg-light border"><?= e($s['kind']) ?></span></td>
                        <td>
                            <?php if (!empty($s['php'])): ?>
                                <?= e($s['php']['version'] !== '' ? 'PHP ' . $s['php']['version'] : 'PHP (unknown)') ?>
                                <?php if (!empty($s['php']['pool'])): ?><div class="small text-body-secondary">pool <?= e($s['php']['pool']) ?></div><?php endif; ?>
                            <?php else: ?><span class="text-body-secondary">—</span><?php endif; ?>
                        </td>
                        <td>
                            <?php if (!empty($s['ssl'])): ?>
                                <?php $st = (int) $s['ssl']['days_remaining'] <= $warnDays && $s['ssl']['status'] === 'valid' ? 'expiring' : (string) $s['ssl']['status']; ?>
                                <?= $this->statusBadge($st, $st === 'error' ? 'error' : $s['ssl']['days_remaining'] . ' days') ?>
                                <?php if (!empty($s['https_redirect'])): ?><i class="bi bi-arrow-right-circle ms-1 text-success" title="HTTP redirects to HTTPS"></i><?php endif; ?>
                            <?php elseif (!empty($s['https'])): ?>
                                <?= $this->statusBadge('warning', 'HTTPS, no cert info') ?>
                            <?php else: ?>
                                <span class="text-body-secondary">none</span>
                            <?php endif; ?>
                        </td>
                        <td class="d-none d-xl-table-cell small">
                            <?php if (!empty($s['document_root'])): ?>
                                <code><?= e($s['document_root']) ?></code>
                                <?php if (empty($s['document_root_exists'])): ?><i class="bi bi-exclamation-circle text-warning" title="Directory does not exist"></i><?php endif; ?>
                            <?php elseif (!empty($s['proxy_pass'])): ?>
                                <span class="text-body-secondary">→ <?= e($s['proxy_pass']) ?></span>
                            <?php endif; ?>
                        </td>
                        <td class="text-end">
                            <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('websites.show', ['id' => (string) $s['id']])) ?>">Details</a>
                        </td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
    <div class="card-footer small text-body-secondary">
        Showing <?= count($sites) ?> of <?= (int) $total ?>.
        <?php if ($catchAll > 0): ?><?= (int) $catchAll ?> catch-all/default server block(s) are not listed as websites.<?php endif; ?>
    </div>
</div>
