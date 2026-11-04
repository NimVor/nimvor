<?php
/**
 * The dialogs of a table's Structure tab. Each <template> is the body of one dialog; a button opens it with
 * data-pgd-open="name" and data-pgd-set='{"field": "value"}' (fills the named fields and the data-pgd-fill texts).
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var string $schema
 * @var string $table
 * @var list<string> $columns
 * @var list<string> $schemas
 */
$alter = $this->url('postgres.alter');
$preview = $this->url('postgres.alter_preview');
?>
<?= $this->partial('postgres/_dialog_shell', ['db' => $db, 'schema' => $schema, 'table' => $table]) ?>
<?= $this->partial('postgres/_types', []) ?>

<template data-pgd-tpl="add_column" data-title="Add a column" data-ok="Add column" data-op="add_column" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <?= $this->partial('postgres/_column_fields', ['prefix' => 'col', 'primary' => false]) ?>
</template>

<template data-pgd-tpl="edit_column" data-title="Change a column" data-ok="Apply" data-op="edit_column" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <input type="hidden" name="name"><input type="hidden" name="cur_type"><input type="hidden" name="cur_not_null">
    <p class="small text-body-secondary">Column <span class="font-monospace" data-pgd-fill="name"></span>. Only what you change is applied, one statement after another. Changing the type converts every value, and fails without changing anything if a value does not fit.</p>
    <div class="row g-2">
        <div class="col-md-6"><label class="form-label small mb-0" for="pgd-c-new">Name</label><input class="form-control form-control-sm" id="pgd-c-new" name="new_name" maxlength="63"></div>
        <div class="col-md-6"><label class="form-label small mb-0" for="pgd-c-type">Type</label><input class="form-control form-control-sm" id="pgd-c-type" name="type" list="pg-types" maxlength="40"></div>
        <div class="col-md-6"><label class="form-label small mb-0" for="pgd-c-dm">Default</label>
            <select class="form-select form-select-sm" id="pgd-c-dm" name="default_mode"><option value="keep">keep as it is</option><option value="remove">remove the default</option><option value="literal">set to a value</option><option value="function">set to a function</option></select></div>
        <div class="col-md-6"><label class="form-label small mb-0" for="pgd-c-d">Value, or now / current_timestamp / current_date / gen_random_uuid</label><input class="form-control form-control-sm" id="pgd-c-d" name="default" maxlength="200"></div>
        <div class="col-md-6"><label class="form-label small mb-0" for="pgd-c-cm">Comment (empty keeps the current one)</label><input class="form-control form-control-sm" id="pgd-c-cm" name="comment" maxlength="200"></div>
        <div class="col-md-6 d-flex align-items-end"><label class="form-check mb-1"><input class="form-check-input" type="checkbox" name="not_null" value="1"> require a value (NOT NULL)</label></div>
    </div>
</template>

<template data-pgd-tpl="drop_column" data-title="Drop a column" data-ok="Drop column" data-op="drop_column" data-danger="1" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <input type="hidden" name="name">
    <p>Delete the column <strong class="font-monospace" data-pgd-fill="name"></strong> and its values from every row? This cannot be undone.</p>
    <label class="form-label small" for="pgd-dc">Type the column name to confirm</label>
    <input class="form-control font-monospace" id="pgd-dc" name="confirm" autocomplete="off" data-pgd-confirm="name">
    <label class="form-check mt-2"><input class="form-check-input" type="checkbox" name="cascade" value="1"> also drop what depends on it (CASCADE)</label>
</template>

