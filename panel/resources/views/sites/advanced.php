<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
$id = (int) $site['id'];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'advanced']) ?>

<?php if ($site['status'] === 'error' && $site['last_error'] !== ''): ?>
    <div class="alert alert-danger small"><strong>The last change failed:</strong> <?= e($site['last_error']) ?></div>
<?php endif; ?>
<?php if ($inspectError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $inspectError]) ?>
<?php elseif ($inspect !== null && !$inspect['config_exists']): ?>
    <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle me-1"></i>The Nginx configuration for this website is missing on the server. Use <strong>Re-apply configuration</strong> below to recreate it.</div>
<?php endif; ?>

<div class="row g-3">
    <div class="col-xl-7">
        <div class="card mb-3" id="runtime">
            <div class="card-header"><i class="bi bi-hdd-stack me-1"></i>Server / Runtime</div>
            <div class="card-body">
                <dl class="wcp-dl">
                    <dt>Type</dt><dd><?= e(\WhaCP\Services\WebsiteService::KINDS[$site['kind']] ?? $site['kind']) ?></dd>
                    <?php if ($site['kind'] === 'php'): ?><dt>PHP</dt><dd><?= e($site['php_version']) ?> · <?= (int) ($site['php_settings']['memory_limit_mb'] ?? 256) ?> MB · up to <?= (int) ($site['php_settings']['max_children'] ?? 5) ?> PHP-FPM processes (<?= e((string) ($site['php_settings']['pm'] ?? 'ondemand')) ?>)</dd><?php endif; ?>
                    <?php if ($site['kind'] === 'proxy'): ?><dt>Upstream</dt><dd><code><?= e($site['proxy_pass']) ?></code></dd><?php endif; ?>
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
            <div class="card-footer small"><a href="<?= e($this->url('sites.edit', ['id' => $id])) ?>"><i class="bi bi-sliders me-1"></i>Change settings</a></div>
        </div>
        <?php if ($inspect !== null && !empty($inspect['config_preview'])): ?>
            <details class="card mb-3">
                <summary class="card-header"><i class="bi bi-file-earmark-code me-1"></i>Generated Nginx configuration</summary>
                <div class="card-body"><pre class="wcp-pre mb-0"><?= e($inspect['config_preview']) ?></pre></div>
            </details>
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
    </div>
    <div class="col-xl-5" id="danger-zone">
        <h2 class="wcp-section-heading text-danger-emphasis"><i class="bi bi-exclamation-octagon me-1"></i>Danger Zone</h2>
        <div class="card mb-3 border-warning-subtle">
            <div class="card-body d-grid gap-3">
                <div class="wcp-danger-row">
                    <div><div class="fw-medium"><?= $site['enabled'] ? 'Disable website' : 'Enable website' ?></div><div class="small text-body-secondary"><?= $site['enabled'] ? 'Visitors can no longer reach it. Files, databases and settings are kept.' : 'Serve the website to visitors again.' ?></div></div>
                    <form method="post" action="<?= e($this->url('sites.toggle', ['id' => $id])) ?>" data-wcp-busy
                          <?= $site['enabled'] ? 'data-wcp-confirm="Disable ' . e($site['domain']) . '? Visitors will no longer reach it. Files, databases and settings are kept."' : '' ?>>
                        <?= $this->csrfField() ?>
                        <button class="btn btn-sm btn-outline-<?= $site['enabled'] ? 'warning' : 'success' ?> text-nowrap" type="submit"><i class="bi bi-power me-1"></i><?= $site['enabled'] ? 'Disable' : 'Enable' ?></button>
                    </form>
                </div>
                <?php if ($site['kind'] === 'php' && $site['enabled']): ?>
                    <div class="wcp-danger-row">
                        <div><div class="fw-medium">Reload PHP</div><div class="small text-body-secondary">Restart this website's PHP <?= e($site['php_version']) ?> processes, e.g. after changing files PHP caches.</div></div>
                        <form method="post" action="<?= e($this->url('sites.restart_php', ['id' => $id])) ?>" data-wcp-busy>
                            <?= $this->csrfField() ?>
                            <button class="btn btn-sm btn-outline-secondary text-nowrap" type="submit"><i class="bi bi-arrow-clockwise me-1"></i>Reload PHP</button>
                        </form>
                    </div>
                <?php endif; ?>
                <div class="wcp-danger-row">
                    <div><div class="fw-medium">Re-apply configuration</div><div class="small text-body-secondary">Write this website's server configuration again from the panel's settings.</div></div>
                    <form method="post" action="<?= e($this->url('sites.reapply', ['id' => $id])) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <button class="btn btn-sm btn-outline-secondary text-nowrap" type="submit"><i class="bi bi-arrow-repeat me-1"></i>Re-apply</button>
                    </form>
                </div>
            </div>
        </div>
        <div class="card border-danger wcp-delete-card" id="delete-website">
            <div class="card-header text-danger"><i class="bi bi-trash me-1"></i>Delete website</div>
            <div class="card-body">
                <p class="small text-body-secondary">Removes the website's Nginx and PHP configuration. This cannot be undone from the panel.</p>
                <form method="post" action="<?= e($this->url('sites.delete', ['id' => $id])) ?>"
                      data-wcp-confirm="Delete <?= e($site['domain']) ?>? The Nginx and PHP configuration is removed. This cannot be undone from the panel.">
                    <?= $this->csrfField() ?>
                    <label class="form-label small" for="cd">Type <code><?= e($site['domain']) ?></code> to confirm</label>
                    <input class="form-control form-control-sm mb-2" id="cd" name="confirm_domain" autocomplete="off" required>
                    <div class="form-check small"><input class="form-check-input" type="checkbox" name="remove_files" value="1" id="rf"><label class="form-check-label" for="rf">Move the website files to the trash (<code>/var/lib/whacp-agent/trash</code>)</label></div>
                    <div class="form-check small mb-3"><input class="form-check-input" type="checkbox" name="remove_user" value="1" id="ru"><label class="form-check-label" for="ru">Remove the system user <code><?= e($site['system_user']) ?></code> if no other website uses it</label></div>
                    <button class="btn btn-danger w-100" type="submit"><i class="bi bi-trash me-1"></i>Delete website…</button>
                </form>
            </div>
        </div>
    </div>
</div>
