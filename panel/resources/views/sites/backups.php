<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
$canManage = $this->can(\WhaCP\Security\Permissions::BACKUPS_MANAGE);
$id = (int) $site['id'];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'backups']) ?>
<div class="row g-3">
    <div class="<?= $canManage ? 'col-xl-8' : 'col-12' ?>">
        <div class="card">
            <div class="card-header"><i class="bi bi-archive me-1"></i>Backups</div>
            <?= $this->partial('backups/_list', ['backups' => $backups, 'canManage' => $canManage]) ?>
        </div>
    </div>
    <?php if ($canManage): ?>
        <div class="col-xl-4">
            <div class="card mb-3">
                <div class="card-header"><i class="bi bi-plus-lg me-1"></i>Back up now</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('sites.backups.create', ['id' => $id])) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <div class="form-check"><input type="hidden" name="files" value="0"><input class="form-check-input" type="checkbox" id="b-files" name="files" value="1" checked><label class="form-check-label" for="b-files">Website files <span class="small text-body-secondary">(without logs and tmp)</span></label></div>
                        <div class="form-check mb-2"><input type="hidden" name="databases" value="0"><input class="form-check-input" type="checkbox" id="b-dbs" name="databases" value="1"<?= $databases !== [] ? ' checked' : ' disabled' ?>><label class="form-check-label" for="b-dbs">Databases <span class="small text-body-secondary">(<?= $databases === [] ? 'none linked to this website' : e(implode(', ', $databases)) ?>)</span></label></div>
                        <input class="form-control form-control-sm mb-2" name="label" maxlength="100" placeholder="Label (optional)" aria-label="Label">
                        <?= $this->partial('backups/_options', ['storages' => $storages, 'prefix' => 'b']) ?>
                        <button class="btn btn-sm btn-primary mt-3" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Start backup</button>
                    </form>
                </div>
            </div>
            <div class="card">
                <div class="card-header"><i class="bi bi-calendar-check me-1"></i>Schedule</div>
                <div class="card-body">
                    <?= $this->partial('backups/_schedules', ['schedules' => $schedules, 'deleteRoute' => 'sites.backups.schedule_delete', 'routeParams' => ['id' => $id]]) ?>
                    <?= $this->partial('backups/_schedule_form', ['action' => $this->url('sites.backups.schedule', ['id' => $id]), 'site' => true, 'storages' => $storages]) ?>
                </div>
            </div>
        </div>
    <?php endif; ?>
</div>
