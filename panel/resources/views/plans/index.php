<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $plans */
/** @var array<string, mixed>|null $edit */
use WhaCP\Services\PlanService;

$form = $old !== [] ? $old : ($edit ?? []);
$lim = static fn ($v): string => (int) $v === 0 ? '∞' : (string) (int) $v;
$admin = \WhaCP\Hosting\Scope::isAll((array) ($user ?? []));
?>
<?php $this->start(); ?>Hosting plans<?php $this->end('title'); ?>
<?php $this->start(); ?>Limits for hosting customers<?= $admin ? ' and resellers' : '' ?><?php $this->end('subtitle'); ?>
<div class="row g-3">
    <div class="col-xl-8">
        <div class="card">
            <?php if ($plans === []): ?>
                <div class="card-body text-body-secondary">No plans yet. Users without a plan are unlimited.</div>
            <?php else: ?>
                <div class="table-responsive"><table class="table table-sm align-middle mb-0">
                    <thead><tr><th>Plan</th><th class="text-center">Websites</th><th class="text-center">Databases</th><th class="text-center">Cron</th><th class="text-center">Workers</th><?php if ($admin): ?><th class="text-center">Customers</th><?php endif; ?><th class="text-center">Disk</th><th class="text-center">Bandwidth</th><th class="text-center">Email</th><th class="text-center">FTP</th><th class="text-center">Users</th><th></th></tr></thead>
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
                            <td class="text-center"><?= (int) $p['users'] ?></td>
                            <td class="text-end text-nowrap">
                                <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('plans', [], ['edit' => (int) $p['id']])) ?>" title="Edit"><i class="bi bi-pencil"></i></a>
                                <form class="d-inline" method="post" action="<?= e($this->url('plans.delete', ['id' => (int) $p['id']])) ?>" data-wcp-confirm="Delete the plan <?= e($p['name']) ?>?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Delete"><i class="bi bi-trash"></i></button></form>
                            </td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table></div>
            <?php endif; ?>
            <div class="card-footer small text-body-secondary">0 means unlimited. A reseller's plan also caps the total used by the reseller and all of their customers. Disk use is measured hourly.</div>
        </div>
    </div>
    <div class="col-xl-4">
        <div class="card">
            <div class="card-header"><?= $edit ? 'Edit ' . e($edit['name']) : 'New plan' ?></div>
            <div class="card-body">
                <form method="post" action="<?= e($this->url('plans.store')) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <?php if ($edit): ?><input type="hidden" name="plan_id" value="<?= (int) $edit['id'] ?>"><?php endif; ?>
                    <?= $this->partial('partials/field', ['name' => 'name', 'label' => 'Name', 'value' => (string) ($form['name'] ?? ''), 'attrs' => ['required' => 'required', 'maxlength' => '60']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'description', 'label' => 'Description (optional)', 'value' => (string) ($form['description'] ?? ''), 'attrs' => ['maxlength' => '300']]) ?>
                    <div class="row g-2">
                        <?php foreach (PlanService::FIELDS as $f => $label): if ($f === 'max_customers' && !$admin) { continue; } ?>
                            <div class="col-6"><?= $this->partial('partials/field', ['name' => $f, 'label' => $label, 'type' => 'number', 'value' => (string) ($form[$f] ?? '0'), 'attrs' => ['min' => '0']]) ?></div>
                        <?php endforeach; ?>
                    </div>
                    <div class="form-check mb-3"><input type="hidden" name="ffmpeg" value="0"><input class="form-check-input" type="checkbox" id="p-ffmpeg" name="ffmpeg" value="1"<?= !empty($form['ffmpeg']) ? ' checked' : '' ?>><label class="form-check-label" for="p-ffmpeg">FFmpeg (audio and video processing)</label><div class="form-text mt-0">The plan's accounts may run the server's FFmpeg<?= $admin ? '' : ' (when your own plan includes it)' ?>.</div></div>
                    <?php if ($admin): $fl = (string) ($form['feature_list_id'] ?? ''); ?>
                        <label class="form-label" for="p-features">Feature list</label>
                        <select class="form-select form-select-sm mb-1" id="p-features" name="feature_list_id">
                            <option value="">All features</option>
                            <?php foreach ($featureLists ?? [] as $l): ?><option value="<?= (int) $l['id'] ?>"<?= $fl === (string) $l['id'] ? ' selected' : '' ?>><?= e($l['name']) ?></option><?php endforeach; ?>
                        </select>
                        <div class="form-text mt-0 mb-3">Which panel features the plan's accounts can use. <a href="<?= e($this->url('features')) ?>">Manage feature lists</a></div>
                    <?php endif; ?>
                    <button class="btn btn-sm btn-primary"><?= $edit ? 'Save' : 'Create plan' ?></button>
                    <?php if ($edit): ?><a class="btn btn-sm btn-link" href="<?= e($this->url('plans')) ?>">Cancel</a><?php endif; ?>
                </form>
            </div>
        </div>
    </div>
</div>
