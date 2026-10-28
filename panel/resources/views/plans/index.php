<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $plans */
/** @var array<string, mixed>|null $edit */
use NimVor\Services\PlanService;

$errorsAll = (array) ($errors ?? []);
// Which window a refused save reopens: the plan's, or New plan.
$failedId = $old !== [] ? (int) ($old['plan_id'] ?? 0) : -1;
// /plans?edit=ID (older links) opens that plan's window.
$openId = $failedId >= 0 ? $failedId : (is_array($edit ?? null) ? (int) $edit['id'] : -1);
$lim = static fn ($v): string => (int) $v === 0 ? '∞' : (string) (int) $v;
$admin = \NimVor\Hosting\Scope::isAll((array) ($user ?? []));
?>
<?php $this->start(); ?>Hosting plans<?php $this->end('title'); ?>
<?php $this->start(); ?>Limits for hosting customers<?= $admin ? ' and resellers' : '' ?><?php $this->end('subtitle'); ?>
<?php $this->start(); ?><button type="button" class="btn btn-sm btn-primary" data-bs-toggle="modal" data-bs-target="#plan-new"><i class="bi bi-plus-lg me-1"></i>New plan</button><?php $this->end('actions'); ?>
<div class="card">
            <?php if ($plans === []): ?>
                <div class="card-body text-body-secondary">No plans yet. Users without a plan are unlimited.</div>
            <?php else: ?>
                <div class="table-responsive"><table class="table table-sm align-middle mb-0">
                    <thead><tr><th>Plan</th><th class="text-center">Websites</th><th class="text-center">Databases</th><th class="text-center">Cron</th><th class="text-center">Workers</th><?php if ($admin): ?><th class="text-center">Customers</th><?php endif; ?><th class="text-center">Disk</th><th class="text-center">Bandwidth</th><th class="text-center">Email</th><th class="text-center">FTP</th><th class="text-center">CPU / memory</th><th class="text-center">Users</th><th></th></tr></thead>
                    <tbody>
                    <?php foreach ($plans as $p): ?>
                        <tr>
                            <td><strong><?= e($p['name']) ?></strong><?php if ($p['owner_name'] !== null && $admin): ?> <span class="badge text-bg-light border">reseller <?= e($p['owner_name']) ?></span><?php endif; ?><div class="small text-body-secondary"><?= e($p['description']) ?></div></td>
                            <td class="text-center"><?= e($lim($p['max_websites'])) ?></td>
                            <td class="text-center"><?= e($lim($p['max_databases'])) ?></td>
                            <td class="text-center"><?= e($lim($p['max_cron_jobs'])) ?></td>
                            <td class="text-center"><?= e($lim($p['max_workers'])) ?></td>
                            <?php if ($admin): ?><td class="text-center"><?= e($lim($p['max_customers'])) ?></td><?php endif; ?>
                            <td class="text-center text-nowrap"><?= (int) $p['disk_quota_mb'] === 0 ? '∞' : e(format_bytes((int) $p['disk_quota_mb'] * 1048576)) ?></td>
                            <td class="text-center text-nowrap"><?= (int) $p['bandwidth_mb'] === 0 ? '∞' : e(format_bytes((int) $p['bandwidth_mb'] * 1048576)) ?></td>
                            <td class="text-center"><?= e($lim($p['max_email_accounts'])) ?></td>
                            <td class="text-center"><?= e($lim($p['max_ftp_accounts'])) ?></td>
                            <td class="text-center text-nowrap small"><?= (int) ($p['cpu_percent'] ?? 0) > 0 ? (int) $p['cpu_percent'] . '%' : '∞' ?> / <?= (int) ($p['memory_mb'] ?? 0) > 0 ? e(format_bytes((int) $p['memory_mb'] * 1048576, 0)) : '∞' ?></td>
                            <td class="text-center"><?= (int) $p['users'] ?></td>
                            <td class="text-end text-nowrap">
                                <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-toggle="modal" data-bs-target="#plan-edit-<?= (int) $p['id'] ?>" title="Edit" aria-label="Edit <?= e($p['name']) ?>"><i class="bi bi-pencil"></i></button>
                                <form class="d-inline" method="post" action="<?= e($this->url('plans.delete', ['id' => (int) $p['id']])) ?>" data-nim-confirm="Delete the plan <?= e($p['name']) ?>?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button></form>
                            </td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table></div>
            <?php endif; ?>
    <div class="card-footer small text-body-secondary">0 means unlimited. A reseller's plan also caps the total used by the reseller and all of their customers. Disk use is measured hourly.</div>
