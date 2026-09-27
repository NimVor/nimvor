<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $backups */
/** @var array<string, mixed>|null $site  website for restore/delete routes; null = server backups */
/** @var bool $canManage */
/** @var bool $showSite */
$showSite ??= false;
?>
<?php if ($backups === []): ?>
    <div class="wcp-empty-state"><i class="bi bi-archive" aria-hidden="true"></i><div class="fw-medium">No backups yet</div><div class="small">A backup keeps a copy of your files and databases you can restore at any time.</div></div>
<?php else: ?>
    <div class="table-responsive"><table class="table table-sm align-middle mb-0">
        <thead><tr><th>Created</th><?php if ($showSite): ?><th>Website</th><?php endif; ?><th>Contents</th><th>Size</th><th>Stored</th><th>Status</th><?php if ($canManage): ?><th class="text-end">Actions</th><?php endif; ?></tr></thead>
        <tbody>
        <?php foreach ($backups as $b):
            $isServer = $b['kind'] === 'server';
            $params = $isServer ? ['backup' => (int) $b['id']] : ['id' => (int) $b['website_id'], 'backup' => (int) $b['id']];
            $dbParts = array_values(array_filter($b['parts'], static fn ($p) => ($p['kind'] ?? '') === 'database'));
            $hasFiles = array_filter($b['parts'], static fn ($p) => ($p['kind'] ?? '') === 'files') !== [];
        ?>
            <tr>
                <td class="small text-nowrap"><?= e(substr((string) $b['created_at'], 0, 16)) ?><?php if ($b['label'] !== ''): ?><div class="text-body-secondary"><?= e($b['label']) ?></div><?php endif; ?></td>
                <?php if ($showSite): ?><td class="small"><a href="<?= e($this->url('sites.backups', ['id' => (int) $b['website_id']])) ?>"><?= e($b['domain']) ?></a></td><?php endif; ?>
                <td class="small">
                    <?php foreach ($b['parts'] as $p): ?>
                        <div><?php if ($canManage && $b['local']): ?><a href="<?= e($this->url($isServer ? 'backups.server.download' : 'sites.backups.download', $params, ['file' => $p['name']])) ?>" title="Download"><?php endif; ?><i class="bi bi-<?= ($p['kind'] ?? '') === 'database' ? 'database' : (($p['kind'] ?? '') === 'config' ? 'gear' : 'folder') ?> me-1"></i><?= e(($p['kind'] ?? '') === 'database' ? $p['database'] : (($p['kind'] ?? '') === 'config' ? 'configuration' : 'files')) ?><?php if ($canManage && $b['local']): ?></a><?php endif; ?></div>
                    <?php endforeach; ?>
                    <?php if ($b['error'] !== ''): ?><div class="text-danger"><?= e($b['error']) ?></div><?php endif; ?>
                </td>
                <td class="small text-nowrap"><?= $b['status'] === 'completed' ? e(format_bytes((int) $b['size'])) : '' ?></td>
                <td class="small"><?= $b['local'] ? 'server' : '' ?><?= $b['local'] && $b['storage_id'] !== null ? ' + ' : '' ?><?= $b['storage_id'] !== null ? e((string) ($b['storage_name'] ?? 'remote')) : '' ?><?= $b['encrypted'] ? ' <i class="bi bi-lock" title="Encrypted"></i>' : '' ?></td>
                <td><?= $this->statusBadge($b['status'] === 'running' ? 'activating' : (string) $b['status'], (string) $b['status']) ?><?php if ($b['status'] === 'running' && $b['job_id'] !== ''): ?> <button type="button" class="btn btn-sm btn-link p-0" data-wcp-job-open="<?= e($b['job_id']) ?>">log</button><?php endif; ?></td>
                <?php if ($canManage): ?>
                    <td class="text-end">
                        <?php if (!$isServer && $b['status'] === 'completed'): ?>
                            <details class="d-inline-block text-start wcp-restore">
                                <summary class="btn btn-sm btn-outline-secondary">Restore</summary>
                                <form class="card card-body mt-1 small" method="post" action="<?= e($this->url('sites.backups.restore', $params)) ?>">
                                    <?= $this->csrfField() ?>
                                    <?php if ($hasFiles): ?><div class="form-check"><input class="form-check-input" type="checkbox" name="restore_files" value="1" id="rf-<?= (int) $b['id'] ?>" checked><label class="form-check-label" for="rf-<?= (int) $b['id'] ?>">Website files (overwrites changed files)</label></div><?php endif; ?>
                                    <?php foreach ($dbParts as $p): ?><div class="form-check"><input class="form-check-input" type="checkbox" name="restore_databases[]" value="<?= e($p['database']) ?>" id="rd-<?= (int) $b['id'] ?>-<?= e($p['database']) ?>"><label class="form-check-label" for="rd-<?= (int) $b['id'] ?>-<?= e($p['database']) ?>">Database <?= e($p['database']) ?> (replaces its tables)</label></div><?php endforeach; ?>
                                    <label class="form-label mt-2" for="rc-<?= (int) $b['id'] ?>">Type <strong><?= e($b['domain']) ?></strong> to confirm</label>
                                    <input class="form-control form-control-sm mb-2" id="rc-<?= (int) $b['id'] ?>" name="confirm_domain" autocomplete="off" required>
                                    <button class="btn btn-sm btn-warning">Restore now</button>
                                </form>
                            </details>
                        <?php endif; ?>
                        <?php if ($isServer && $b['status'] === 'completed' && $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE)): ?>
                            <details class="d-inline-block text-start wcp-restore">
                                <summary class="btn btn-sm btn-outline-secondary">Restore</summary>
                                <form class="card card-body mt-1 small" method="post" action="<?= e($this->url('backups.server.restore', $params)) ?>">
                                    <?= $this->csrfField() ?>
                                    <p class="mb-2">Each part replaces its folder as a whole. A safety backup is taken first; the configuration is validated and rolled back if a service does not accept it. Firewall, SSH, systemd units and WhaCP's own settings are never restored automatically.</p>
                                    <?php foreach (\WhaCP\Services\BackupService::SERVER_COMPONENTS as $k => $label): ?>
                                        <div class="form-check"><input class="form-check-input" type="checkbox" name="components[]" value="<?= e($k) ?>" id="sc-<?= (int) $b['id'] ?>-<?= e($k) ?>"><label class="form-check-label" for="sc-<?= (int) $b['id'] ?>-<?= e($k) ?>"><?= e($label) ?></label></div>
                                    <?php endforeach; ?>
                                    <label class="form-label mt-2" for="scc-<?= (int) $b['id'] ?>">Type <strong>restore</strong> to confirm</label>
                                    <input class="form-control form-control-sm mb-2" id="scc-<?= (int) $b['id'] ?>" name="confirm" autocomplete="off">
                                    <div class="d-flex gap-2">
                                        <button class="btn btn-sm btn-warning">Restore now</button>
                                        <button class="btn btn-sm btn-outline-secondary" name="extract_only" value="1">Extract for review only</button>
                                    </div>
                                </form>
                            </details>
                        <?php endif; ?>
                        <?php if ($b['status'] !== 'running'): ?>
                            <form class="d-inline" method="post" action="<?= e($this->url($isServer ? 'backups.server.delete' : 'sites.backups.delete', $params)) ?>" data-wcp-confirm="Delete this backup<?= $b['storage_id'] !== null ? ' here and in remote storage' : '' ?>?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button></form>
                        <?php endif; ?>
                    </td>
                <?php endif; ?>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table></div>
<?php endif; ?>
