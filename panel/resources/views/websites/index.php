<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $sites */
?>
<?php $this->start(); ?>Websites<?php $this->end('title'); ?>
<?php $this->start(); ?><?= $this->simpleLayout() ? count($managed) . ' website' . (count($managed) === 1 ? '' : 's') : count($managed) . ' managed by WhaCP' ?><?= $discoveredAt !== null ? ' · ' . (int) $total . ' other website(s) found on this server' : '' ?><?php $this->end('subtitle'); ?>

<?php $layout ??= 'grid'; $canCreate = $this->can(\WhaCP\Security\Permissions::WEBSITES_MANAGE); ?>
<div class="wcp-toolbar mb-3" data-wcp-cards-toolbar>
    <div class="input-group input-group-sm wcp-card-search">
        <span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span>
        <input class="form-control" type="search" placeholder="Search websites" aria-label="Search websites" data-wcp-cards-search="#wcp-sites">
    </div>
    <select class="form-select form-select-sm w-auto" aria-label="Sort" data-wcp-cards-sort="#wcp-sites">
        <option value="created">By creation date</option>
        <option value="name">By name</option>
    </select>
    <select class="form-select form-select-sm w-auto" aria-label="Show" data-wcp-cards-status="#wcp-sites">
        <option value="">All sites</option>
        <option value="active">Active</option>
        <option value="disabled">Disabled</option>
    </select>
    <span class="ms-auto"></span>
    <?php if (!$this->simpleLayout()): ?>
        <div class="btn-group btn-group-sm" role="group" aria-label="View">
            <a class="btn btn-outline-secondary<?= $layout === 'grid' ? ' active' : '' ?>" href="?view=grid" title="Cards" aria-label="Cards"<?= $layout === 'grid' ? ' aria-current="true"' : '' ?>><i class="bi bi-grid-3x3-gap"></i></a>
            <a class="btn btn-outline-secondary<?= $layout === 'list' ? ' active' : '' ?>" href="?view=list" title="List" aria-label="List"<?= $layout === 'list' ? ' aria-current="true"' : '' ?>><i class="bi bi-list-ul"></i></a>
        </div>
    <?php endif; ?>
    <?php if ($canCreate): ?><a class="btn btn-primary btn-sm" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1"></i>Create site</a><?php endif; ?>
</div>

<?php if ($managed === []): ?>
    <div class="card mb-4"><div class="wcp-empty-state">
        <i class="bi bi-globe2" aria-hidden="true"></i>
        <div class="fw-medium">No websites yet</div>
        <div class="small"><?= $canCreate ? 'Create your first website to get started.' : 'Your hosting provider has not set up a website for you yet.' ?></div>
        <?php if ($canCreate): ?><a class="btn btn-primary mt-3" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1"></i>Create site</a><?php endif; ?>
    </div></div>
