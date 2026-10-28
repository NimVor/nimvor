<?php
/**
 * Website → Apps: a Softaculous-style installer — categories on the left, the
 * selected category's applications on the right, each with an install modal.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var list<array<string, mixed>> $installs
 * @var list<array<string, mixed>> $catalog   categories, each with 'apps'
 * @var bool $installerEnabled
 * @var list<array{id: int, domain: string, docroot: string, is_php: bool}> $targets
 */
use NimVor\Services\AppInstallerService as A;

$id = (int) $site['id'];
$isPhp = $site['kind'] === 'php';
$catalog ??= [];
$installerEnabled ??= true;
$targets ??= [['id' => $id, 'domain' => (string) $site['domain'], 'docroot' => $docroot ?? 'public_html', 'is_php' => $isPhp]];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'apps']) ?>
<?= $this->partial('partials/secret', ['secret' => $secret]) ?>

<?php if (!$installerEnabled): ?>
    <div class="card"><div class="nim-empty-state"><i class="bi bi-box" aria-hidden="true"></i><div class="fw-medium">The application installer is turned off</div><div class="small">Ask your hosting provider to enable it.</div></div></div>
    <?php return; ?>
<?php endif; ?>
<?php if (!$isPhp): ?>
    <div class="alert alert-info small">Applications need a PHP website. Change the type under <a href="<?= e($this->url('sites.edit', ['id' => $id])) ?>">Settings</a>.</div>
<?php endif; ?>
<?php if ($catalog === []): ?>
    <div class="card"><div class="nim-empty-state"><i class="bi bi-box" aria-hidden="true"></i><div class="fw-medium">No applications are available</div></div></div>
    <?php return; ?>
<?php endif; ?>

<div class="row g-3" id="app-installer">
    <div class="col-lg-3">
        <div class="list-group" role="tablist">
            <?php foreach ($catalog as $ci => $cat): ?>
                <button type="button" class="list-group-item list-group-item-action d-flex align-items-center gap-2<?= $ci === 0 ? ' active' : '' ?>" data-app-cat="<?= e((string) $cat['slug']) ?>">
                    <i class="bi <?= e((string) $cat['icon']) ?>" aria-hidden="true"></i>
                    <span class="flex-grow-1"><?= e((string) $cat['name']) ?></span>
                    <span class="badge rounded-pill text-bg-light border"><?= count($cat['apps']) ?></span>
                </button>
            <?php endforeach; ?>
        </div>
    </div>
    <div class="col-lg-9">
        <?php foreach ($catalog as $ci => $cat): ?>
            <div class="app-cat-panel<?= $ci === 0 ? '' : ' d-none' ?>" data-app-cat-panel="<?= e((string) $cat['slug']) ?>">
                <div class="row g-3 row-cols-1 row-cols-md-2 row-cols-xl-3">
                    <?php foreach ($cat['apps'] as $app): $slug = (string) $app['slug']; ?>
                        <div class="col">
                            <div class="card h-100">
                                <div class="card-body d-flex flex-column">
                                    <div class="d-flex align-items-center gap-2 mb-2">
                                        <i class="bi <?= e((string) $app['icon']) ?> fs-4" aria-hidden="true"></i>
                                        <span class="fw-semibold"><?= e((string) $app['name']) ?></span>
                                        <?php if (($app['status'] ?? 'enabled') === 'testing'): ?><span class="badge text-bg-warning" title="Still being tested: only administrators see it">Testing</span><?php endif; ?>
                                    </div>
                                    <p class="small text-body-secondary flex-grow-1"><?= e((string) $app['description']) ?></p>
                                    <button class="btn btn-sm btn-primary align-self-start" type="button" data-bs-toggle="modal" data-bs-target="#app-<?= e($slug) ?>"<?= $isPhp ? '' : ' disabled' ?>><i class="bi bi-download me-1"></i>Install</button>
                                    <a class="small mt-1" href="<?= e($this->url('sites.apps.plan', ['id' => $id, 'app' => $slug])) ?>"><i class="bi bi-list-check me-1" aria-hidden="true"></i>Review what it will do</a>
                                </div>
                            </div>
                        </div>
                    <?php endforeach; ?>
                </div>
            </div>
        <?php endforeach; ?>
    </div>
</div>

