<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
/** @var list<array<string, mixed>> $installs */
use WhaCP\Services\AppInstallerService as A;

$id = (int) $site['id'];
$isPhp = $site['kind'] === 'php';
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'apps']) ?>
<?= $this->partial('partials/secret', ['secret' => $secret]) ?>
<?php if (!$isPhp): ?>
    <div class="alert alert-info small">Applications need a PHP website. Change the type under <a href="<?= e($this->url('sites.edit', ['id' => $id])) ?>">Settings</a>.</div>
<?php endif; ?>
<div class="row g-3">
    <div class="col-lg-6">
        <div class="card h-100" id="app-wordpress">
            <div class="card-header"><i class="bi bi-wordpress me-1"></i><?= e(A::APPS['wordpress']['title']) ?></div>
            <div class="card-body">
                <p class="small text-body-secondary"><?= e(A::APPS['wordpress']['description']) ?> Installed into <code><?= e($docroot) ?></code>, which must be empty.</p>
                <form method="post" action="<?= e($this->url('sites.apps.install', ['id' => $id, 'app' => 'wordpress'])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <?= $this->partial('partials/field', ['name' => 'title', 'label' => 'Site title', 'value' => (string) ($old['title'] ?? $site['domain'])]) ?>
                    <div class="row g-2">
                        <div class="col-sm-5"><?= $this->partial('partials/field', ['name' => 'admin_user', 'label' => 'Admin user', 'value' => (string) ($old['admin_user'] ?? 'admin')]) ?></div>
                        <div class="col-sm-7"><?= $this->partial('partials/field', ['name' => 'admin_email', 'label' => 'Admin email', 'type' => 'email', 'value' => (string) ($old['admin_email'] ?? $email)]) ?></div>
                    </div>
                    <button class="btn btn-sm btn-primary"<?= $isPhp ? '' : ' disabled' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Install WordPress</button>
                    <div class="form-text">A database, a database user and a strong administrator password are created for you; the password is shown once.</div>
                </form>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100" id="app-laravel">
            <div class="card-header"><i class="bi bi-braces-asterisk me-1"></i><?= e(A::APPS['laravel']['title']) ?></div>
            <div class="card-body">
                <p class="small text-body-secondary"><?= e(A::APPS['laravel']['description']) ?> Requires Composer (under Packages).</p>
                <form method="post" action="<?= e($this->url('sites.apps.install', ['id' => $id, 'app' => 'laravel'])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <?= $this->partial('partials/field', ['name' => 'path', 'label' => 'Folder', 'value' => (string) ($old['path'] ?? 'app'), 'help' => 'A new folder inside the website.']) ?>
                    <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="migrate" value="1" id="l-mig" checked><label class="form-check-label" for="l-mig">Run database migrations</label></div>
                    <button class="btn btn-sm btn-primary"<?= $isPhp ? '' : ' disabled' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Install Laravel</button>
                </form>
            </div>
        </div>
    </div>
</div>
<?php
$stagedIcons = ['joomla' => 'bi-journal-richtext', 'drupal' => 'bi-droplet', 'phpbb' => 'bi-chat-left-text', 'smf' => 'bi-chat-square-dots', 'prestashop' => 'bi-bag', 'opencart' => 'bi-cart', 'abantecart' => 'bi-cart4'];
?>
<div class="card mt-3" id="apps-staged">
    <div class="card-header"><i class="bi bi-box-seam me-1"></i>More applications</div>
    <div class="card-body">
        <p class="small text-body-secondary mb-3">Each is downloaded (verified where the vendor publishes a checksum), unpacked into <code><?= e($docroot) ?></code> (which must be empty) with a fresh database, then you finish setup in its own installer. The database name, user and password are listed under <a href="<?= e($this->url('databases')) ?>">Databases</a>.</p>
        <div class="row g-2 row-cols-2 row-cols-md-3 row-cols-lg-4">
            <?php foreach (A::STAGED as $app => $meta): ?>
                <div class="col">
                    <form method="post" action="<?= e($this->url('sites.apps.install', ['id' => $id, 'app' => $app])) ?>" data-wcp-busy data-wcp-confirm="Install <?= e(A::APPS[$app]['title']) ?> into <?= e($docroot) ?>? The folder must be empty.">
                        <?= $this->csrfField() ?>
                        <button class="btn btn-outline-secondary w-100 text-start py-2"<?= $isPhp ? '' : ' disabled' ?>>
                            <i class="bi <?= e($stagedIcons[$app] ?? 'bi-box') ?> me-1"></i><?= e(A::APPS[$app]['title']) ?>
                        </button>
                    </form>
                </div>
            <?php endforeach; ?>
        </div>
    </div>
</div>

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
                    <td class="text-end"><?php if ($i['job_id'] !== ''): ?><button type="button" class="btn btn-sm btn-outline-secondary" data-wcp-job-open="<?= e($i['job_id']) ?>" title="Log"><i class="bi bi-terminal"></i></button><?php endif; ?>
                        <?php if ($i['status'] === 'succeeded' && $i['app'] === 'wordpress'): ?><a class="btn btn-sm btn-outline-primary" href="<?= e(($site['https'] ? 'https://' : 'http://') . $site['domain']) ?>/wp-admin/" target="_blank" rel="noopener noreferrer">Log in</a><?php endif; ?>
                        <?php if ($i['status'] === 'succeeded' && ($f = A::finishUrl($site, (string) $i['app'])) !== ''): ?><a class="btn btn-sm btn-primary" href="<?= e($f) ?>" target="_blank" rel="noopener noreferrer">Finish setup</a><?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    </div>
<?php endif; ?>
