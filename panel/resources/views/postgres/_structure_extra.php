<?php
/**
 * The Structure tab's indexes, constraints and triggers of one PostgreSQL table, with the forms to change them.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var string $schema
 * @var string $table
 * @var array<string, mixed>|null $structure
 * @var array<string, mixed>|null $details
 * @var array<string, mixed>|null $partitions
 * @var bool $canAlter
 * @var string $hid   hidden db/schema/table fields (already escaped)
 */
$d = (array) $details;
$form = function (string $op, array $fields, string $label, string $cls = 'btn-outline-secondary', string $confirm = '') use ($hid): string {
    $h = '<form class="d-inline" method="post" action="' . e($this->url('postgres.alter')) . '"' . ($confirm !== '' ? ' data-nim-confirm="' . e($confirm) . '"' : '') . '>' . $this->csrfField() . $hid . '<input type="hidden" name="op" value="' . e($op) . '">';
    foreach ($fields as $k => $v) {
        $h .= '<input type="hidden" name="' . e((string) $k) . '" value="' . e((string) $v) . '">';
    }
    return $h . '<button class="btn btn-sm ' . e($cls) . '">' . e($label) . '</button></form>';
};
$columns = array_map(static fn ($c): string => (string) $c['name'], (array) ($structure['columns'] ?? []));
$ops = ['eq' => '=', 'ne' => '<>', 'lt' => '<', 'le' => '<=', 'gt' => '>', 'ge' => '>=', 'null' => 'is null', 'not_null' => 'is not null', 'like' => 'like', 'between' => 'between', 'in' => 'in (a, b, ...)'];
$predicate = function (string $legend) use ($columns, $ops): string {
    $h = '<div class="row g-1 align-items-center mb-1"><div class="col-12 small text-body-secondary">' . e($legend) . '</div>';
    $h .= '<div class="col-6 col-md-3"><select class="form-select form-select-sm" name="pcol" aria-label="Condition column"><option value="">column…</option>';
    foreach ($columns as $c) {
        $h .= '<option>' . e($c) . '</option>';
    }
    $h .= '</select></div><div class="col-6 col-md-2"><select class="form-select form-select-sm" name="pop" aria-label="Condition operator"><option value="">operator…</option>';
    foreach ($ops as $k => $v) {
        $h .= '<option value="' . e($k) . '">' . e($v) . '</option>';
    }
    return $h . '</select></div><div class="col-6 col-md-3"><input class="form-control form-control-sm" name="pval" placeholder="value" maxlength="200" aria-label="Condition value"></div><div class="col-6 col-md-3"><input class="form-control form-control-sm" name="pval2" placeholder="upper value (between)" maxlength="200" aria-label="Condition upper value"></div></div>';
};
$indexes = (array) ($d['indexes'] ?? []);
$constraints = (array) ($d['constraints'] ?? []);
$triggers = (array) ($d['triggers'] ?? []);
?>
<h2 class="h6 mb-2">Indexes</h2>
<?php if ($indexes === []): ?>
    <p class="small text-body-secondary">This table has no indexes.</p>
<?php else: ?>
<div class="table-responsive"><table class="table table-sm nim-db-grid mb-2">
    <thead><tr><th>Name</th><th>Columns</th><th>Method</th><th class="text-end">Size</th><th class="text-end">Scans</th><th>Notes</th><?php if ($canAlter): ?><th></th><?php endif; ?></tr></thead>
    <tbody>
    <?php foreach ($indexes as $ix): $n = (string) $ix['name']; ?>
        <tr>
            <td class="font-monospace"><?= e($n) ?></td><td class="font-monospace small"><?= e((string) $ix['columns']) ?></td><td><?= e((string) $ix['method']) ?></td>
            <td class="text-end"><?= e(format_bytes((int) $ix['bytes'])) ?></td><td class="text-end"><?= number_format((int) $ix['scans']) ?></td>
            <td class="small">
                <?= !empty($ix['primary']) ? '<span class="badge text-bg-warning">primary</span> ' : (!empty($ix['unique']) ? '<span class="badge text-bg-info">unique</span> ' : '') ?>
                <?= empty($ix['valid']) ? '<span class="badge text-bg-danger" title="The index is not usable (a concurrent build failed). Rebuild or drop it.">invalid</span> ' : '' ?>
                <?= (string) $ix['predicate'] !== '' ? '<span class="text-body-secondary">where ' . e((string) $ix['predicate']) . '</span>' : '' ?>
                <?= (string) ($ix['backs_constraint'] ?? '') !== '' ? '<span class="text-body-secondary">backs constraint ' . e((string) $ix['backs_constraint']) . '</span>' : '' ?>
                <?= (int) $ix['scans'] === 0 && empty($ix['primary']) && empty($ix['unique']) ? '<span class="text-body-secondary" title="Since the statistics were last reset">never used</span>' : '' ?>
            </td>
            <?php if ($canAlter): ?><td class="text-end text-nowrap">
                <?= $form('reindex_index', ['name' => $n], 'Rebuild') ?>
                <details class="d-inline-block"><summary class="btn btn-sm btn-outline-secondary">Rename</summary>
                    <form class="d-flex gap-1 mt-1" method="post" action="<?= e($this->url('postgres.alter')) ?>"><?= $this->csrfField() ?><?= $hid ?><input type="hidden" name="op" value="rename_index"><input type="hidden" name="name" value="<?= e($n) ?>">
                        <input class="form-control form-control-sm" name="new_name" placeholder="new name" maxlength="63" aria-label="New name of <?= e($n) ?>"><button class="btn btn-sm btn-outline-primary">Rename</button></form></details>
                <?php if ((string) ($ix['backs_constraint'] ?? '') === ''): ?><?= $form('drop_index', ['name' => $n], 'Drop', 'btn-outline-danger', 'Drop the index ' . $n . '? Queries that use it may become slower. The table data is not changed.') ?><?php endif; ?>
            </td><?php endif; ?>
        </tr>
    <?php endforeach; ?>
    </tbody>
