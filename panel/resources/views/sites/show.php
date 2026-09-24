<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
use WhaCP\Security\Permissions as P;

$canManage = $this->can(P::WEBSITES_MANAGE);
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'overview']) ?>

<?php if ($site['status'] === 'error' && $site['last_error'] !== ''): ?>
    <div class="alert alert-danger small"><strong>Last change failed:</strong> <?= e($site['last_error']) ?></div>
<?php endif; ?>
<?php if ($inspectError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $inspectError]) ?>
<?php elseif ($inspect !== null && !$inspect['config_exists']): ?>
    <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle me-1"></i>The Nginx configuration for this website is missing on the server. Use <strong>Re-apply configuration</strong> to recreate it.</div>
<?php endif; ?>

<?php if (!empty($import)): ?>
    <?php if ($import['status'] === 'running'): ?>
        <div class="alert alert-info small"><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Importing from <code><?= e($import['source']) ?></code>. The old website keeps serving visitors until the switch. Reload this page to see the result.</div>
    <?php elseif ($import['status'] === 'succeeded'): ?>
        <div class="alert alert-secondary small d-flex flex-wrap align-items-center gap-2">
            <span class="me-auto"><i class="bi bi-box-arrow-in-down me-1"></i>Imported from <code><?= e($import['source']) ?></code> on <?= e(substr((string) $import['created_at'], 0, 10)) ?>. The original files and configuration are kept.</span>
            <?php if ($this->can(P::IMPORT_RUN)): ?>
                <form method="post" action="<?= e($this->url('sites.import.undo', ['id' => $site['id']])) ?>" data-wcp-confirm="Undo the import? The original configuration will serve <?= e($site['domain']) ?> again, and the WhaCP copy (files in <?= e($site['root']) ?>, its account and settings) will be deleted. Changes made since the import are lost.">
                    <?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Undo import</button>
                </form>
            <?php endif; ?>
        </div>
    <?php endif; ?>
<?php endif; ?>

