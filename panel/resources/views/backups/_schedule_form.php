<?php
/** @var \NimVor\View\View $this */
/** @var string $action */
/** @var bool $site */
use NimVor\Services\BackupScheduler;
?>
<form method="post" action="<?= e($action) ?>" class="small">
    <?= $this->csrfField() ?>
    <?php if (!$site): ?>
        <div class="mb-2" data-nim-schedule-scope>
            <div class="form-check"><input class="form-check-input" type="radio" name="scope" value="server" id="s-scope-server" checked><label class="form-check-label" for="s-scope-server">The server configuration</label></div>
            <div class="form-check"><input class="form-check-input" type="radio" name="scope" value="all_sites" id="s-scope-all"><label class="form-check-label" for="s-scope-all">Every website (files and databases; each website its own backup, also websites added later)</label></div>
        </div>
    <?php endif; ?>
    <div class="row g-2 mb-2">
        <div class="col-4"><label class="form-label small" for="s-freq">Frequency</label>
            <select class="form-select form-select-sm" id="s-freq" name="frequency"><option value="daily">Daily</option><option value="weekly">Weekly</option></select></div>
        <div class="col-4"><label class="form-label small" for="s-day">Day (weekly)</label>
            <select class="form-select form-select-sm" id="s-day" name="weekday"><?php foreach (BackupScheduler::WEEKDAYS as $i => $d): ?><option value="<?= $i ?>"><?= e($d) ?></option><?php endforeach; ?></select></div>
        <div class="col-4"><label class="form-label small" for="s-hour">Hour</label>
            <select class="form-select form-select-sm" id="s-hour" name="hour"><?php for ($h = 0; $h < 24; $h++): ?><option value="<?= $h ?>"<?= $h === 3 ? ' selected' : '' ?>><?= sprintf('%02d:00', $h) ?></option><?php endfor; ?></select></div>
    </div>
    <?php if ($site): ?>
        <div class="d-flex gap-3 mb-2">
            <div class="form-check"><input type="hidden" name="include_files" value="0"><input class="form-check-input" type="checkbox" id="s-files" name="include_files" value="1" checked><label class="form-check-label" for="s-files">Files</label></div>
            <div class="form-check"><input type="hidden" name="include_databases" value="0"><input class="form-check-input" type="checkbox" id="s-dbs" name="include_databases" value="1" checked><label class="form-check-label" for="s-dbs">Databases</label></div>
        </div>
    <?php endif; ?>
    <div class="mb-2"><label class="form-label small" for="s-ret">Keep the last</label>
        <div class="input-group input-group-sm"><input class="form-control" id="s-ret" type="number" name="retention" min="1" max="365" value="7"><span class="input-group-text">backups</span></div></div>
    <?= $this->partial('backups/_options', ['storages' => $storages, 'prefix' => 's', 'alwaysEncrypted' => !$site]) ?>
    <button class="btn btn-sm btn-outline-primary mt-2">Add schedule</button>
    <div class="form-text">Times use the server's time zone.<?= $site ? '' : ' For every website, the number kept applies to each website.' ?></div>
</form>
