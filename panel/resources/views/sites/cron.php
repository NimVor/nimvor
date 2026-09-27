<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
/** @var list<array<string, mixed>> $jobs */
/** @var array<string, mixed>|null $edit */
use WhaCP\Services\CronService;

$canManage = $this->can(\WhaCP\Security\Permissions::CRON_MANAGE);
$id = (int) $site['id'];
$errors ??= [];
$form = $old !== [] ? $old : ($edit ?? []);
$sched = (string) ($form['schedule'] ?? '*/5 * * * *');
$isPreset = isset(CronService::PRESETS[$sched]);
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'cron']) ?>
<?php $open = $canManage && ($edit || $errors !== []); ?>
<div class="card" id="cron-jobs">
    <div class="card-header wcp-toolbar">
        <h2 class="wcp-toolbar-title"><i class="bi bi-clock-history me-1"></i>Cron jobs <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($jobs) ?></span></h2>
        <?php if ($canManage): ?>
            <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="collapse" data-bs-target="#new-cron" aria-expanded="<?= $open ? 'true' : 'false' ?>" aria-controls="new-cron"><i class="bi bi-plus-lg me-1"></i>Add cron job</button>
        <?php endif; ?>
    </div>
    <?php if ($canManage): ?>
        <div class="collapse wcp-form-panel<?= $open ? ' show' : '' ?>" id="new-cron" data-wcp-form-panel>
            <div class="p-3">
                <h3 class="h6"><?= $edit ? 'Edit cron job' : 'New cron job' ?></h3>
                    <form method="post" action="<?= e($this->url('sites.cron.store', ['id' => $id])) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <?php if ($edit): ?><input type="hidden" name="job_id" value="<?= (int) $edit['id'] ?>"><?php endif; ?>
                        <label class="form-label" for="c-sched">Schedule</label>
                        <select class="form-select mb-2<?= isset($errors['schedule']) ? ' is-invalid' : '' ?>" id="c-sched" name="schedule">
                            <?php foreach (CronService::PRESETS as $expr => $label): ?>
                                <option value="<?= e($expr) ?>"<?= $sched === $expr ? ' selected' : '' ?>><?= e($label) ?> (<?= e($expr) ?>)</option>
                            <?php endforeach; ?>
                            <option value="custom"<?= $isPreset ? '' : ' selected' ?>>Custom…</option>
                        </select>
                        <input class="form-control mb-1 font-monospace<?= isset($errors['schedule']) ? ' is-invalid' : '' ?>" name="schedule_custom" placeholder="minute hour day month weekday" value="<?= e($isPreset ? '' : $sched) ?>" aria-label="Custom schedule">
                        <?php if (isset($errors['schedule'])): ?><div class="invalid-feedback d-block"><?= e($errors['schedule']) ?></div><?php endif; ?>
                        <div class="form-text mb-3">For a custom schedule choose “Custom…” and enter five fields, e.g. <code>30 2 * * 1</code> (Mondays 02:30).</div>
                        <label class="form-label" for="c-cmd">Command</label>
                        <input class="form-control font-monospace<?= isset($errors['command']) ? ' is-invalid' : '' ?>" id="c-cmd" name="command" value="<?= e($form['command'] ?? '') ?>" placeholder="php app/artisan schedule:run" required>
                        <?php if (isset($errors['command'])): ?><div class="invalid-feedback"><?= e($errors['command']) ?></div><?php endif; ?>
                        <div class="form-text mb-3">Runs in <code><?= e($site['root']) ?></code>.</div>
                        <?= $this->partial('partials/field', ['name' => 'description', 'label' => 'Description (optional)', 'value' => (string) ($form['description'] ?? '')]) ?>
                        <div class="form-check form-switch mb-3"><input type="hidden" name="enabled" value="0"><input class="form-check-input" type="checkbox" role="switch" id="c-en" name="enabled" value="1"<?= ($form['enabled'] ?? true) ? ' checked' : '' ?>><label class="form-check-label" for="c-en">Enabled</label></div>
                        <button class="btn btn-primary btn-sm" type="submit"><?= $edit ? 'Save' : 'Add job' ?></button>
                        <?php if ($edit): ?><a class="btn btn-link btn-sm" href="<?= e($this->url('sites.cron', ['id' => $id])) ?>">Cancel</a><?php endif; ?>
                    </form>
            </div>
        </div>
    <?php endif; ?>
    <?php if ($jobs === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-clock-history" aria-hidden="true"></i><div class="fw-medium">No cron jobs yet</div><div class="small">Run a command on a schedule, e.g. WordPress or Laravel tasks every few minutes.</div></div>
    <?php else: ?>
                <div class="table-responsive">
                    <table class="table table-sm align-middle mb-0">
                        <thead><tr><th>Schedule</th><th>Command</th><th>Status</th><?php if ($canManage): ?><th class="text-end">Actions</th><?php endif; ?></tr></thead>
                        <tbody>
                        <?php foreach ($jobs as $j): ?>
                            <tr>
                                <td class="text-nowrap"><code><?= e($j['schedule']) ?></code><div class="small text-body-secondary"><?= e(CronService::PRESETS[$j['schedule']] ?? '') ?></div></td>
                                <td><code class="small text-break"><?= e($j['command']) ?></code><?php if ($j['description'] !== ''): ?><div class="small text-body-secondary"><?= e($j['description']) ?></div><?php endif; ?>
                                    <div class="small text-body-secondary">Output: logs/cron-<?= (int) $j['id'] ?>.log</div></td>
                                <td><?= $j['enabled'] ? $this->statusBadge('active', 'enabled') : $this->statusBadge('inactive', 'paused') ?></td>
                                <?php if ($canManage): ?>
                                    <td class="text-end text-nowrap">
                                        <form class="d-inline" method="post" action="<?= e($this->url('sites.cron.run', ['id' => $id, 'job' => (int) $j['id']])) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary" title="Run now"><i class="bi bi-play"></i></button></form>
                                        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.cron', ['id' => $id], ['edit' => (int) $j['id']])) ?>" title="Edit"><i class="bi bi-pencil"></i></a>
                                        <form class="d-inline" method="post" action="<?= e($this->url('sites.cron.toggle', ['id' => $id, 'job' => (int) $j['id']])) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary" title="<?= $j['enabled'] ? 'Pause' : 'Enable' ?>"><i class="bi bi-<?= $j['enabled'] ? 'pause' : 'check-lg' ?>"></i></button></form>
                                        <form class="d-inline" method="post" action="<?= e($this->url('sites.cron.delete', ['id' => $id, 'job' => (int) $j['id']])) ?>" data-wcp-confirm="Delete this cron job?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button></form>
                                    </td>
                                <?php endif; ?>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
    <?php endif; ?>
    <div class="card-footer small text-body-secondary">Jobs run as <code><?= e($site['system_user']) ?></code> from <code><?= e($site['root']) ?></code>. Times use the server's time zone.</div>
</div>
