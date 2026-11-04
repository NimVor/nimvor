<?php
/**
 * The Structure tab's indexes, constraints, triggers and partitions of one PostgreSQL table. Creating and renaming open
 * in the page's dialog (see _dialogs_structure.php); the small one-click changes (rebuild, validate, enable, drop) ask
 * for confirmation and post their own form.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var string $schema
 * @var string $table
 * @var array<string, mixed>|null $structure
 * @var array<string, mixed>|null $details
 * @var array<string, mixed>|null $partitions
 * @var bool $canAlter
 */
$d = (array) $details;
$hid = '<input type="hidden" name="db" value="' . (int) $db['id'] . '"><input type="hidden" name="schema" value="' . e($schema) . '"><input type="hidden" name="table" value="' . e($table) . '">';
$form = function (string $op, array $fields, string $label, string $cls = 'btn-outline-secondary', string $confirm = '') use ($hid): string {
    $h = '<form class="d-inline" method="post" action="' . e($this->url('postgres.alter')) . '"' . ($confirm !== '' ? ' data-nim-confirm="' . e($confirm) . '"' : '') . '>' . $this->csrfField() . $hid . '<input type="hidden" name="op" value="' . e($op) . '">';
    foreach ($fields as $k => $v) {
        $h .= '<input type="hidden" name="' . e((string) $k) . '" value="' . e((string) $v) . '">';
    }
    return $h . '<button class="btn btn-sm ' . e($cls) . '">' . e($label) . '</button></form>';
};
$open = static fn (string $dialog, array $set, string $label, string $cls = 'btn-outline-secondary'): string => '<button type="button" class="btn btn-sm ' . e($cls) . '" data-pgd-open="' . e($dialog) . '" data-pgd-set="' . e((string) json_encode($set)) . '">' . e($label) . '</button>';
$indexes = (array) ($d['indexes'] ?? []);
$constraints = (array) ($d['constraints'] ?? []);
$triggers = (array) ($d['triggers'] ?? []);
?>
<div class="d-flex align-items-center gap-2 mb-2"><h2 class="h6 mb-0">Indexes</h2>
    <?php if ($canAlter): ?><button type="button" class="btn btn-sm btn-outline-primary ms-auto" data-pgd-open="create_index"><i class="bi bi-plus-lg"></i> Create index</button><?php endif; ?></div>
<?php if ($indexes === []): ?>
    <p class="small text-body-secondary">This table has no indexes.</p>
<?php else: ?>
<div class="table-responsive"><table class="table table-sm nim-db-grid mb-3">
    <thead><tr><th>Name</th><th>Columns</th><th>Method</th><th class="text-end">Size</th><th class="text-end">Scans</th><th>Notes</th><?php if ($canAlter): ?><th></th><?php endif; ?></tr></thead>
    <tbody>
    <?php foreach ($indexes as $ix): $n = (string) $ix['name']; ?>
        <tr>
            <td class="font-monospace"><?= e($n) ?></td><td class="font-monospace small"><?= e((string) $ix['columns']) ?></td><td><?= e((string) $ix['method']) ?></td>
            <td class="text-end"><?= e(format_bytes((int) $ix['bytes'])) ?></td><td class="text-end"><?= number_format((int) $ix['scans']) ?></td>
            <td class="small">
                <?= !empty($ix['primary']) ? '<span class="badge text-bg-warning">primary</span> ' : (!empty($ix['unique']) ? '<span class="badge text-bg-info">unique</span> ' : '') ?>
                <?= empty($ix['valid']) ? '<span class="badge text-bg-danger" title="The index is not usable (a concurrent build failed). Rebuild or drop it.">invalid</span> ' : '' ?>
                <?= (string) ($ix['predicate'] ?? '') !== '' ? '<span class="text-body-secondary">where ' . e((string) $ix['predicate']) . '</span>' : '' ?>
                <?= (string) ($ix['backs_constraint'] ?? '') !== '' ? '<span class="text-body-secondary">backs constraint ' . e((string) $ix['backs_constraint']) . '</span>' : '' ?>
                <?= (int) $ix['scans'] === 0 && empty($ix['primary']) && empty($ix['unique']) ? '<span class="text-body-secondary" title="Since the statistics were last reset">never used</span>' : '' ?>
            </td>
            <?php if ($canAlter): ?><td class="text-end text-nowrap">
                <?= $form('reindex_index', ['name' => $n], 'Rebuild') ?>
                <?= $open('rename_index', ['name' => $n, 'new_name' => $n], 'Rename') ?>
                <?php if ((string) ($ix['backs_constraint'] ?? '') === ''): ?><?= $form('drop_index', ['name' => $n], 'Drop', 'btn-outline-danger', 'Drop the index ' . $n . '? Queries that use it may become slower. The table data is not changed.') ?><?php endif; ?>
            </td><?php endif; ?>
        </tr>
    <?php endforeach; ?>
    </tbody>
