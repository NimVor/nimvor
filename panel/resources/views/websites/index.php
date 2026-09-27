<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $sites */
use WhaCP\Security\Permissions as P;

$layout ??= 'grid';
$backups ??= [];
$ssl ??= [];
$nginxVersion ??= null;
$canCreate = $this->can(P::WEBSITES_MANAGE);
$showOwner = $this->can(P::USERS_VIEW);
?>
<?php $this->start(); ?>Websites<?php $this->end('title'); ?>
<?php $this->start(); ?><?= $this->simpleLayout() ? count($managed) . ' website' . (count($managed) === 1 ? '' : 's') : count($managed) . ' managed by WhaCP' ?><?= $discoveredAt !== null ? ' · ' . (int) $total . ' other website(s) found on this server' : '' ?><?php $this->end('subtitle'); ?>

<?php if ($layout === 'grid'): ?>
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
            <a class="btn btn-outline-secondary active" href="?view=grid" title="Cards" aria-label="Cards" aria-current="true"><i class="bi bi-grid-3x3-gap"></i></a>
            <a class="btn btn-outline-secondary" href="?view=list" title="List" aria-label="List"><i class="bi bi-list-ul"></i></a>
        </div>
    <?php endif; ?>
    <?php if ($canCreate): ?><a class="btn btn-primary btn-sm" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1"></i>Create site</a><?php endif; ?>
</div>
<?php endif; ?>