</div>

<?php
$planForm = function (array $form, int $pid, string $px, array $ferr) use ($admin, $featureLists): void { ?>
<form method="post" action="<?= e($this->url('plans.store')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <?php if ($pid > 0): ?><input type="hidden" name="plan_id" value="<?= $pid ?>"><?php endif; ?>
    <?= $this->partial('partials/field', ['idPrefix' => $px, 'errors' => $ferr, 'name' => 'name', 'label' => 'Name', 'value' => (string) ($form['name'] ?? ''), 'attrs' => ['required' => 'required', 'maxlength' => '60']]) ?>
    <?= $this->partial('partials/field', ['idPrefix' => $px, 'errors' => $ferr, 'name' => 'description', 'label' => 'Description (optional)', 'value' => (string) ($form['description'] ?? ''), 'attrs' => ['maxlength' => '300']]) ?>
    <div class="row g-2">
        <?php foreach (PlanService::FIELDS as $f => $label): if ($f === 'max_customers' && !$admin) { continue; } ?>
            <div class="col-6"><?= $this->partial('partials/field', ['idPrefix' => $px, 'errors' => $ferr, 'name' => $f, 'label' => $label, 'type' => 'number', 'value' => (string) ($form[$f] ?? '0'), 'attrs' => ['min' => '0']]) ?></div>
        <?php endforeach; ?>
    </div>
    <fieldset class="border rounded p-2 mb-3"><legend class="float-none w-auto px-1 mb-0 small fw-semibold">Resource limits (CPU, memory)</legend>
        <div class="row g-2">
            <?php foreach (PlanService::LIMITS as $f => [$label, $min, $max, $unit]): ?>
                <div class="col-6"><?= $this->partial('partials/field', ['idPrefix' => $px, 'errors' => $ferr, 'name' => $f, 'label' => $label . ($unit !== '' ? ' (' . $unit . ')' : ''), 'type' => 'number', 'value' => (string) ($form[$f] ?? '0'), 'attrs' => ['min' => '0', 'max' => (string) $max]]) ?></div>
            <?php endforeach; ?>
        </div>
        <div class="form-text mt-0">0 = no limit. Applied with cgroups to all of an account's websites (PHP), cron jobs and workers together, and to its SSH/SFTP logins; changes apply at once.<?= $admin ? '' : ' Your own plan\'s limits cap these.' ?></div>
    </fieldset>
    <div class="form-check mb-3"><input type="hidden" name="ffmpeg" value="0"><input class="form-check-input" type="checkbox" id="<?= e($px) ?>ffmpeg" name="ffmpeg" value="1"<?= !empty($form['ffmpeg']) ? ' checked' : '' ?>><label class="form-check-label" for="<?= e($px) ?>ffmpeg">FFmpeg (audio and video processing)</label><div class="form-text mt-0">The plan's accounts may run the server's FFmpeg<?= $admin ? '' : ' (when your own plan includes it)' ?>.</div></div>
    <?php if ($admin): $fl = (string) ($form['feature_list_id'] ?? ''); ?>
        <label class="form-label" for="<?= e($px) ?>features">Feature list</label>
        <select class="form-select form-select-sm mb-1" id="<?= e($px) ?>features" name="feature_list_id">
            <option value="">All features</option>
            <?php foreach ($featureLists ?? [] as $l): ?><option value="<?= (int) $l['id'] ?>"<?= $fl === (string) $l['id'] ? ' selected' : '' ?>><?= e($l['name']) ?></option><?php endforeach; ?>
        </select>
        <div class="form-text mt-0 mb-3">Which panel features the plan's accounts can use. <a href="<?= e($this->url('features')) ?>">Manage feature lists</a></div>
    <?php endif; ?>
    <div class="d-flex justify-content-end gap-2">
        <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
        <button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $pid > 0 ? 'Save plan' : 'Create plan' ?></button>
    </div>
</form>
<?php };
echo $this->modalStart('plan-new', 'New plan', $openId === 0);
$planForm($failedId === 0 ? $old : [], 0, 'pn-', $failedId === 0 ? $errorsAll : []);
echo $this->modalEnd();
foreach ($plans as $p) {
    $pid = (int) $p['id'];
    echo $this->modalStart('plan-edit-' . $pid, 'Edit plan ' . $p['name'], $openId === $pid);
    $planForm($failedId === $pid ? $old : $p, $pid, 'pe' . $pid . '-', $failedId === $pid ? $errorsAll : []);
    echo $this->modalEnd();
}
?>
