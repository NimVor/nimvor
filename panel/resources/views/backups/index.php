<?php
/** @var \WhaCP\View\View $this */
$canManage = $this->can(\WhaCP\Security\Permissions::BACKUPS_MANAGE);
?>
<?php $this->start(); ?>Backups<?php $this->end('title'); ?>
<?php $this->start(); ?>Website and server backups<?php $this->end('subtitle'); ?>
<div class="card mb-3">
    <div class="card-header"><i class="bi bi-globe2 me-1"></i>Website backups</div>
    <?= $this->partial('backups/_list', ['backups' => $backups, 'canManage' => $canManage, 'showSite' => true]) ?>
    <div class="card-footer small text-body-secondary">Create, restore and schedule website backups from each website's <strong>Backups</strong> tab.</div>
</div>
<?php if ($admin): ?>
<div class="row g-3">
    <div class="col-xl-8">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-hdd-rack me-1"></i>Server configuration</div>
            <?= $this->partial('backups/_list', ['backups' => $serverBackups, 'canManage' => $canManage]) ?>
            <div class="card-footer small text-body-secondary">Archives Nginx, PHP, MySQL, Redis, certificates, SSH/firewall and WhaCP configuration for disaster recovery. It contains private keys, so it is always encrypted before upload.</div>
        </div>
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-cloud me-1"></i>Remote storage</div>
            <div class="card-body">
                <?php if ($storages === []): ?><p class="text-body-secondary small">No remote storage. Add an S3-compatible bucket (AWS S3, Backblaze B2, Wasabi, Cloudflare R2, MinIO, DigitalOcean Spaces…) to keep copies off this server.</p><?php endif; ?>
                <?php foreach ($storages as $s): ?>
                    <div class="d-flex align-items-center border-bottom py-2 small">
                        <div><strong><?= e($s['name']) ?></strong><div class="text-body-secondary"><?= e($s['endpoint']) ?> · <?= e($s['bucket']) ?><?= $s['prefix'] !== '' ? '/' . e($s['prefix']) : '' ?></div></div>
                        <?php if ($canManage): ?><form class="ms-auto" method="post" action="<?= e($this->url('backups.storages.delete', ['storage' => (int) $s['id']])) ?>" data-wcp-confirm="Remove the storage <?= e($s['name']) ?>? Backups already uploaded stay in the bucket."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger"><i class="bi bi-trash"></i></button></form><?php endif; ?>
                    </div>
                <?php endforeach; ?>
                <?php if ($canManage): ?>
                    <details class="mt-3"<?= isset($errors['endpoint']) || isset($errors['bucket']) || isset($errors['access_key']) ? ' open' : '' ?>>
                        <summary class="btn btn-sm btn-outline-primary">Add storage</summary>
                        <form class="mt-3" method="post" action="<?= e($this->url('backups.storages.store')) ?>" data-wcp-busy>
                            <?= $this->csrfField() ?>
                            <div class="row g-2">
                                <div class="col-md-6"><?= $this->partial('partials/field', ['name' => 'name', 'label' => 'Name', 'value' => (string) ($old['name'] ?? ''), 'attrs' => ['required' => 'required', 'placeholder' => 'Offsite']]) ?></div>
                                <div class="col-md-6"><?= $this->partial('partials/field', ['name' => 'endpoint', 'label' => 'Endpoint', 'value' => (string) ($old['endpoint'] ?? ''), 'attrs' => ['required' => 'required', 'placeholder' => 'https://s3.eu-central-1.amazonaws.com']]) ?></div>
                                <div class="col-md-4"><?= $this->partial('partials/field', ['name' => 'bucket', 'label' => 'Bucket', 'value' => (string) ($old['bucket'] ?? ''), 'attrs' => ['required' => 'required']]) ?></div>
                                <div class="col-md-4"><?= $this->partial('partials/field', ['name' => 'region', 'label' => 'Region', 'value' => (string) ($old['region'] ?? 'us-east-1')]) ?></div>
                                <div class="col-md-4"><?= $this->partial('partials/field', ['name' => 'prefix', 'label' => 'Folder (optional)', 'value' => (string) ($old['prefix'] ?? ''), 'attrs' => ['placeholder' => 'whacp/web1']]) ?></div>
                                <div class="col-md-6"><?= $this->partial('partials/field', ['name' => 'access_key', 'label' => 'Access key', 'value' => '', 'attrs' => ['required' => 'required', 'autocomplete' => 'off']]) ?></div>
                                <div class="col-md-6"><?= $this->partial('partials/field', ['name' => 'secret_key', 'label' => 'Secret key', 'type' => 'password', 'value' => '', 'attrs' => ['required' => 'required', 'autocomplete' => 'new-password']]) ?></div>
                            </div>
                            <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="path_style" value="1" id="st-path"><label class="form-check-label" for="st-path">Path-style URLs (MinIO and some other providers)</label></div>
                            <button class="btn btn-sm btn-primary">Test and save</button>
                            <div class="form-text">The keys are stored encrypted in the panel and sent to the agent only for each backup. Use keys limited to this bucket.</div>
                        </form>
                    </details>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-xl-4">
        <?php if ($canManage): ?>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-plus-lg me-1"></i>Back up the server configuration</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('backups.server')) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <input class="form-control form-control-sm mb-2" name="label" maxlength="100" placeholder="Label (optional)" aria-label="Label">
                        <?= $this->partial('backups/_options', ['storages' => $storages, 'prefix' => 'sv']) ?>
                        <button class="btn btn-sm btn-primary mt-3"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Start backup</button>
                    </form>
                </div>
            </div>
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-calendar-check me-1"></i>Server backup schedule</div>
                <div class="card-body">
                    <?= $this->partial('backups/_schedules', ['schedules' => $schedules, 'deleteRoute' => 'backups.schedules.delete', 'routeParams' => []]) ?>
                    <?= $this->partial('backups/_schedule_form', ['action' => $this->url('backups.schedules.store'), 'site' => false, 'storages' => $storages]) ?>
                </div>
            </div>
        <?php endif; ?>
        <div class="card">
            <div class="card-header"><i class="bi bi-key me-1"></i>Encryption key</div>
            <div class="card-body small">
                <?php if ($keyInfo === null): ?>
                    <span class="text-body-secondary">Agent not reachable.</span>
                <?php elseif (empty($keyInfo['exists'])): ?>
                    The key is created with the first encrypted backup at <code><?= e($keyInfo['path']) ?></code>.
                <?php else: ?>
                    Key ID <code><?= e($keyInfo['key_id']) ?></code> in <code><?= e($keyInfo['path']) ?></code>.
                <?php endif; ?>
                <div class="alert alert-warning mt-2 mb-0 p-2"><i class="bi bi-exclamation-triangle me-1"></i>Copy this file somewhere safe (as root). Encrypted backups cannot be restored without it — for example on a new server after a disk failure.</div>
            </div>
        </div>
    </div>
</div>
<?php endif; ?>