</table></div>
<?php endif; ?>
<?php if ($canAlter): ?>
<details class="mb-3"><summary class="btn btn-sm btn-outline-primary"><i class="bi bi-plus-lg"></i> Create an index</summary>
    <form class="border rounded p-2 mt-2" method="post" action="<?= e($this->url('postgres.alter')) ?>"><?= $this->csrfField() ?><?= $hid ?><input type="hidden" name="op" value="create_index">
        <div class="row g-1 mb-1">
            <div class="col-12 col-md-3"><input class="form-control form-control-sm" name="name" placeholder="name (optional)" maxlength="63" aria-label="Index name"></div>
            <div class="col-6 col-md-2"><select class="form-select form-select-sm" name="method" aria-label="Index method"><?php foreach (['btree', 'hash', 'gin', 'gist', 'brin', 'spgist'] as $m): ?><option><?= e($m) ?></option><?php endforeach; ?></select></div>
            <div class="col-12 col-md-7 d-flex flex-wrap gap-3 small align-items-center">
                <label class="form-check mb-0"><input class="form-check-input" type="checkbox" name="unique" value="1"> unique</label>
                <label class="form-check mb-0" title="Builds without blocking writes; slower, and cannot be used on partitioned tables."><input class="form-check-input" type="checkbox" name="concurrent" value="1"> without locking writes</label>
            </div>
        </div>
        <?php for ($i = 0; $i < 3; $i++): ?>
        <div class="row g-1 mb-1">
            <div class="col-6 col-md-3"><select class="form-select form-select-sm" name="icols[<?= $i ?>][name]" aria-label="Index column <?= $i + 1 ?>"><option value=""><?= $i === 0 ? 'column…' : 'another column (optional)' ?></option><?php foreach ($columns as $c): ?><option><?= e($c) ?></option><?php endforeach; ?></select></div>
            <div class="col-3 col-md-2"><select class="form-select form-select-sm" name="icols[<?= $i ?>][order]" aria-label="Order"><option value="">ascending</option><option value="desc">descending</option></select></div>
            <div class="col-3 col-md-2"><select class="form-select form-select-sm" name="icols[<?= $i ?>][nulls]" aria-label="NULLs"><option value="">nulls default</option><option value="first">nulls first</option><option value="last">nulls last</option></select></div>
        </div>
        <?php endfor; ?>
        <div class="row g-1 mb-1"><div class="col-12 col-md-6"><input class="form-control form-control-sm" name="include" placeholder="also store these columns (comma-separated, optional)" maxlength="300" aria-label="Included columns"></div></div>
        <?= $predicate('Only index rows where (optional):') ?>
        <button class="btn btn-sm btn-primary mt-1">Create index</button>
    </form>
</details>
<?php endif; ?>

<h2 class="h6 mb-2">Constraints</h2>
<?php if ($constraints === []): ?>
    <p class="small text-body-secondary">This table has no constraints.</p>
