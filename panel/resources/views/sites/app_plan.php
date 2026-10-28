<?php
/**
 * The dry run of an installation.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $site
 * @var array<string, mixed> $plan
 * @var string $app
 * @var string $title
 */
$id = (int) $site['id'];
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'apps']) ?>

<div class="d-flex flex-wrap align-items-center gap-2 mb-3">
    <h2 class="h5 mb-0 me-auto">What installing <?= e($title) ?> would do <?php if (($plan['status'] ?? '') === 'testing'): ?><span class="badge text-bg-warning">Testing</span><?php endif; ?></h2>
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.apps', ['id' => $id])) ?>"><i class="bi bi-arrow-left me-1"></i>Back to applications</a>
</div>
<p class="small text-body-secondary">This is a preview. Nothing has been changed on <strong><?= e($site['domain']) ?></strong>.</p>

<?php if (!empty($plan['ok'])): ?>
    <div class="alert alert-success small"><i class="bi bi-check-circle me-1"></i>Everything this installation needs is in place.</div>
<?php else: ?>
    <div class="alert alert-warning small"><strong>It cannot be installed yet:</strong>
        <ul class="mb-0 mt-1"><?php foreach ((array) ($plan['conflicts'] ?? []) as $c): ?><li><?= e((string) $c) ?></li><?php endforeach; ?></ul>
    </div>
<?php endif; ?>

<div class="row g-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header">Requirements</div>
            <ul class="list-group list-group-flush">
                <?php foreach ((array) ($plan['checks'] ?? []) as $c): ?>
                    <li class="list-group-item d-flex gap-2">
                        <i class="bi bi-<?= !empty($c['ok']) ? 'check-circle-fill text-success' : 'x-circle-fill text-danger' ?>" aria-hidden="true"></i>
                        <span><?= e((string) $c['name']) ?><?php if (empty($c['ok']) && !empty($c['detail'])): ?><div class="small text-body-secondary"><?= e((string) $c['detail']) ?></div><?php endif; ?></span>
                    </li>
                <?php endforeach; ?>
            </ul>
            <div class="card-footer small text-body-secondary">Disk space: <?= (int) ($plan['disk_needed_mb'] ?? 0) ?> MB needed<?= !empty($plan['disk_free_mb']) ? ', ' . (int) $plan['disk_free_mb'] . ' MB free' : '' ?>. Licence: <?= e((string) ($plan['license'] ?? '')) === 'NOASSERTION' ? 'not recorded' : e((string) ($plan['license'] ?? '')) ?>.</div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header">Changes that would be made</div>
            <ol class="list-group list-group-numbered list-group-flush">
                <?php foreach ((array) ($plan['steps'] ?? []) as $s): ?>
                    <li class="list-group-item"><span class="fw-medium"><?= e((string) $s['action']) ?></span> — <?= e((string) $s['detail']) ?></li>
                <?php endforeach; ?>
            </ol>
            <div class="card-footer small text-body-secondary">Back up the website first if it holds anything you need: installing into a folder that has files is refused, and removing an installation is a separate, confirmed step.</div>
        </div>
    </div>
</div>