<?php if ($managed === [] && $layout === 'grid'): ?>
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
<div class="wcp-panel mb-4" data-wcp-dt id="wcp-sites-dt">
    <div class="wcp-actionbar">
        <?php if ($canCreate): ?><a class="btn btn-primary btn-sm" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1"></i>Add site</a><?php endif; ?>
        <?php if ($discoveredAt !== null && $total > 0): ?><a class="btn btn-outline-secondary btn-sm" href="#other-websites"><i class="bi bi-box-arrow-in-down me-1"></i>Import existing (<?= (int) $total ?>)</a><?php endif; ?>
        <?php if ($nginxVersion !== null && $nginxVersion !== ''): ?>
            <?php $svcTag = $this->can(P::SERVICES_VIEW) && $this->hasRoute('services') ? 'a' : 'span'; ?>
            <<?= $svcTag ?> class="wcp-svc"<?= $svcTag === 'a' ? ' href="' . e($this->url('services')) . '"' : '' ?> title="Web server"><i class="bi bi-hdd-rack" aria-hidden="true"></i>Nginx <?= e($nginxVersion) ?> <i class="bi bi-play-fill" aria-hidden="true"></i></<?= $svcTag ?>>
        <?php endif; ?>
        <div class="wcp-actionbar-end">
            <select class="form-select form-select-sm w-auto" aria-label="Show" data-wcp-dt-select="status">
                <option value="">All sites</option>
                <option value="active">Running</option>
                <option value="disabled">Disabled</option>
                <option value="nossl">Without HTTPS</option>
            </select>
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Domain or user" aria-label="Search websites" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
            <div class="btn-group btn-group-sm" role="group" aria-label="View">
                <a class="btn btn-outline-secondary" href="?view=grid" title="Cards" aria-label="Cards"><i class="bi bi-grid-3x3-gap"></i></a>
                <a class="btn btn-outline-secondary active" href="?view=list" title="List" aria-label="List" aria-current="true"><i class="bi bi-list-ul"></i></a>
            </div>
        </div>
    </div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle" id="wcp-sites">
            <thead><tr>
                <th>Site name</th><th class="text-center">Status</th><th>Backup</th><th>Quick action</th><th>SSL</th>
                <?php if ($showOwner): ?><th class="d-none d-lg-table-cell">Owner</th><?php endif; ?>
                <th class="text-end">Operate</th>
            </tr></thead>
            <tbody>
            <?php foreach ($managed as $m):
                $id = (int) $m['id'];
                $scheme = $m['https'] ? 'https://' : 'http://';
                $n = (int) ($backups[$id] ?? 0);
                $cert = $ssl[strtolower((string) $m['domain'])] ?? null;
                $tags = [$m['enabled'] ? 'active' : 'disabled'];
                if (!$m['https']) {
                    $tags[] = 'nossl';
                }
                ?>
                <tr data-wcp-row data-dt-status="<?= e(implode(' ', $tags)) ?>" data-search="<?= e(strtolower($m['domain'] . ' ' . implode(' ', $m['aliases']) . ' ' . $m['system_user'] . ' ' . ($m['owner_name'] ?? ''))) ?>">
                    <td>
                        <a class="fw-semibold" href="<?= e($this->url('sites.show', ['id' => $id])) ?>"><?= e($m['domain']) ?></a>
                        <?php if ($m['enabled']): ?><a class="ms-1 small" href="<?= e($scheme . $m['domain']) ?>/" target="_blank" rel="noopener noreferrer" title="Open the website" aria-label="Open <?= e($m['domain']) ?>"><i class="bi bi-box-arrow-up-right"></i></a><?php endif; ?>
                        <div class="wcp-sub"><?= e($m['system_user']) ?><?= $m['aliases'] !== [] ? ' · +' . count($m['aliases']) . ' alias' . (count($m['aliases']) === 1 ? '' : 'es') : '' ?></div>
                    </td>
                    <td class="text-center">
                        <?php $title = $m['status'] === 'error' ? 'Needs attention' : ($m['enabled'] ? 'Running' : 'Disabled'); ?>
                        <?php if ($canCreate): ?>
                            <form method="post" action="<?= e($this->url('sites.toggle', ['id' => $id])) ?>"<?= $m['enabled'] ? ' data-wcp-confirm="Disable ' . e($m['domain']) . '? Visitors get an error page until you enable it again; files and settings are kept."' : '' ?>>
                                <?= $this->csrfField() ?><input type="hidden" name="return_to" value="websites">
                                <button class="wcp-toggle-op <?= $m['enabled'] ? 'is-on' : 'is-off' ?>" type="submit" title="<?= e($title) ?> — click to <?= $m['enabled'] ? 'disable' : 'enable' ?>" aria-label="<?= $m['enabled'] ? 'Disable' : 'Enable' ?> <?= e($m['domain']) ?>"><i class="bi bi-<?= $m['enabled'] ? 'play-circle-fill' : 'pause-circle' ?>"></i></button>
                            </form>
                        <?php else: ?>
                            <span class="wcp-toggle-op <?= $m['enabled'] ? 'is-on' : 'is-off' ?>" title="<?= e($title) ?>"><i class="bi bi-<?= $m['enabled'] ? 'play-circle-fill' : 'pause-circle' ?>" aria-hidden="true"></i><span class="visually-hidden"><?= e($title) ?></span></span>
                        <?php endif; ?>
                        <?php if ($m['status'] === 'error'): ?><div class="small wcp-bad">Needs attention</div><?php endif; ?>
                    </td>
                    <td class="text-nowrap">
                        <?php if ($this->can(P::BACKUPS_VIEW)): ?>
                            <a href="<?= e($this->url('sites.backups', ['id' => $id])) ?>"><span class="<?= $n > 0 ? 'wcp-good' : 'wcp-warn' ?> fw-semibold"><?= $n ?></span> <span class="<?= $n > 0 ? '' : 'wcp-warn' ?>">Backup<?= $n === 1 ? '' : 's' ?></span></a>
                            <?php if ($this->can(P::BACKUPS_MANAGE)): ?><div class="wcp-sub"><a class="wcp-muted" href="<?= e($this->url('sites.backups', ['id' => $id])) ?>#backup-now">Click to back up</a></div><?php endif; ?>
                        <?php else: ?><span class="wcp-muted">—</span><?php endif; ?>
                    </td>
                    <td class="text-nowrap">
                        <?php if ($this->can(P::FILES_MANAGE)): ?><a class="wcp-icon-op" href="<?= e($this->url('sites.files', ['id' => $id])) ?>" title="File manager" aria-label="Files of <?= e($m['domain']) ?>"><i class="bi bi-folder2-open"></i></a><?php endif; ?>
                        <?php if ($this->can(P::DATABASES_VIEW)): ?><a class="wcp-icon-op" href="<?= e($this->url('sites.databases', ['id' => $id])) ?>" title="Databases" aria-label="Databases of <?= e($m['domain']) ?>"><i class="bi bi-database"></i></a><?php endif; ?>
                        <?php if ($this->can(P::LOGS_VIEW)): ?><a class="wcp-icon-op" href="<?= e($this->url('sites.stats', ['id' => $id])) ?>" title="Statistics" aria-label="Statistics of <?= e($m['domain']) ?>"><i class="bi bi-speedometer2"></i></a><?php endif; ?>
                        <?php if ($m['kind'] === 'php'): ?>
                            <?php if ($canCreate): ?><a class="ms-1 small fw-semibold" href="<?= e($this->url('sites.edit', ['id' => $id])) ?>" title="PHP version">PHP <?= e($m['php_version']) ?></a><?php else: ?><span class="ms-1 small">PHP <?= e($m['php_version']) ?></span><?php endif; ?>
                        <?php else: ?><span class="ms-1 small wcp-muted"><?= $m['kind'] === 'proxy' ? 'Proxy' : 'Static' ?></span><?php endif; ?>
                    </td>
                    <td class="text-nowrap small">
                        <?php $sslLink = $this->can(P::SSL_VIEW) ? $this->url('sites.ssl', ['id' => $id]) : null; ?>
                        <?php if (!$m['https']): ?>
                            <?= $sslLink !== null ? '<a class="wcp-muted" href="' . e($sslLink) . '">Not set</a>' : '<span class="wcp-muted">Not set</span>' ?>
                        <?php elseif ($cert !== null && $cert['status'] !== 'error'): ?>
                            <?php $cls = $cert['days'] <= 0 ? 'wcp-bad' : ($cert['days'] <= $warnDays ? 'wcp-warn' : 'wcp-good'); $label = $cert['days'] <= 0 ? 'Expired' : $cert['days'] . ' Days'; ?>
                            <?= $sslLink !== null ? '<a class="' . $cls . '" href="' . e($sslLink) . '">' . e($label) . '</a>' : '<span class="' . $cls . '">' . e($label) . '</span>' ?>
                        <?php else: ?>
                            <?= $sslLink !== null ? '<a class="wcp-good" href="' . e($sslLink) . '">On</a>' : '<span class="wcp-good">On</span>' ?>
                        <?php endif; ?>
                    </td>
                    <?php if ($showOwner): ?><td class="d-none d-lg-table-cell small"><?= e($m['owner_name'] ?? '—') ?></td><?php endif; ?>
                    <td class="text-end">
                        <span class="wcp-ops">
                            <a class="wcp-op" href="<?= e($this->url('sites.show', ['id' => $id])) ?>">Manage</a>
                            <?php if ($this->can(P::LOGS_VIEW)): ?><a class="wcp-op" href="<?= e($this->url('sites.logs', ['id' => $id])) ?>">Log</a><?php endif; ?>
                            <?php if ($canCreate): ?>
                                <details data-wcp-menu>
                                    <summary class="wcp-op-more" title="More" aria-label="More actions for <?= e($m['domain']) ?>"><i class="bi bi-three-dots-vertical"></i></summary>
                                    <div class="wcp-dropmenu">
                                        <a href="<?= e($this->url('sites.edit', ['id' => $id])) ?>"><i class="bi bi-sliders"></i>PHP &amp; settings</a>
                                        <?php if ($this->hasRoute('sites.domains')): ?><a href="<?= e($this->url('sites.domains', ['id' => $id])) ?>"><i class="bi bi-diagram-3"></i>Subdomains &amp; addons</a><?php endif; ?>
                                        <a href="<?= e($this->url('sites.advanced', ['id' => $id])) ?>"><i class="bi bi-tools"></i>Advanced</a>
                                        <a class="is-danger" href="<?= e($this->url('sites.advanced', ['id' => $id])) ?>#delete-website"><i class="bi bi-trash"></i>Delete website</a>
                                    </div>
                                </details>
                            <?php endif; ?>
                        </span>
                    </td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $managed === [] ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="<?= $showOwner ? 7 : 6 ?>" class="text-center py-4 wcp-muted"><?= $managed === [] ? ($canCreate ? 'No websites yet. Use “Add site” to create one.' : 'No websites yet.') : 'No website matches.' ?></td></tr>
            </tbody>
        </table>
    </div>
    <div class="wcp-pager" data-wcp-dt-pager></div>
