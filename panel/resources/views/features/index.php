<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $lists */
/** @var array<string, array{0: string, 1: list<string>, 2: list<string>}> $catalogue */
$total = count($catalogue);
$form = function (?array $l) use ($catalogue): string {
    $id = $l === null ? 'new' : (string) (int) $l['id'];
    ob_start(); ?>
    <div class="modal fade" id="fl-modal-<?= e($id) ?>" tabindex="-1" aria-labelledby="fl-title-<?= e($id) ?>" aria-hidden="true">
        <div class="modal-dialog modal-lg modal-dialog-scrollable">
            <form class="modal-content" method="post" action="<?= e($this->url('features.store')) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <?php if ($l !== null): ?><input type="hidden" name="list_id" value="<?= (int) $l['id'] ?>"><?php endif; ?>
                <div class="modal-header"><h2 class="modal-title fs-5" id="fl-title-<?= e($id) ?>"><?= $l === null ? 'New feature list' : 'Edit ' . e($l['name']) ?></h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
                <div class="modal-body">
                    <div class="row g-2 mb-3">
                        <div class="col-md-5"><label class="form-label" for="fl-name-<?= e($id) ?>">Name</label><input class="form-control" id="fl-name-<?= e($id) ?>" name="name" required maxlength="60" value="<?= e((string) ($l['name'] ?? '')) ?>"></div>
                        <div class="col-md-7"><label class="form-label" for="fl-desc-<?= e($id) ?>">Description</label><input class="form-control" id="fl-desc-<?= e($id) ?>" name="description" maxlength="300" value="<?= e((string) ($l['description'] ?? '')) ?>"></div>
                    </div>
                    <div class="fw-medium mb-2">Features included</div>
                    <?php
                    $defaultOff = \NimVor\Services\FeatureService::DEFAULT_OFF;
                    $optIn = $l['enabled_optin'] ?? [];
                    $disabled = $l['disabled'] ?? [];
                    // Group features by their 4th catalogue element.
                    $groups = [];
                    foreach ($catalogue as $key => $meta) {
                        $groups[$meta[3] ?? 'Other'][$key] = $meta[0];
                    }
                    foreach ($groups as $group => $items): ?>
                        <div class="text-uppercase small text-body-secondary fw-semibold mt-3 mb-1"><?= e((string) $group) ?></div>
                        <div class="row row-cols-1 row-cols-md-2 g-1">
                        <?php foreach ($items as $key => $label):
                            $isOff = in_array($key, $defaultOff, true);
                            $on = $l === null ? !$isOff : ($isOff ? in_array($key, $optIn, true) : !in_array($key, $disabled, true)); ?>
                            <div class="col"><div class="form-check form-switch">
                                <input class="form-check-input" type="checkbox" role="switch" name="features[]" value="<?= e($key) ?>" id="fl-<?= e($id) ?>-<?= e($key) ?>"<?= $on ? ' checked' : '' ?>>
                                <label class="form-check-label" for="fl-<?= e($id) ?>-<?= e($key) ?>"><?= e($label) ?><?php if ($isOff): ?> <span class="badge text-bg-light border text-body-secondary">off by default</span><?php endif; ?></label>
                            </div></div>
                        <?php endforeach; ?>
                        </div>
                    <?php endforeach; ?>
                </div>
                <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save</button></div>
            </form>
        </div>
    </div>
    <?php return (string) ob_get_clean();
};
?>
<?php $this->start(); ?>Feature Manager<?php $this->end('title'); ?>
<?php $this->start(); ?>Which panel features each hosting plan includes<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('plans')) ?>"><i class="bi bi-box-seam me-1"></i>Plans</a> <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#fl-modal-new"><i class="bi bi-plus-lg me-1"></i>New feature list</button><?php $this->end('actions'); ?>

<div class="nim-infobar mb-3"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>Assign a list to a plan under Plans. A feature that is off is refused by the server, not only hidden. A reseller's customers get only features both their own plan and the reseller's plan include. Plans without a list have every feature; administrators are never limited.</span></div>

<div class="nim-panel">
    <div class="nim-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Feature list</th><th>Features</th><th class="text-end">Plans</th><th></th></tr></thead>
            <tbody>
            <?php
            $defaultOff = \NimVor\Services\FeatureService::DEFAULT_OFF;
            foreach ($lists as $l):
                $off = array_values(array_unique(array_merge(
                    $l['disabled'],
                    array_values(array_diff($defaultOff, $l['enabled_optin'] ?? []))
                )));
            ?>
                <tr>
                    <td><span class="fw-semibold"><?= e($l['name']) ?></span><?php if ($l['description'] !== ''): ?><div class="small text-body-secondary"><?= e($l['description']) ?></div><?php endif; ?></td>
                    <td class="small"><?= $total - count($off) ?> of <?= $total ?><?php if ($off !== []): ?><div class="text-body-secondary">Off: <?= e(implode(', ', array_map(static fn (string $k): string => $catalogue[$k][0] ?? $k, $off))) ?></div><?php endif; ?></td>
                    <td class="text-end"><?= (int) $l['plans'] ?></td>
                    <td class="text-end text-nowrap">
                        <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#fl-modal-<?= (int) $l['id'] ?>">Edit</button>
                        <form class="d-inline" method="post" action="<?= e($this->url('features.delete', ['id' => (int) $l['id']])) ?>" data-nim-confirm="Delete the feature list “<?= e($l['name']) ?>”?<?= (int) $l['plans'] > 0 ? ' The ' . (int) $l['plans'] . ' plan(s) using it will have every feature.' : '' ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger">Delete</button></form>
                    </td>
                </tr>
            <?php endforeach; ?>
            <?php if ($lists === []): ?><tr><td colspan="4" class="text-center py-4 nim-muted">No feature lists yet: every plan has every feature.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
</div>
<?= $form(null) ?>
<?php foreach ($lists as $l): ?><?= $form($l) ?><?php endforeach; ?>