</table></div>
<?php endif; ?>

<div class="d-flex align-items-center gap-2 mb-2"><h2 class="h6 mb-0">Constraints</h2>
    <?php if ($canAlter): ?><button type="button" class="btn btn-sm btn-outline-primary ms-auto" data-pgd-open="add_constraint"><i class="bi bi-plus-lg"></i> Add constraint</button><?php endif; ?></div>
<?php if ($constraints === []): ?>
    <p class="small text-body-secondary">This table has no constraints.</p>
<?php else: ?>
<div class="table-responsive"><table class="table table-sm nim-db-grid mb-3">
    <thead><tr><th>Name</th><th>Type</th><th>Definition</th><th>State</th><?php if ($canAlter): ?><th></th><?php endif; ?></tr></thead>
    <tbody>
    <?php foreach ($constraints as $c): $n = (string) $c['name']; ?>
        <tr>
            <td class="font-monospace"><?= e($n) ?></td><td><?= e((string) $c['type']) ?></td><td class="font-monospace small"><?= e((string) $c['definition']) ?></td>
            <td class="small"><?= empty($c['validated']) ? '<span class="badge text-bg-warning" title="Added with NOT VALID: new rows are checked, existing rows are not yet.">not validated</span> ' : '' ?><?= !empty($c['deferrable']) ? 'deferrable' : '' ?></td>
            <?php if ($canAlter): ?><td class="text-end text-nowrap">
                <?php if (empty($c['validated'])): ?><?= $form('validate_constraint', ['name' => $n], 'Validate') ?><?php endif; ?>
                <?= $open('rename_constraint', ['name' => $n, 'new_name' => $n], 'Rename') ?>
                <?= $form('drop_constraint', ['name' => $n], 'Drop', 'btn-outline-danger', 'Drop the constraint ' . $n . '? The data stays, but it is no longer enforced.') ?>
            </td><?php endif; ?>
        </tr>
    <?php endforeach; ?>
    </tbody>
</table></div>
<?php endif; ?>

<h2 class="h6 mb-2">Triggers</h2>
<?php if ($triggers === []): ?>
    <p class="small text-body-secondary">This table has no triggers. Create one from the SQL tab, since its body is code.</p>
<?php else: ?>
<div class="table-responsive"><table class="table table-sm nim-db-grid mb-3">
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
<div class="d-flex align-items-center gap-2 mb-2"><h2 class="h6 mb-0">Partitions <span class="text-body-secondary fw-normal small">by <?= e((string) $pt['key']) ?></span></h2>
    <?php if ($canAlter): ?><button type="button" class="btn btn-sm btn-outline-primary ms-auto" data-pgd-open="add_partition"><i class="bi bi-plus-lg"></i> Add partition</button><?php endif; ?></div>
<?php if ((array) $pt['partitions'] === []): ?>
    <p class="small text-body-secondary">No partitions yet. Rows cannot be inserted until a partition accepts them.</p>
<?php else: ?>
<div class="table-responsive"><table class="table table-sm nim-db-grid mb-3">
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
<?php endif; ?>