</div>
<?php endif; ?>

<?php if ($discoveredAt === null) { return; } ?>
<h2 class="h6 text-body-secondary mb-2" id="other-websites"><i class="bi bi-binoculars me-1"></i>Other websites on this server (read-only)</h2>
<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<?php foreach ($warnings as $w): ?>
    <div class="alert alert-warning py-2 small"><i class="bi bi-exclamation-triangle me-1"></i><?= e($w) ?></div>
<?php endforeach; ?>

<div class="wcp-panel" data-wcp-dt id="wcp-other-dt">
    <div class="wcp-actionbar">
        <span class="small wcp-muted"><?= (int) $total ?> found in the Nginx configuration<?= $catchAll > 0 ? ' · ' . (int) $catchAll . ' catch-all/default server block(s) not listed' : '' ?></span>
        <div class="wcp-actionbar-end">
            <select class="form-select form-select-sm w-auto" aria-label="Filter" data-wcp-dt-select="state">
                <?php foreach (['' => 'All websites', 'enabled' => 'Enabled', 'disabled' => 'Disabled (not enabled in Nginx)', 'https' => 'With HTTPS', 'nohttps' => 'Without HTTPS'] as $v => $label): ?>
                    <option value="<?= e($v) ?>"<?= $status === $v ? ' selected' : '' ?>><?= e($label) ?></option>
                <?php endforeach; ?>
            </select>
            <div class="wcp-search"><input class="form-control form-control-sm" type="search" value="<?= e($q) ?>" placeholder="Domain, alias or path" aria-label="Search other websites" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div>
        </div>
    </div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Domain</th><th>Status</th><th>Type</th><th>PHP</th><th>SSL</th><th class="d-none d-xl-table-cell">Document root</th><th class="text-end">Operate</th></tr></thead>
            <tbody>
            <?php foreach ($sites as $s): ?>
                <tr data-wcp-row data-dt-state="<?= !empty($s['enabled']) ? 'enabled' : 'disabled' ?> <?= !empty($s['https']) ? 'https' : 'nohttps' ?>" data-search="<?= e(strtolower(implode(' ', array_merge([(string) $s['domain']], (array) ($s['aliases'] ?? []), [(string) ($s['document_root'] ?? '')])))) ?>">
                    <td>
                        <a class="fw-semibold" href="<?= e($this->url('websites.show', ['id' => (string) $s['id']])) ?>"><?= e($s['domain']) ?></a>
                        <?php if (!empty($s['aliases'])): ?><div class="wcp-sub">+ <?= e(implode(', ', array_slice($s['aliases'], 0, 3))) ?><?= count($s['aliases']) > 3 ? ' …' : '' ?></div><?php endif; ?>
                        <?php if (!empty($s['managed_by'])): ?><span class="badge text-bg-info-subtle border border-info-subtle text-info-emphasis mt-1" title="Detected from file locations">managed by <?= e($s['managed_by']) ?></span><?php endif; ?>
                    </td>
                    <td><?= !empty($s['enabled']) ? $this->statusBadge('enabled') : $this->statusBadge('disabled') ?></td>
                    <td><span class="badge text-bg-light border"><?= e($s['kind']) ?></span></td>
                    <td>
                        <?php if (!empty($s['php'])): ?>
                            <?= e($s['php']['version'] !== '' ? 'PHP ' . $s['php']['version'] : 'PHP (unknown)') ?>
                            <?php if (!empty($s['php']['pool'])): ?><div class="wcp-sub">pool <?= e($s['php']['pool']) ?></div><?php endif; ?>
                        <?php else: ?><span class="wcp-muted">—</span><?php endif; ?>
                    </td>
                    <td class="small">
                        <?php if (!empty($s['ssl'])): ?>
                            <?php $days = (int) $s['ssl']['days_remaining']; ?>
                            <?php if ($s['ssl']['status'] === 'error'): ?><span class="wcp-bad">Error</span><?php else: ?><span class="<?= $days <= 0 ? 'wcp-bad' : ($days <= $warnDays ? 'wcp-warn' : 'wcp-good') ?>"><?= $days <= 0 ? 'Expired' : $days . ' Days' ?></span><?php endif; ?>
                            <?php if (!empty($s['https_redirect'])): ?><i class="bi bi-arrow-right-circle ms-1 text-success" title="HTTP redirects to HTTPS"></i><?php endif; ?>
                        <?php elseif (!empty($s['https'])): ?>
                            <span class="wcp-warn">HTTPS, no cert info</span>
                        <?php else: ?>
                            <span class="wcp-muted">Not set</span>
                        <?php endif; ?>
                    </td>
                    <td class="d-none d-xl-table-cell small">
                        <?php if (!empty($s['document_root'])): ?>
                            <code><?= e($s['document_root']) ?></code>
                            <?php if (empty($s['document_root_exists'])): ?><i class="bi bi-exclamation-circle text-warning" title="Directory does not exist"></i><?php endif; ?>
                        <?php elseif (!empty($s['proxy_pass'])): ?>
                            <span class="wcp-muted">→ <?= e($s['proxy_pass']) ?></span>
                        <?php endif; ?>
                    </td>
                    <td class="text-end">
                        <span class="wcp-ops">
                            <a class="wcp-op" href="<?= e($this->url('websites.show', ['id' => (string) $s['id']])) ?>">Details</a>
                            <?php if ($this->can(P::IMPORT_RUN) && $this->hasRoute('websites.import')): ?><a class="wcp-op" href="<?= e($this->url('websites.import', ['id' => (string) $s['id']])) ?>">Import</a><?php endif; ?>
                        </span>
                    </td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $sites === [] ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="7" class="text-center py-4 wcp-muted">
                <?= $sites === [] && $total === 0 ? ($managed !== [] ? 'No other websites were found in the Nginx configuration. Websites managed by WhaCP are listed above.' : 'No websites were found in the Nginx configuration.') : 'No websites match your filter.' ?>
            </td></tr>
            </tbody>
        </table>
    </div>
    <div class="wcp-pager" data-wcp-dt-pager></div>
</div>