<template data-pgd-tpl="create_index" data-title="Create an index" data-ok="Create index" data-op="create_index" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <div class="row g-1 mb-1">
        <div class="col-12 col-md-4"><input class="form-control form-control-sm" name="name" placeholder="name (optional)" maxlength="63" aria-label="Index name"></div>
        <div class="col-6 col-md-3"><select class="form-select form-select-sm" name="method" aria-label="Index method"><?php foreach (['btree', 'hash', 'gin', 'gist', 'brin', 'spgist'] as $m): ?><option><?= e($m) ?></option><?php endforeach; ?></select></div>
        <div class="col-12 col-md-5 d-flex flex-wrap gap-3 small align-items-center">
            <label class="form-check mb-0"><input class="form-check-input" type="checkbox" name="unique" value="1"> unique</label>
            <label class="form-check mb-0" title="Builds without blocking writes; slower, and cannot be used on partitioned tables."><input class="form-check-input" type="checkbox" name="concurrent" value="1"> without locking writes</label>
        </div>
    </div>
    <?php for ($i = 0; $i < 3; $i++): ?>
    <div class="row g-1 mb-1">
        <div class="col-6 col-md-5"><select class="form-select form-select-sm" name="icols[<?= $i ?>][name]" aria-label="Index column <?= $i + 1 ?>"><option value=""><?= $i === 0 ? 'column…' : 'another column (optional)' ?></option><?php foreach ($columns as $c): ?><option><?= e($c) ?></option><?php endforeach; ?></select></div>
        <div class="col-3 col-md-3"><select class="form-select form-select-sm" name="icols[<?= $i ?>][order]" aria-label="Order"><option value="">ascending</option><option value="desc">descending</option></select></div>
        <div class="col-3 col-md-4"><select class="form-select form-select-sm" name="icols[<?= $i ?>][nulls]" aria-label="NULLs"><option value="">nulls default</option><option value="first">nulls first</option><option value="last">nulls last</option></select></div>
    </div>
    <?php endfor; ?>
    <div class="row g-1 mb-1"><div class="col-12"><input class="form-control form-control-sm" name="include" placeholder="also store these columns (comma-separated, optional)" maxlength="300" aria-label="Included columns"></div></div>
    <?= $this->partial('postgres/_predicate', ['columns' => $columns, 'legend' => 'Only index rows where (optional):']) ?>
</template>

<template data-pgd-tpl="rename_index" data-title="Rename an index" data-ok="Rename" data-op="rename_index" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <input type="hidden" name="name">
    <label class="form-label small" for="pgd-ri">New name for <span class="font-monospace" data-pgd-fill="name"></span></label>
    <input class="form-control" id="pgd-ri" name="new_name" maxlength="63" autocomplete="off">
</template>

<template data-pgd-tpl="add_constraint" data-title="Add a constraint" data-ok="Add constraint" data-op="add_constraint" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <div class="row g-1 mb-1">
        <div class="col-12 col-md-4"><input class="form-control form-control-sm" name="name" placeholder="name (optional)" maxlength="63" aria-label="Constraint name"></div>
        <div class="col-6 col-md-3"><select class="form-select form-select-sm" name="kind" aria-label="Constraint type"><option value="unique">unique</option><option value="primary">primary key</option><option value="check">check</option><option value="foreign">foreign key</option></select></div>
        <div class="col-12 col-md-5"><input class="form-control form-control-sm" name="columns" placeholder="columns, comma-separated (unique, primary, foreign)" maxlength="300" aria-label="Columns"></div>
    </div>
    <?= $this->partial('postgres/_predicate', ['columns' => $columns, 'legend' => 'Check: the condition every row must satisfy:']) ?>
    <div class="row g-1 mb-1"><div class="col-12 small text-body-secondary">Foreign key: references</div>
        <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="ref_schema" value="<?= e($schema) ?>" maxlength="63" aria-label="Referenced schema"></div>
        <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="ref_table" placeholder="table" maxlength="63" aria-label="Referenced table"></div>
        <div class="col-12 col-md-6"><input class="form-control form-control-sm" name="ref_columns" placeholder="columns, comma-separated" maxlength="300" aria-label="Referenced columns"></div>
        <div class="col-6"><select class="form-select form-select-sm" name="on_delete" aria-label="On delete"><?php foreach (['' => 'on delete: no action', 'restrict' => 'restrict', 'cascade' => 'cascade', 'set null' => 'set null', 'set default' => 'set default'] as $k => $v): ?><option value="<?= e($k) ?>"><?= e($v) ?></option><?php endforeach; ?></select></div>
        <div class="col-6"><select class="form-select form-select-sm" name="on_update" aria-label="On update"><?php foreach (['' => 'on update: no action', 'restrict' => 'restrict', 'cascade' => 'cascade', 'set null' => 'set null', 'set default' => 'set default'] as $k => $v): ?><option value="<?= e($k) ?>"><?= e($v) ?></option><?php endforeach; ?></select></div>
    </div>
    <div class="d-flex flex-wrap gap-3 small">
        <label class="form-check mb-0" title="Check new rows now, existing rows later with Validate. Avoids a long lock on big tables."><input class="form-check-input" type="checkbox" name="not_valid" value="1"> do not check existing rows yet (check and foreign key)</label>
        <label class="form-check mb-0"><input class="form-check-input" type="checkbox" name="deferrable" value="1"> deferrable (foreign key)</label>
    </div>