<?php else: ?>
<div class="table-responsive"><table class="table table-sm nim-db-grid mb-2">
    <thead><tr><th>Name</th><th>Type</th><th>Definition</th><th>State</th><?php if ($canAlter): ?><th></th><?php endif; ?></tr></thead>
    <tbody>
    <?php foreach ($constraints as $c): $n = (string) $c['name']; ?>
        <tr>
            <td class="font-monospace"><?= e($n) ?></td><td><?= e((string) $c['type']) ?></td><td class="font-monospace small"><?= e((string) $c['definition']) ?></td>
            <td class="small"><?= empty($c['validated']) ? '<span class="badge text-bg-warning" title="Added with NOT VALID: new rows are checked, existing rows are not yet.">not validated</span> ' : '' ?><?= !empty($c['deferrable']) ? 'deferrable' : '' ?></td>
            <?php if ($canAlter): ?><td class="text-end text-nowrap">
                <?php if (empty($c['validated'])): ?><?= $form('validate_constraint', ['name' => $n], 'Validate') ?><?php endif; ?>
                <details class="d-inline-block"><summary class="btn btn-sm btn-outline-secondary">Rename</summary>
                    <form class="d-flex gap-1 mt-1" method="post" action="<?= e($this->url('postgres.alter')) ?>"><?= $this->csrfField() ?><?= $hid ?><input type="hidden" name="op" value="rename_constraint"><input type="hidden" name="name" value="<?= e($n) ?>">
                        <input class="form-control form-control-sm" name="new_name" placeholder="new name" maxlength="63" aria-label="New name of <?= e($n) ?>"><button class="btn btn-sm btn-outline-primary">Rename</button></form></details>
                <?= $form('drop_constraint', ['name' => $n], 'Drop', 'btn-outline-danger', 'Drop the constraint ' . $n . '? The data stays, but it is no longer enforced.') ?>
            </td><?php endif; ?>
        </tr>
    <?php endforeach; ?>
    </tbody>
</table></div>
<?php endif; ?>
<?php if ($canAlter): ?>
<details class="mb-3"><summary class="btn btn-sm btn-outline-primary"><i class="bi bi-plus-lg"></i> Add a constraint</summary>
    <form class="border rounded p-2 mt-2" method="post" action="<?= e($this->url('postgres.alter')) ?>"><?= $this->csrfField() ?><?= $hid ?><input type="hidden" name="op" value="add_constraint">
        <div class="row g-1 mb-1">
            <div class="col-12 col-md-3"><input class="form-control form-control-sm" name="name" placeholder="name (optional)" maxlength="63" aria-label="Constraint name"></div>
            <div class="col-6 col-md-3"><select class="form-select form-select-sm" name="kind" aria-label="Constraint type"><option value="unique">unique</option><option value="primary">primary key</option><option value="check">check</option><option value="foreign">foreign key</option></select></div>
            <div class="col-12 col-md-6"><input class="form-control form-control-sm" name="columns" placeholder="columns, comma-separated (unique, primary, foreign)" maxlength="300" aria-label="Columns"></div>
        </div>
        <?= $predicate('Check: the condition every row must satisfy:') ?>
        <div class="row g-1 mb-1"><div class="col-12 small text-body-secondary">Foreign key: references</div>
            <div class="col-6 col-md-2"><input class="form-control form-control-sm" name="ref_schema" value="<?= e($schema) ?>" maxlength="63" aria-label="Referenced schema"></div>
            <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="ref_table" placeholder="table" maxlength="63" aria-label="Referenced table"></div>
            <div class="col-12 col-md-3"><input class="form-control form-control-sm" name="ref_columns" placeholder="columns, comma-separated" maxlength="300" aria-label="Referenced columns"></div>
            <div class="col-6 col-md-2"><select class="form-select form-select-sm" name="on_delete" aria-label="On delete"><?php foreach (['' => 'on delete: no action', 'restrict' => 'restrict', 'cascade' => 'cascade', 'set null' => 'set null', 'set default' => 'set default'] as $k => $v): ?><option value="<?= e($k) ?>"><?= e($v) ?></option><?php endforeach; ?></select></div>
            <div class="col-6 col-md-2"><select class="form-select form-select-sm" name="on_update" aria-label="On update"><?php foreach (['' => 'on update: no action', 'restrict' => 'restrict', 'cascade' => 'cascade', 'set null' => 'set null', 'set default' => 'set default'] as $k => $v): ?><option value="<?= e($k) ?>"><?= e($v) ?></option><?php endforeach; ?></select></div>
        </div>
        <div class="d-flex flex-wrap gap-3 small mb-1">
            <label class="form-check mb-0" title="Check new rows now, existing rows later with Validate. Avoids a long lock on big tables."><input class="form-check-input" type="checkbox" name="not_valid" value="1"> do not check existing rows yet (check and foreign key)</label>
            <label class="form-check mb-0"><input class="form-check-input" type="checkbox" name="deferrable" value="1"> deferrable (foreign key)</label>
        </div>
        <button class="btn btn-sm btn-primary mt-1">Add constraint</button>
    </form>
</details>
<?php endif; ?>

<h2 class="h6 mb-2">Triggers</h2>
<?php if ($triggers === []): ?>
    <p class="small text-body-secondary">This table has no triggers. Create one from the SQL tab, since its body is code.</p>