<div class="row g-3">
    <div class="col-lg-7">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-info-circle me-1"></i>Website</div>
            <div class="card-body">
                <dl class="wcp-dl">
                    <dt>Domain</dt><dd><?= e($site['domain']) ?></dd>
                    <dt>Aliases</dt><dd><?= $site['aliases'] === [] ? '<span class="text-body-secondary">none</span>' : e(implode(', ', $site['aliases'])) ?></dd>
                    <dt>Type</dt><dd><?= e(\WhaCP\Services\WebsiteService::KINDS[$site['kind']] ?? $site['kind']) ?></dd>
                    <?php if ($site['kind'] === 'php'): ?><dt>PHP</dt><dd><?= e($site['php_version']) ?> · <?= (int) ($site['php_settings']['memory_limit_mb'] ?? 256) ?> MB · up to <?= (int) ($site['php_settings']['max_children'] ?? 5) ?> processes</dd><?php endif; ?>
                    <?php if ($site['kind'] === 'proxy'): ?><dt>Upstream</dt><dd><code><?= e($site['proxy_pass']) ?></code></dd><?php endif; ?>
                    <dt>HTTPS</dt><dd><?= $site['https'] ? $this->statusBadge('valid', 'enabled') . ($site['force_https'] ? ' <span class="small">(HTTP redirects to HTTPS)</span>' : '') : '<span class="text-body-secondary">not enabled</span>' ?></dd>
                    <dt>System user</dt><dd><code><?= e($site['system_user']) ?></code></dd>
                    <dt>Site root</dt><dd><code><?= e($site['root']) ?></code></dd>
                    <dt>Document root</dt><dd><code><?= e($site['document_root']) ?></code></dd>
                    <?php if ($inspect !== null): ?>
                        <dt>Disk usage</dt><dd><?= e(format_bytes((int) $inspect['disk_bytes'])) ?><?= empty($inspect['disk_complete']) ? '+' : '' ?></dd>
                    <?php endif; ?>
                    <?php if (!empty($site['owner_name'])): ?><dt>Owner</dt><dd><?= e($site['owner_name']) ?></dd><?php endif; ?>
                    <dt>Last applied</dt><dd><?= e(time_ago($site['applied_at'])) ?></dd>
                </dl>
            </div>
        </div>
        <?php if ($inspect !== null && !empty($inspect['config_preview'])): ?>
            <div class="card">
                <div class="card-header"><i class="bi bi-file-earmark-code me-1"></i>Generated Nginx configuration</div>
                <div class="card-body"><pre class="wcp-pre mb-0"><?= e($inspect['config_preview']) ?></pre></div>
            </div>
        <?php endif; ?>
    </div>
    <div class="col-lg-5">
        <?php if ($canManage): ?>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-lightning me-1"></i>Actions</div>
                <div class="card-body d-grid gap-2">
                    <form method="post" action="<?= e($this->url('sites.toggle', ['id' => (int) $site['id']])) ?>" data-wcp-busy
                          <?= $site['enabled'] ? 'data-wcp-confirm="Disable ' . e($site['domain']) . '? Visitors will no longer reach it. Files, databases and settings are kept."' : '' ?>>
                        <?= $this->csrfField() ?>
                        <button class="btn btn-outline-<?= $site['enabled'] ? 'warning' : 'success' ?> w-100" type="submit"><i class="bi bi-power me-1"></i><?= $site['enabled'] ? 'Disable website' : 'Enable website' ?></button>
                    </form>
                    <?php if ($site['kind'] === 'php' && $site['enabled']): ?>
                        <form method="post" action="<?= e($this->url('sites.restart_php', ['id' => (int) $site['id']])) ?>" data-wcp-busy>
                            <?= $this->csrfField() ?>
                            <button class="btn btn-outline-secondary w-100" type="submit"><i class="bi bi-arrow-clockwise me-1"></i>Reload PHP <?= e($site['php_version']) ?></button>
                        </form>
                    <?php endif; ?>
                    <form method="post" action="<?= e($this->url('sites.reapply', ['id' => (int) $site['id']])) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <button class="btn btn-outline-secondary w-100" type="submit"><i class="bi bi-arrow-repeat me-1"></i>Re-apply configuration</button>
                    </form>
                </div>
            </div>
        <?php endif; ?>
        <?php if ($jobs !== []): ?>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-list-task me-1"></i>Recent tasks</div>
                <ul class="list-group list-group-flush small">
                    <?php foreach ($jobs as $j): ?>
                        <li class="list-group-item d-flex justify-content-between">
                            <a href="#" data-wcp-job-open="<?= e($j['job_id']) ?>"><?= e($j['kind']) ?></a>
                            <span><?= $this->statusBadge((string) $j['status']) ?> <span class="text-body-secondary"><?= e(time_ago((string) $j['created_at'])) ?></span></span>
                        </li>
                    <?php endforeach; ?>
                </ul>
            </div>
        <?php endif; ?>
        <?php if ($canManage): ?>
            <div class="card border-danger-subtle">
                <div class="card-header text-danger"><i class="bi bi-trash me-1"></i>Delete website</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('sites.delete', ['id' => (int) $site['id']])) ?>"
                          data-wcp-confirm="Delete <?= e($site['domain']) ?>? The Nginx and PHP configuration is removed. This cannot be undone from the panel.">
                        <?= $this->csrfField() ?>
                        <label class="form-label small" for="cd">Type <code><?= e($site['domain']) ?></code> to confirm</label>
                        <input class="form-control form-control-sm mb-2" id="cd" name="confirm_domain" autocomplete="off" required>
                        <div class="form-check small"><input class="form-check-input" type="checkbox" name="remove_files" value="1" id="rf"><label class="form-check-label" for="rf">Move the website files to the trash (<code>/var/lib/whacp-agent/trash</code>)</label></div>
                        <div class="form-check small mb-3"><input class="form-check-input" type="checkbox" name="remove_user" value="1" id="ru"><label class="form-check-label" for="ru">Remove the system user <code><?= e($site['system_user']) ?></code> if no other website uses it</label></div>
                        <button class="btn btn-outline-danger w-100" type="submit">Delete website…</button>
                    </form>
                </div>
            </div>
        <?php endif; ?>
    </div>
</div>