<?php
// One install modal per catalog app.
foreach ($catalog as $cat):
    foreach ($cat['apps'] as $app):
        $slug = (string) $app['slug'];
        $needsDb = (int) ($app['needs_db'] ?? 1) === 1;
        $action = $this->url('sites.apps.install', ['id' => $id, 'app' => $slug]);
        ?>
        <?= $this->modalStart('app-' . $slug, 'Install ' . (string) $app['name']) ?>
            <form method="post" action="<?= e($action) ?>" data-nim-busy>
                <div class="p-3">
                    <?= $this->csrfField() ?>
                    <p class="small text-body-secondary"><?= e((string) $app['description']) ?></p>
                    <?php $multi = count($targets) > 1; ?>
                    <?php if ($multi): ?>
                        <div class="mb-3">
                            <label class="form-label" for="tgt-<?= e($slug) ?>">Install on</label>
                            <select class="form-select" id="tgt-<?= e($slug) ?>" name="target_id">
                                <?php foreach ($targets as $t): ?>
                                    <option value="<?= (int) $t['id'] ?>"<?= $t['is_php'] ? '' : ' disabled' ?>><?= e((string) $t['domain']) ?><?= (int) $t['id'] === (int) $site['id'] ? ' — main domain' : ' — subdomain' ?><?= $t['is_php'] ? '' : ' (not a PHP site)' ?></option>
                                <?php endforeach; ?>
                            </select>
                            <div class="form-text">Choose this website or one of its subdomains; the application installs into the chosen site's document root.</div>
                        </div>
                    <?php endif; ?>
                    <?php if ($slug === 'wordpress' || isset(\NimVor\Services\AppInstallerService::WORDPRESS_BASED[$slug])): ?>
                        <?= $this->partial('partials/field', ['name' => 'title', 'label' => 'Site title', 'value' => (string) $site['domain']]) ?>
                        <div class="row g-2">
                            <div class="col-sm-5"><?= $this->partial('partials/field', ['name' => 'admin_user', 'label' => 'Admin user', 'value' => 'admin']) ?></div>
                            <div class="col-sm-7"><?= $this->partial('partials/field', ['name' => 'admin_email', 'label' => 'Admin email', 'type' => 'email', 'value' => (string) $email]) ?></div>
                        </div>
                        <div class="form-text mb-2"><?php if ($multi): ?>Installed into the selected website's document root (must be empty).<?php else: ?>Installed into <code><?= e($docroot) ?></code> (must be empty).<?php endif; ?> A database and a strong admin password are created; the password is shown once.</div>
                    <?php elseif ($slug === 'laravel'): ?>
                        <?= $this->partial('partials/field', ['name' => 'path', 'label' => 'Folder', 'value' => 'app', 'help' => 'A new folder inside the website; served from <folder>/public.']) ?>
                        <div class="form-check mb-2"><input class="form-check-input" type="checkbox" name="migrate" value="1" id="mig-<?= e($slug) ?>" checked><label class="form-check-label" for="mig-<?= e($slug) ?>">Run database migrations</label></div>
                    <?php else: ?>
                        <div class="form-text mb-2">
                            <?php if ($multi): ?>Staged into the selected website's document root (must be empty)<?php else: ?>Staged into <code><?= e($docroot) ?></code> (must be empty)<?php endif; ?><?= $needsDb ? ' with a new database' : ' (no database needed)' ?>.
                            <?php if ($needsDb): ?>The database credentials appear under Databases; finish setup in the application's own installer (a “Finish setup” link appears below once ready).<?php endif; ?>
                        </div>
                    <?php endif; ?>
                    <button class="btn btn-primary btn-sm" type="submit"<?= $isPhp ? '' : ' disabled' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Install <?= e((string) $app['name']) ?></button>
                </div>
            </form>
        <?= $this->modalEnd() ?>
    <?php endforeach; endforeach; ?>

<?php if ($installs !== []): ?>
    <div class="card mt-3">
        <div class="card-header"><i class="bi bi-clock-history me-1"></i>Installations</div>
        <div class="table-responsive"><table class="table table-sm align-middle mb-0">
            <thead><tr><th>Application</th><th>Folder</th><th>Database</th><th>Status</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($installs as $i): ?>
                <tr>
                    <td><?= e(A::APPS[$i['app']]['title'] ?? $i['app']) ?><?= $i['admin_user'] !== '' ? ' <span class="small text-body-secondary">admin: ' . e($i['admin_user']) . '</span>' : '' ?></td>
                    <td><code><?= e($i['path']) ?></code></td>
                    <td class="small"><?= e((string) ($i['database_name'] ?? '—')) ?></td>
                    <td><?= $this->statusBadge($i['status'] === 'running' ? 'activating' : (string) $i['status'], (string) $i['status']) ?><?php if ($i['error'] !== ''): ?><div class="small text-danger"><?= e($i['error']) ?></div><?php endif; ?></td>
                    <td class="text-end"><?php if ($i['job_id'] !== ''): ?><button type="button" class="btn btn-sm btn-outline-secondary" data-nim-job-open="<?= e($i['job_id']) ?>" title="Log"><i class="bi bi-terminal"></i></button><?php endif; ?>
                        <?php if ($i['status'] === 'succeeded' && $i['app'] === 'wordpress'): ?><a class="btn btn-sm btn-outline-primary" href="<?= e(($site['https'] ? 'https://' : 'http://') . $site['domain']) ?>/wp-admin/" target="_blank" rel="noopener noreferrer">Log in</a><?php endif; ?>
                        <?php if ($i['status'] === 'succeeded' && ($f = A::finishUrl($site, (string) $i['app'])) !== ''): ?><a class="btn btn-sm btn-primary" href="<?= e($f) ?>" target="_blank" rel="noopener noreferrer">Finish setup</a><?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    </div>
<?php endif; ?>