<?php else: ?>
<div class="table-responsive"><table class="table table-sm nim-db-grid mb-2">
    <thead><tr><th>Name</th><th>State</th><th>Definition</th><?php if ($canAlter): ?><th></th><?php endif; ?></tr></thead>
    <tbody>
    <?php foreach ($triggers as $tg): $n = (string) $tg['name']; $on = (string) $tg['enabled'] !== 'disabled'; ?>
        <tr><td class="font-monospace"><?= e($n) ?></td><td><?= e((string) $tg['enabled']) ?></td><td class="font-monospace small"><?= e((string) $tg['definition']) ?></td>
            <?php if ($canAlter): ?><td class="text-end text-nowrap">
                <?= $form($on ? 'disable_trigger' : 'enable_trigger', ['name' => $n], $on ? 'Disable' : 'Enable') ?>
                <?= $form('drop_trigger', ['name' => $n], 'Drop', 'btn-outline-danger', 'Drop the trigger ' . $n . '? The function it calls stays.') ?>
            </td><?php endif; ?></tr>
    <?php endforeach; ?>
    </tbody>
</table></div>
<?php endif; ?>

<?php $pt = (array) $partitions; if (!empty($pt['partitioned'])): ?>
<h2 class="h6 mb-2 mt-3">Partitions <span class="text-body-secondary fw-normal small">by <?= e((string) $pt['key']) ?></span></h2>
<?php if ((array) $pt['partitions'] === []): ?>
    <p class="small text-body-secondary">No partitions yet. Rows cannot be inserted until a partition accepts them.</p>
<?php else: ?>
<div class="table-responsive"><table class="table table-sm nim-db-grid mb-2">
    <thead><tr><th>Partition</th><th>Bounds</th><th class="text-end">Rows (est.)</th><th class="text-end">Size</th><?php if ($canAlter): ?><th></th><?php endif; ?></tr></thead>
    <tbody>
    <?php foreach ((array) $pt['partitions'] as $p): ?>
        <tr><td class="font-monospace"><a href="<?= e($this->url('postgres.browse') . '?' . http_build_query(['db' => (int) $db['id'], 'schema' => $p['schema'], 'table' => $p['name']])) ?>"><?= e((string) $p['name']) ?></a></td>
            <td class="font-monospace small"><?= e((string) $p['bound']) ?></td><td class="text-end"><?= number_format((int) $p['rows_estimate']) ?></td><td class="text-end"><?= e(format_bytes((int) $p['total_bytes'])) ?></td>
            <?php if ($canAlter): ?><td class="text-end"><?= $form('detach_partition', ['name' => (string) $p['name']], 'Detach', 'btn-outline-secondary', 'Detach the partition ' . $p['name'] . '? It becomes a normal table with its rows; the partitioned table no longer includes them.') ?></td><?php endif; ?></tr>
    <?php endforeach; ?>
    </tbody>
</table></div>
<?php endif; ?>
<?php if ($canAlter): ?>
<details class="mb-3"><summary class="btn btn-sm btn-outline-primary"><i class="bi bi-plus-lg"></i> Add a partition</summary>
    <form class="border rounded p-2 mt-2" method="post" action="<?= e($this->url('postgres.alter')) ?>"><?= $this->csrfField() ?><?= $hid ?>
        <div class="row g-1 mb-1">
            <div class="col-6 col-md-3"><select class="form-select form-select-sm" name="op" aria-label="Create a new partition or attach an existing table"><option value="create_partition">create a new partition</option><option value="attach_partition">attach an existing table</option></select></div>
            <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="new_name" placeholder="new partition name" maxlength="63" aria-label="New partition name"></div>
            <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="name" placeholder="or: table to attach" maxlength="63" aria-label="Table to attach"></div>
            <div class="col-6 col-md-3"><select class="form-select form-select-sm" name="bound_kind" aria-label="Bound type"><option value="range">range (from / to)</option><option value="list">list (values)</option><option value="hash">hash (modulus / remainder)</option><option value="default">default partition</option></select></div>
        </div>
        <div class="row g-1 mb-1">
            <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="bound_from" placeholder="from (MINVALUE allowed)" aria-label="From"></div>
            <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="bound_to" placeholder="to, exclusive (MAXVALUE allowed)" aria-label="To"></div>
            <div class="col-12 col-md-3"><input class="form-control form-control-sm" name="bound_values" placeholder="values, comma-separated" aria-label="Values"></div>
            <div class="col-6 col-md-1"><input class="form-control form-control-sm" name="bound_modulus" placeholder="mod" aria-label="Modulus"></div>
            <div class="col-6 col-md-2"><input class="form-control form-control-sm" name="bound_remainder" placeholder="remainder" aria-label="Remainder"></div>
        </div>
        <button class="btn btn-sm btn-primary mt-1">Add partition</button>
    </form>
</details>
<?php endif; ?>
<?php endif; ?>
