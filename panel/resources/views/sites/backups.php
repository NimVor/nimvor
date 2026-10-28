<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
$canManage = $this->can(\NimVor\Security\Permissions::BACKUPS_MANAGE);
$id = (int) $site['id'];
$last = null;
foreach ($backups as $b) {
    if ($b['status'] === 'completed') {
        $last = $b;
        break;
    }
}
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'backups']) ?>

<div class="d-flex flex-wrap align-items-center gap-3 mb-3 small">
    <span><?= $last !== null ? '<span class="nim-state is-ok">Last backup ' . e(time_ago((string) $last['created_at'])) . '</span>' : '<span class="nim-state is-warn">No backup yet</span>' ?></span>
    <span class="text-body-secondary"><?= $schedules !== [] ? count($schedules) . ' schedule' . (count($schedules) === 1 ? '' : 's') : 'No automatic backups scheduled' ?></span>
</div>

<div class="card mb-3" id="backup-list">
    <div class="card-header nim-toolbar">
        <h2 class="nim-toolbar-title"><i class="bi bi-archive me-1"></i>Backups <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($backups) ?></span></h2>
        <?php if ($canManage): ?>
            <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#backup-now"><i class="bi bi-plus-lg me-1"></i>Back up now</button>
        <?php endif; ?>
    </div>
    <?php if ($canManage): ?>
        <?= $this->modalStart('backup-now', 'Back up now') ?>
            <form class="p-3" method="post" action="<?= e($this->url('sites.backups.create', ['id' => $id])) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <div class="row g-3">
                    <div class="col-md-6">
                        <div class="fw-medium small mb-1">What to back up</div>
                        <div class="form-check"><input type="hidden" name="files" value="0"><input class="form-check-input" type="checkbox" id="b-files" name="files" value="1" checked><label class="form-check-label" for="b-files">Website files <span class="small text-body-secondary">(without logs and tmp)</span></label></div>
                        <div class="form-check mb-2"><input type="hidden" name="databases" value="0"><input class="form-check-input" type="checkbox" id="b-dbs" name="databases" value="1"<?= $databases !== [] ? ' checked' : ' disabled' ?>><label class="form-check-label" for="b-dbs">Databases <span class="small text-body-secondary">(<?= $databases === [] ? 'none linked to this website' : e(implode(', ', $databases)) ?>)</span></label></div>
                        <input class="form-control form-control-sm" name="label" maxlength="100" placeholder="Label (optional)" aria-label="Label">
                    </div>
                    <div class="col-md-6"><?= $this->partial('backups/_options', ['storages' => $storages, 'prefix' => 'b']) ?></div>
                </div>
                <button class="btn btn-sm btn-primary mt-3" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Start backup</button>
            </form>
        <?= $this->modalEnd() ?>
    <?php endif; ?>
    <?= $this->partial('backups/_list', ['backups' => $backups, 'canManage' => $canManage]) ?>
</div>

<?php $canSchedule ??= false; ?>
<div class="card" id="schedule">
    <div class="card-header"><i class="bi bi-calendar-check me-1"></i>Automatic backups</div>
    <div class="card-body">
        <?php if ($canSchedule): ?>
            <div class="row g-4">
                <div class="col-lg-6"><?= $this->partial('backups/_schedules', ['schedules' => $schedules, 'deleteRoute' => 'sites.backups.schedule_delete', 'routeParams' => ['id' => $id]]) ?></div>
                <div class="col-lg-6"><?= $this->partial('backups/_schedule_form', ['action' => $this->url('sites.backups.schedule', ['id' => $id]), 'site' => true, 'storages' => $storages]) ?></div>
            </div>
        <?php else: ?>
            <?= $this->partial('backups/_schedules', ['schedules' => $schedules, 'deleteRoute' => null, 'routeParams' => ['id' => $id]]) ?>
            <p class="small text-body-secondary mt-2 mb-0"><i class="bi bi-info-circle me-1"></i>Automatic backups are set up by your administrator. You can back up and restore at any time above.</p>
        <?php endif; ?>
    </div>
</div>