<?php elseif ($layout === 'grid'): ?>
    <div class="row row-cols-1 row-cols-sm-2 row-cols-lg-3 row-cols-xxl-4 g-3 mb-4" id="wcp-sites" data-wcp-cards>
        <?php foreach ($managed as $m): ?>
            <?php
            $active = $m['enabled'] && $m['status'] !== 'error';
            $tint = abs(crc32((string) $m['domain'])) % 6;
            ?>
            <div class="col" data-wcp-card data-name="<?= e(strtolower((string) $m['domain'])) ?>" data-created="<?= e((string) ($m['created_at'] ?? '')) ?>" data-status="<?= $m['enabled'] ? 'active' : 'disabled' ?>" data-search="<?= e(strtolower($m['domain'] . ' ' . implode(' ', $m['aliases']))) ?>">
                <a class="card h-100 wcp-site-card text-decoration-none" href="<?= e($this->url('sites.show', ['id' => (int) $m['id']])) ?>">
                    <div class="wcp-site-thumb wcp-tint-<?= (int) $tint ?><?= $active ? '' : ' is-off' ?>">
                        <span class="wcp-site-initial" aria-hidden="true"><?= e(strtoupper(substr((string) $m['domain'], 0, 1))) ?></span>
                        <span class="wcp-site-badges">
                            <span class="wcp-site-badge <?= $m['https'] ? 'is-ok' : 'is-muted' ?>" title="<?= $m['https'] ? 'HTTPS on' : 'HTTPS off' ?>"><i class="bi bi-<?= $m['https'] ? 'lock-fill' : 'unlock' ?>" aria-hidden="true"></i></span>
                            <?php if ($m['kind'] === 'php'): ?><span class="wcp-site-badge" title="PHP <?= e($m['php_version']) ?>">PHP <?= e($m['php_version']) ?></span><?php endif; ?>
                        </span>
                        <?php if (!$m['enabled']): ?><span class="wcp-site-ribbon">Disabled</span><?php elseif ($m['status'] === 'error'): ?><span class="wcp-site-ribbon is-error">Needs attention</span><?php endif; ?>
                    </div>
                    <div class="card-body py-2">
                        <div class="fw-semibold text-body text-truncate"><i class="bi bi-globe2 me-1 text-body-secondary" aria-hidden="true"></i><?= e($m['domain']) ?></div>
                        <div class="small text-body-secondary text-truncate"><?= $m['aliases'] !== [] ? '+ ' . e(implode(', ', array_slice($m['aliases'], 0, 2))) : e(\WhaCP\Services\WebsiteService::KINDS[$m['kind']] ?? $m['kind']) ?></div>
                    </div>
                </a>
            </div>
        <?php endforeach; ?>
        <div class="col-12 d-none" data-wcp-cards-empty><div class="text-body-secondary small">No website matches.</div></div>
    </div>
<?php else: ?>
<div class="card mb-4">
    <div class="card-header"><i class="bi bi-stars me-1"></i>Managed by WhaCP</div>
        <div class="table-responsive">
            <table class="table table-hover align-middle mb-0 wcp-table" id="wcp-sites" data-wcp-cards>
                <thead><tr><th>Domain</th><th>Status</th><th>Type</th><th>HTTPS</th><th>User</th><th class="d-none d-lg-table-cell">Owner</th><th></th></tr></thead>
                <tbody>
                <?php foreach ($managed as $m): ?>
                    <tr data-wcp-card data-name="<?= e(strtolower((string) $m['domain'])) ?>" data-created="<?= e((string) ($m['created_at'] ?? '')) ?>" data-status="<?= $m['enabled'] ? 'active' : 'disabled' ?>" data-search="<?= e(strtolower($m['domain'] . ' ' . implode(' ', $m['aliases']))) ?>">
                        <td><a class="fw-semibold" href="<?= e($this->url('sites.show', ['id' => (int) $m['id']])) ?>"><?= e($m['domain']) ?></a>
                            <?php if ($m['aliases'] !== []): ?><div class="small text-body-secondary">+ <?= e(implode(', ', array_slice($m['aliases'], 0, 3))) ?></div><?php endif; ?></td>
                        <td><?= $m['status'] === 'error' ? $this->statusBadge('error') : ($m['enabled'] ? $this->statusBadge('enabled') : $this->statusBadge('disabled')) ?></td>
                        <td><span class="badge text-bg-light border"><?= e($m['kind']) ?><?= $m['kind'] === 'php' ? ' ' . e($m['php_version']) : '' ?></span></td>
                        <td><?= $m['https'] ? $this->statusBadge('valid', 'on') : '<span class="text-body-secondary">off</span>' ?></td>
                        <td><code class="small"><?= e($m['system_user']) ?></code></td>
                        <td class="d-none d-lg-table-cell small"><?= e($m['owner_name'] ?? '—') ?></td>
                        <td class="text-end"><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.show', ['id' => (int) $m['id']])) ?>">Manage</a></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
</div>
<?php endif; ?>

<?php if ($discoveredAt === null) { return; } ?>
<h2 class="h6 text-body-secondary mb-2"><i class="bi bi-binoculars me-1"></i>Other websites on this server (read-only)</h2>
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
                <?= $total !== 0 ? 'No websites match your filter.' : ($managed !== [] ? 'No other websites were found in the Nginx configuration. Websites managed by WhaCP are listed above.' : 'No websites were found in the Nginx configuration.') ?>
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
