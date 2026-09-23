<?php
/** @var \WhaCP\View\View $this */
$isNew = $role === null;
$readonly = !$isNew && (int) $role['is_system'] === 1;
$checked = array_flip((array) ($old['permissions'] ?? $granted));
$groups = [];
foreach ($permissions as $p) {
    $groups[$p['grp']][] = $p;
}
?>
<?php $this->start(); ?><?= $isNew ? 'New role' : e($role['label']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('users')) ?>"><i class="bi bi-arrow-left"></i> Users &amp; roles</a><?php $this->end('actions'); ?>

<?php if ($readonly): ?>
    <div class="alert alert-info small"><i class="bi bi-lock me-1"></i>This is a built-in role. Its permissions are defined by WhaCP and shown read-only.</div>
<?php endif; ?>
<form method="post" action="<?= e($isNew ? $this->url('roles.store') : $this->url('roles.update', ['id' => (int) $role['id']])) ?>" data-wcp-busy novalidate>
    <?= $this->csrfField() ?>
    <fieldset<?= $readonly ? ' disabled' : '' ?>>
    <div class="row g-3">
        <div class="col-lg-4">
            <div class="card"><div class="card-body">
                <?php if ($isNew): ?>
                    <?= $this->partial('partials/field', ['name' => 'name', 'label' => 'Name (identifier)', 'type' => 'text', 'value' => (string) ($old['name'] ?? ''), 'help' => 'e.g. support_staff', 'attrs' => ['required' => 'required', 'maxlength' => '32']]) ?>
                <?php else: ?>
                    <div class="mb-3"><label class="form-label">Name</label><input class="form-control" value="<?= e($role['name']) ?>" disabled></div>
                <?php endif; ?>
                <?= $this->partial('partials/field', ['name' => 'label', 'label' => 'Label', 'type' => 'text', 'value' => (string) ($old['label'] ?? $role['label'] ?? ''), 'attrs' => ['required' => 'required', 'maxlength' => '60']]) ?>
                <?= $this->partial('partials/field', ['name' => 'description', 'label' => 'Description', 'type' => 'text', 'value' => (string) ($old['description'] ?? $role['description'] ?? ''), 'attrs' => ['maxlength' => '255']]) ?>
                <?php if (!$readonly): ?>
                    <button class="btn btn-primary" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $isNew ? 'Create role' : 'Save role' ?></button>
                <?php endif; ?>
            </div></div>
        </div>
        <div class="col-lg-8">
            <div class="card">
                <div class="card-header">Permissions</div>
                <div class="card-body">
                    <?php if (isset($errors['permissions'])): ?><div class="alert alert-danger small"><?= e($errors['permissions']) ?></div><?php endif; ?>
                    <div class="row">
                    <?php foreach ($groups as $group => $items): ?>
                        <div class="col-md-6 mb-3">
                            <div class="fw-semibold small text-body-secondary mb-1"><?= e($group) ?></div>
                            <?php foreach ($items as $p): ?>
                                <div class="form-check">
                                    <input class="form-check-input" type="checkbox" name="permissions[]" value="<?= e($p['name']) ?>" id="perm-<?= e($p['name']) ?>"<?= isset($checked[$p['name']]) ? ' checked' : '' ?>>
                                    <label class="form-check-label small" for="perm-<?= e($p['name']) ?>"><?= e($p['label']) ?> <code class="text-body-secondary"><?= e($p['name']) ?></code></label>
                                </div>
                            <?php endforeach; ?>
                        </div>
                    <?php endforeach; ?>
                    </div>
                </div>
            </div>
        </div>
    </div>
    </fieldset>
</form>
