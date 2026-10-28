<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
/** @var array<string, mixed>|null $cfg */
/** @var list<array<string, mixed>> $history */
$id = (int) $site['id'];
$form = $old !== [] ? $old : ($cfg ?? ['branch' => 'main', 'path' => 'app']);
$hooks = $form['hooks'] ?? '';
if (is_array($hooks)) {
    $hooks = implode("\n", $hooks);
}
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'git']) ?>
<div class="row g-3">
    <div class="col-xl-7">
        <?php if ($cfg !== null): ?>
            <div class="card mb-3">
                <div class="card-header d-flex align-items-center"><i class="bi bi-git me-1"></i>Deployment
                    <form class="ms-auto" method="post" action="<?= e($this->url('sites.git.deploy', ['id' => $id])) ?>" data-nim-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><i class="bi bi-rocket-takeoff me-1"></i>Deploy now</button></form>
                </div>
                <div class="card-body">
                    <dl class="nim-dl mb-0">
                        <dt>Repository</dt><dd><code class="text-break"><?= e($cfg['repo']) ?></code></dd>
                        <dt>Branch</dt><dd><code><?= e($cfg['branch']) ?></code></dd>
                        <dt>Deployed to</dt><dd><code><?= e(rtrim((string) $site['root'], '/') . '/' . $cfg['path']) ?></code></dd>
                        <dt>On push</dt><dd><?= $cfg['auto_deploy'] ? $this->statusBadge('active', 'deploys automatically') : '<span class="text-body-secondary">manual only</span>' ?></dd>
                    </dl>
                    <?php if ($cfg['path'] !== '' && !str_starts_with((string) $site['document_root'], rtrim((string) $site['root'], '/') . '/' . $cfg['path'])): ?>
                        <div class="alert alert-info small mt-3 mb-0"><i class="bi bi-info-circle me-1"></i>The website is served from <code><?= e($site['document_root']) ?></code>. To serve the deployed code, set the document root to e.g. <code><?= e($cfg['path']) ?>/public</code> in <a href="<?= e($this->url('sites.edit', ['id' => $id])) ?>">Settings</a>.</div>
                    <?php endif; ?>
                </div>
            </div>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-clock-history me-1"></i>History</div>
                <?php if ($history === []): ?>
                    <div class="card-body text-body-secondary">No deployments yet.</div>
                <?php else: ?>
                    <div class="table-responsive"><table class="table table-sm align-middle mb-0">
                        <thead><tr><th>When</th><th>Commit</th><th>By</th><th>Status</th><th></th></tr></thead>
                        <tbody>
                        <?php foreach ($history as $d): ?>
                            <tr>
                                <td class="text-nowrap small"><?= e(gmdate('Y-m-d H:i', (int) strtotime((string) $d['created_at']))) ?> UTC</td>
                                <td class="small"><?php if ($d['commit_hash'] !== ''): ?><code><?= e(substr((string) $d['commit_hash'], 0, 10)) ?></code> <?= e($d['message']) ?><?php else: ?><span class="text-body-secondary"><?= e($d['error']) ?></span><?php endif; ?></td>
                                <td class="small"><?= $d['trigger'] === 'webhook' ? 'webhook' : e((string) ($d['username'] ?? '')) ?></td>
                                <td><?= $this->statusBadge((string) $d['status']) ?></td>
                                <td class="text-end"><?php if ($d['job_id'] !== ''): ?><button type="button" class="btn btn-sm btn-outline-secondary" data-nim-job-open="<?= e($d['job_id']) ?>" title="Log"><i class="bi bi-terminal"></i></button><?php endif; ?></td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table></div>
                <?php endif; ?>
            </div>
        <?php endif; ?>
        <div class="card">
            <div class="card-header"><i class="bi bi-sliders me-1"></i><?= $cfg === null ? 'Connect a repository' : 'Repository settings' ?></div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('sites.git.configure', ['id' => $id])) ?>" data-nim-busy>
                    <?= $this->csrfField() ?>
                    <?= $this->partial('partials/field', ['name' => 'repo', 'label' => 'Repository URL', 'value' => (string) ($form['repo'] ?? ''), 'help' => 'https:// for public repositories, or an SSH address (git@github.com:owner/repo.git) with the deploy key below.', 'attrs' => ['required' => 'required', 'placeholder' => 'git@github.com:owner/repo.git']]) ?>
                    <div class="row g-2">
                        <div class="col-sm-6"><?= $this->partial('partials/field', ['name' => 'branch', 'label' => 'Branch', 'value' => (string) ($form['branch'] ?? 'main'), 'attrs' => ['required' => 'required']]) ?></div>
                        <div class="col-sm-6"><?= $this->partial('partials/field', ['name' => 'path', 'label' => 'Folder in the website', 'value' => (string) ($form['path'] ?? 'app'), 'help' => 'Cloned here; must be empty on first deploy.', 'attrs' => ['required' => 'required']]) ?></div>
                    </div>
                    <label class="form-label" for="g-hooks">Commands after each deployment (optional)</label>
                    <textarea class="form-control font-monospace mb-1<?= isset($errors['hooks']) ? ' is-invalid' : '' ?>" id="g-hooks" name="hooks" rows="4" placeholder="composer install --no-dev --optimize-autoloader&#10;php artisan migrate --force"><?= e((string) $hooks) ?></textarea>
                    <?php if (isset($errors['hooks'])): ?><div class="invalid-feedback"><?= e($errors['hooks']) ?></div><?php endif; ?>
                    <div class="form-text mb-3">One command per line, run in order as <code><?= e($site['system_user']) ?></code> inside the deployed folder. The deployment stops at the first failing command.</div>
                    <div class="form-check form-switch mb-3"><input type="hidden" name="auto_deploy" value="0"><input class="form-check-input" type="checkbox" role="switch" id="g-auto" name="auto_deploy" value="1"<?= !empty($form['auto_deploy']) ? ' checked' : '' ?>><label class="form-check-label" for="g-auto">Deploy automatically when the webhook receives a push to this branch</label></div>
                    <button class="btn btn-sm btn-primary" type="submit">Save</button>
                </form>
            </div>
            <?php if ($cfg !== null): ?>
                <div class="card-footer"><form method="post" action="<?= e($this->url('sites.git.remove', ['id' => $id])) ?>" data-nim-confirm="Disconnect the repository? Deployed files stay in place."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Disconnect repository</button></form></div>
            <?php endif; ?>
        </div>
    </div>
    <?php if ($cfg !== null): ?>
        <div class="col-xl-5">
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-key me-1"></i>Deploy key</div>
                <div class="card-body">
                    <?php if ($keyError !== null): ?>
                        <p class="text-warning small mb-0">The deploy key could not be created: <?= e($keyError) ?></p>
                    <?php else: ?>
                        <p class="small">Add this public key as a read-only <strong>deploy key</strong> in your repository settings (GitHub: Settings → Deploy keys; GitLab: Settings → Repository → Deploy keys).</p>
                        <textarea class="form-control form-control-sm font-monospace mb-2" rows="3" readonly data-nim-deploy-key><?= e($cfg['public_key']) ?></textarea>
                        <button type="button" class="btn btn-sm btn-outline-secondary" data-nim-copy="[data-nim-deploy-key]"><i class="bi bi-clipboard"></i> Copy</button>
                    <?php endif; ?>
                </div>
            </div>
            <div class="card">
                <div class="card-header"><i class="bi bi-broadcast me-1"></i>Webhook</div>
                <div class="card-body small">
                    <p>Add a <em>push</em> webhook with content type <code>application/json</code>:</p>
                    <label class="form-label" for="g-hook-url">Payload URL</label>
                    <div class="input-group input-group-sm mb-2"><input class="form-control font-monospace" id="g-hook-url" value="<?= e($webhookUrl) ?>" readonly data-nim-hook-url><button type="button" class="btn btn-outline-secondary" data-nim-copy="[data-nim-hook-url]"><i class="bi bi-clipboard"></i></button></div>
                    <label class="form-label" for="g-hook-secret">Secret (GitHub/Gitea) or secret token (GitLab)</label>
                    <div class="input-group input-group-sm mb-3"><input class="form-control font-monospace" id="g-hook-secret" value="<?= e($webhookSecret) ?>" readonly data-nim-hook-secret><button type="button" class="btn btn-outline-secondary" data-nim-copy="[data-nim-hook-secret]"><i class="bi bi-clipboard"></i></button></div>
                    <form method="post" action="<?= e($this->url('sites.git.rotate', ['id' => $id])) ?>" data-nim-confirm="Generate a new webhook URL and secret? The current webhook stops working."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary">Regenerate</button></form>
                </div>
            </div>
        </div>
    <?php endif; ?>
</div>