</template>

<template data-pgd-tpl="rename_constraint" data-title="Rename a constraint" data-ok="Rename" data-op="rename_constraint" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <input type="hidden" name="name">
    <label class="form-label small" for="pgd-rc">New name for <span class="font-monospace" data-pgd-fill="name"></span></label>
    <input class="form-control" id="pgd-rc" name="new_name" maxlength="63" autocomplete="off">
</template>

<template data-pgd-tpl="add_partition" data-title="Add a partition" data-ok="Add partition" data-op="create_partition" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <div class="row g-1 mb-1">
        <div class="col-12 col-md-6"><label class="form-label small mb-0" for="pgd-p-op">Do what</label><select class="form-select form-select-sm" id="pgd-p-op" name="op"><option value="create_partition">create a new partition</option><option value="attach_partition">attach an existing table</option></select></div>
        <div class="col-6 col-md-3"><label class="form-label small mb-0" for="pgd-p-new">New partition name</label><input class="form-control form-control-sm" id="pgd-p-new" name="new_name" maxlength="63"></div>
        <div class="col-6 col-md-3"><label class="form-label small mb-0" for="pgd-p-att">Table to attach</label><input class="form-control form-control-sm" id="pgd-p-att" name="name" maxlength="63"></div>
        <div class="col-12 col-md-6"><label class="form-label small mb-0" for="pgd-p-bk">Bound type</label><select class="form-select form-select-sm" id="pgd-p-bk" name="bound_kind"><option value="range">range (from / to)</option><option value="list">list (values)</option><option value="hash">hash (modulus / remainder)</option><option value="default">default partition</option></select></div>
    </div>
    <div class="row g-1 mb-1">
        <div class="col-6"><input class="form-control form-control-sm" name="bound_from" placeholder="from (MINVALUE allowed)" aria-label="From"></div>
        <div class="col-6"><input class="form-control form-control-sm" name="bound_to" placeholder="to, exclusive (MAXVALUE allowed)" aria-label="To"></div>
        <div class="col-12"><input class="form-control form-control-sm" name="bound_values" placeholder="values, comma-separated (list)" aria-label="Values"></div>
        <div class="col-6"><input class="form-control form-control-sm" name="bound_modulus" placeholder="modulus (hash)" aria-label="Modulus"></div>
        <div class="col-6"><input class="form-control form-control-sm" name="bound_remainder" placeholder="remainder (hash)" aria-label="Remainder"></div>
    </div>
</template>

<template data-pgd-tpl="table_rename" data-title="Rename this table" data-ok="Rename" data-op="rename_table" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <label class="form-label small" for="pgd-tr">New name</label><input class="form-control" id="pgd-tr" name="new_name" maxlength="63" autocomplete="off">
</template>
<template data-pgd-tpl="table_schema" data-title="Move this table to another schema" data-ok="Move" data-op="set_schema" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <label class="form-label small" for="pgd-ts">Schema</label>
    <?php if (count($schemas) > 1): ?><select class="form-select" id="pgd-ts" name="new_schema"><?php foreach ($schemas as $sc): ?><option<?= $sc === $schema ? ' selected' : '' ?>><?= e($sc) ?></option><?php endforeach; ?></select>
    <?php else: ?><input class="form-control" id="pgd-ts" name="new_schema" maxlength="63" autocomplete="off"><?php endif; ?>
</template>
<template data-pgd-tpl="table_comment" data-title="Comment on this table" data-ok="Save comment" data-op="comment_table" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <label class="form-label small" for="pgd-tc">Comment (empty removes it)</label><input class="form-control" id="pgd-tc" name="comment" maxlength="500" autocomplete="off">
</template>
<template data-pgd-tpl="table_duplicate" data-title="Duplicate this table" data-ok="Duplicate" data-op="duplicate_table" data-action="<?= e($alter) ?>" data-preview="<?= e($preview) ?>">
    <label class="form-label small" for="pgd-td">Name of the copy</label><input class="form-control" id="pgd-td" name="new_name" maxlength="63" autocomplete="off">
    <label class="form-check mt-2"><input class="form-check-input" type="checkbox" name="with_data" value="1"> copy the rows too</label>
    <div class="form-text">Columns, defaults, constraints, indexes and comments are copied.</div>
</template>
