<?php
/**
 * PostgreSQL table browser, laid out like the MySQL one: a table list on the left and Browse / Structure / Insert / SQL
 * tabs. Browse: the rows of one table (sorted, searched, filtered, paged), with edit and delete where the table has a
 * usable key, and a CSV export. Structure: columns and indexes. Insert: a new row.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var string $schema
 * @var string $table
 * @var string $tab        browse | structure | insert
 * @var array{schemas: list<string>, tables: list<array<string, mixed>>} $tables
 * @var array<string, mixed>|null $result
 * @var array<string, mixed>|null $structure
 * @var bool $canWrite
 */
$id = (int) $db['id'];
$base = $this->url('postgres.browse');
$here = static function (array $over = []) use ($base, $id, $schema, $table, $q, $fc, $fo, $fv, $sort, $dir, $per, $pageNo): string {
    $params = ['db' => $id, 'schema' => $schema, 'table' => $table, 'q' => (string) $q, 'fc' => $fc, 'fo' => $fo, 'fv' => $fv, 'sort' => $sort, 'dir' => $dir, 'per' => $per, 'page' => $pageNo];
    $params = array_filter(array_merge($params, $over), static fn ($v): bool => $v !== '' && $v !== null);
    return $base . '?' . http_build_query($params);
};
$sortLink = static function (string $col) use ($base, $id, $schema, $table, $q, $fc, $fo, $fv, $sort, $dir, $per): string {
    $nextDir = $sort === $col && $dir === 'asc' ? 'desc' : 'asc';
    return $base . '?' . http_build_query(array_filter(['db' => $id, 'schema' => $schema, 'table' => $table, 'q' => $q, 'fc' => $fc, 'fo' => $fo, 'fv' => $fv, 'sort' => $col, 'dir' => $nextDir, 'per' => $per], static fn ($v): bool => $v !== ''));
};
$cols = $result !== null ? (array) $result['columns'] : [];
$total = $result !== null ? (int) $result['total'] : 0;
$pages = max(1, (int) ceil($total / max(1, $per)));
$rowCount = $result !== null ? count((array) $result['rows']) : 0;
$from = $total === 0 ? 0 : ($pageNo - 1) * $per + 1;
$to = $total === 0 ? 0 : min($total, ($pageNo - 1) * $per + $rowCount);
$editable = $result !== null && !empty($result['editable']) && $canWrite;
$numeric = static fn (string $type): bool => preg_match('/^(smallint|integer|bigint|numeric|decimal|real|double precision|money|serial|bigserial)/i', $type) === 1;
$filtering = $fc !== '' || (string) $q !== '';

ob_start();
if ($result === null): ?>
    <div class="nim-db-empty"><i class="bi bi-table" aria-hidden="true"></i><div>Choose a table on the left, or open the SQL tab.</div></div>
<?php elseif ($tab === 'structure'): ?>
    <div class="nim-db-editor">
        <h2 class="h6 mb-2">Columns</h2>
        <div class="table-responsive"><table class="table table-sm nim-db-grid mb-3">
            <thead><tr><th>Column</th><th>Type</th><th>Null</th><th>Default</th><th>Key</th></tr></thead>
            <tbody>
            <?php foreach ((array) ($structure['columns'] ?? []) as $c): ?>
                <tr><td><?= e((string) $c['name']) ?></td><td><?= e((string) $c['type']) ?></td><td><?= !empty($c['not_null']) ? 'NOT NULL' : 'null' ?></td><td><?= e((string) ($c['default'] ?? '')) ?></td><td><?= !empty($c['key']) ? '<i class="bi bi-key-fill text-warning" title="Key column" aria-hidden="true"></i> yes' : '' ?></td></tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
        <h2 class="h6 mb-2">Indexes</h2>
        <?php if (empty($structure['indexes'])): ?>
            <p class="small text-body-secondary">This table has no indexes.</p>
        <?php else: ?>
            <?php foreach ((array) $structure['indexes'] as $ix): ?><pre class="nim-db-create mb-2"><?= e((string) $ix['definition']) ?></pre><?php endforeach; ?>
        <?php endif; ?>
    </div>
<?php elseif ($tab === 'insert'): ?>
    <div class="nim-db-editor">
        <h2 class="h6 mb-3">Insert a row into <span class="font-monospace"><?= e($schema . '.' . $table) ?></span></h2>
        <?php if (!$editable): ?>
            <div class="alert alert-secondary small"><i class="bi bi-lock me-1"></i><?= e(!$canWrite ? 'You may read this database, not change it.' : (string) ($result['reason'] ?? 'This table cannot be changed from here.')) ?></div>
        <?php else: ?>
            <form method="post" action="<?= e($this->url('postgres.row_insert')) ?>" data-nim-busy>
                <?= $this->csrfField() ?>
                <input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="schema" value="<?= e($schema) ?>"><input type="hidden" name="table" value="<?= e($table) ?>">
                <?php foreach ($cols as $c): $gen = !empty($c['generated']); ?>
                    <div class="row g-2 align-items-start mb-2">
                        <div class="col-md-3 small"><div class="font-monospace fw-medium"><?= e((string) $c['name']) ?></div><div class="text-body-secondary"><?= e((string) $c['type']) ?><?= !empty($c['not_null']) ? ' · not null' : '' ?></div></div>
                        <div class="col-md-3">
                            <select class="form-select form-select-sm" name="mode[<?= e((string) $c['name']) ?>]" aria-label="What to do with <?= e((string) $c['name']) ?>" data-pgb-mode<?= $gen ? ' disabled' : '' ?>>
                                <?php if ($gen): ?><option value="keep" selected>Generated</option><?php else: ?>
                                    <option value="default" selected><?= !empty($c['default']) ? 'Default' : 'Default / NULL' ?></option><option value="value">Value</option><option value="null">NULL</option>
                                <?php endif; ?>
                            </select>
                        </div>
                        <div class="col-md-6"><textarea class="form-control form-control-sm font-monospace" rows="1" name="value[<?= e((string) $c['name']) ?>]" aria-label="Value of <?= e((string) $c['name']) ?>" data-pgb-text<?= $gen ? ' disabled' : '' ?>></textarea></div>
                    </div>
                <?php endforeach; ?>
                <div class="d-flex gap-2 mt-3"><button class="btn btn-sm btn-primary">Insert</button><a class="btn btn-sm btn-outline-secondary" href="<?= e($here()) ?>">Cancel</a></div>
            </form>
        <?php endif; ?>
    </div>
<?php else: ?>
    <form class="nim-db-bar" method="get" action="<?= e($base) ?>">
        <input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="schema" value="<?= e($schema) ?>"><input type="hidden" name="table" value="<?= e($table) ?>">
        <?php if ($sort !== ''): ?><input type="hidden" name="sort" value="<?= e($sort) ?>"><input type="hidden" name="dir" value="<?= e($dir) ?>"><?php endif; ?>
        <div class="input-group input-group-sm nim-db-filter">
            <select class="form-select" name="fc" aria-label="Filter column"><option value="">Filter column…</option><?php foreach ($cols as $c): ?><option value="<?= e((string) $c['name']) ?>"<?= $fc === $c['name'] ? ' selected' : '' ?>><?= e((string) $c['name']) ?></option><?php endforeach; ?></select>
            <select class="form-select" name="fo" aria-label="Operator"><?php foreach (['eq' => '=', 'ne' => '!=', 'contains' => 'CONTAINS', 'null' => 'IS NULL', 'notnull' => 'IS NOT NULL'] as $k => $l): ?><option value="<?= e($k) ?>"<?= $fo === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select>
            <input class="form-control" name="fv" value="<?= e($fv) ?>" placeholder="value" aria-label="Filter value">
            <button class="btn btn-outline-secondary">Filter</button>
            <?php if ($filtering): ?><a class="btn btn-outline-secondary" href="<?= e($base . '?' . http_build_query(['db' => $id, 'schema' => $schema, 'table' => $table])) ?>" title="Clear the filter and the search" aria-label="Clear the filter and the search"><i class="bi bi-x-lg"></i></a><?php endif; ?>
        </div>
        <div class="nim-search"><input class="form-control form-control-sm" type="search" name="q" value="<?= e((string) $q) ?>" placeholder="Search all columns" aria-label="Search all columns" maxlength="200"><i class="bi bi-search" aria-hidden="true"></i></div>
        <div class="d-flex align-items-center gap-1 ms-auto">
            <span class="small text-body-secondary me-1"><?= number_format($from) ?>–<?= number_format($to) ?> of <?= number_format($total) ?><?= !empty($result['total_capped']) ? '+' : '' ?></span>
            <a class="btn btn-sm btn-outline-secondary<?= $pageNo <= 1 ? ' disabled' : '' ?>" href="<?= e($here(['page' => 1])) ?>" aria-label="First page"><i class="bi bi-chevron-double-left"></i></a>
            <a class="btn btn-sm btn-outline-secondary<?= $pageNo <= 1 ? ' disabled' : '' ?>" href="<?= e($here(['page' => max(1, $pageNo - 1)])) ?>" aria-label="Previous page"><i class="bi bi-chevron-left"></i></a>
            <a class="btn btn-sm btn-outline-secondary<?= $pageNo >= $pages ? ' disabled' : '' ?>" href="<?= e($here(['page' => $pageNo + 1])) ?>" aria-label="Next page"><i class="bi bi-chevron-right"></i></a>
            <a class="btn btn-sm btn-outline-secondary<?= $pageNo >= $pages ? ' disabled' : '' ?>" href="<?= e($here(['page' => $pages])) ?>" aria-label="Last page"><i class="bi bi-chevron-double-right"></i></a>
            <select class="form-select form-select-sm w-auto" name="per" aria-label="Rows per page" data-nim-autosubmit><?php foreach ([25, 50, 100, 250] as $n): ?><option value="<?= $n ?>"<?= $per === $n ? ' selected' : '' ?>><?= $n ?> / page</option><?php endforeach; ?></select>
        </div>
    </form>
    <div class="nim-db-bar py-1">
        <?php if ($editable): ?><a class="btn btn-sm btn-outline-primary" href="<?= e($base . '?' . http_build_query(['db' => $id, 'schema' => $schema, 'table' => $table, 'tab' => 'insert'])) ?>"><i class="bi bi-plus-lg"></i> Insert row</a><?php endif; ?>
        <a class="btn btn-sm btn-outline-secondary" href="<?= e($here()) ?>"><i class="bi bi-arrow-clockwise"></i> Refresh</a>
        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.export') . '?' . http_build_query(array_filter(['db' => $id, 'schema' => $schema, 'table' => $table, 'q' => $q, 'fc' => $fc, 'fo' => $fo, 'fv' => $fv, 'sort' => $sort, 'dir' => $dir], static fn ($v): bool => $v !== ''))) ?>"><i class="bi bi-filetype-csv"></i> Export CSV</a>
        <div class="dropdown">
            <button class="btn btn-sm btn-outline-secondary dropdown-toggle" type="button" data-bs-toggle="dropdown" data-bs-auto-close="outside" aria-expanded="false"><i class="bi bi-layout-three-columns"></i> Columns</button>
            <div class="dropdown-menu p-2" style="min-width:14rem">
                <?php foreach ($cols as $i => $c): ?>
                    <label class="dropdown-item-text d-flex gap-2 small"><input type="checkbox" class="form-check-input" data-pgb-col="<?= (int) $i ?>" checked><span class="font-monospace"><?= e((string) $c['name']) ?></span></label>
                <?php endforeach; ?>
            </div>
        </div>
        <?php if (empty($result['editable'])): ?><span class="small text-body-secondary ms-2"><i class="bi bi-lock me-1"></i><?= e((string) ($result['reason'] ?? '')) ?></span><?php endif; ?>
    </div>
    <div class="nim-db-grid-wrap">
        <table class="table table-sm table-hover nim-db-grid mb-0" id="pgb-grid">
            <thead><tr>
                <?php if ($editable): ?><th class="nim-db-check"></th><?php endif; ?>
                <?php foreach ($cols as $i => $c): ?>
                    <th data-c="<?= (int) $i ?>"<?= $numeric((string) $c['type']) ? ' class="text-end"' : '' ?><?= $sort === $c['name'] ? ' aria-sort="' . ($dir === 'desc' ? 'descending' : 'ascending') . '"' : '' ?>><a class="text-reset fw-semibold text-decoration-none" title="<?= e((string) $c['type']) ?> — sort" href="<?= e($sortLink((string) $c['name'])) ?>"><?= e((string) $c['name']) ?><?= $sort === $c['name'] ? '<i class="bi ms-1 ' . ($dir === 'desc' ? 'bi-caret-down-fill' : 'bi-caret-up-fill') . '"></i>' : '' ?></a><?= !empty($c['key']) ? ' <i class="bi bi-key-fill text-warning" title="Key column" aria-hidden="true"></i>' : '' ?></th>
                <?php endforeach; ?>
            </tr></thead>
            <tbody>
            <?php
            $cut = [];
            foreach ((array) $result['truncated'] as $tc) { $cut[(int) $tc[0] . ':' . (int) $tc[1]] = true; }
            foreach ((array) $result['rows'] as $ri => $row):
                $key = [];
                $vals = [];
                $cutNames = [];
                foreach ($cols as $ci => $c) {
                    $vals[(string) $c['name']] = $row[$ci];
                    if (isset($cut[$ri . ':' . $ci])) { $cutNames[] = (string) $c['name']; }
                    if (!empty($c['key'])) { $key[(string) $c['name']] = $row[$ci]; }
                }
            ?>
                <tr>
                    <?php if ($editable): ?>
                        <td class="nim-db-check text-nowrap">
                            <button class="btn btn-link p-0" type="button" title="Edit" aria-label="Edit row" data-bs-toggle="modal" data-bs-target="#pgb-row" data-pgb-mode="edit" data-key="<?= e((string) json_encode($key)) ?>" data-values="<?= e((string) json_encode($vals)) ?>" data-cut="<?= e((string) json_encode($cutNames)) ?>"><i class="bi bi-pencil"></i></button>
                            <form class="d-inline" method="post" action="<?= e($this->url('postgres.row_delete')) ?>" data-nim-confirm="Delete this row? This cannot be undone.">
                                <?= $this->csrfField() ?>
                                <input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="schema" value="<?= e($schema) ?>"><input type="hidden" name="table" value="<?= e($table) ?>"><input type="hidden" name="page" value="<?= $pageNo ?>">
                                <?php foreach ($key as $kc => $kv): ?><input type="hidden" name="key[<?= e((string) $kc) ?>]" value="<?= e((string) $kv) ?>"><?php endforeach; ?>
                                <button class="btn btn-link p-0 text-danger" title="Delete" aria-label="Delete row"><i class="bi bi-trash"></i></button>
                            </form>
                        </td>
                    <?php endif; ?>
                    <?php foreach ($cols as $ci => $c): $v = $row[$ci]; ?>
                        <td data-c="<?= (int) $ci ?>"<?= $numeric((string) $c['type']) ? ' class="text-end"' : '' ?>>
                            <?php if ($v === null): ?><span class="nim-db-null">NULL</span>
                            <?php else: ?><span title="<?= e(mb_substr((string) $v, 0, 300)) ?>"><?= e((string) $v) ?></span><?= isset($cut[$ri . ':' . $ci]) ? ' <span class="badge text-bg-warning" title="Only the first 2,048 characters are shown">cut</span>' : '' ?><?php endif; ?>
                        </td>
                    <?php endforeach; ?>
                </tr>
            <?php endforeach; ?>
            <?php if ($rowCount === 0): ?><tr><td colspan="<?= count($cols) + ($editable ? 1 : 0) ?>" class="text-center small text-body-secondary py-4">No rows.</td></tr><?php endif; ?>
            </tbody>
        </table>
    </div>
<?php endif;
$inner = (string) ob_get_clean();
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres')) ?>"><i class="bi bi-arrow-left"></i> PostgreSQL</a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-table me-1"></i>Database <span class="font-monospace"><?= e((string) $db['name']) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.backups') . '?db=' . $id) ?>"><i class="bi bi-cloud-arrow-down"></i> Backups</a><?php $this->end('actions'); ?>

<?= $this->partial('postgres/_frame', ['db' => $db, 'schema' => $schema, 'table' => $table, 'tables' => $tables, 'tab' => $tab, 'inner' => $inner]) ?>

<?php if ($editable && $tab === 'browse'): ?>
<div class="modal fade" id="pgb-row" data-columns="<?= e((string) json_encode($cols)) ?>" tabindex="-1" aria-labelledby="pgb-row-t" aria-hidden="true"><div class="modal-dialog modal-lg modal-dialog-scrollable"><form class="modal-content" method="post" action="<?= e($this->url('postgres.row_insert')) ?>" data-pgb-form data-insert="<?= e($this->url('postgres.row_insert')) ?>" data-update="<?= e($this->url('postgres.row_update')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="schema" value="<?= e($schema) ?>"><input type="hidden" name="table" value="<?= e($table) ?>"><input type="hidden" name="page" value="<?= $pageNo ?>">
    <div class="modal-header"><h2 class="modal-title fs-5" id="pgb-row-t">Row</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
    <div class="modal-body" data-pgb-fields></div>
    <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary">Save</button></div>
</form></div></div>
<?php endif; ?>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/postgres-frame.js')) ?>" defer></script><script src="<?= e($this->asset('assets/js/postgres-browser.js')) ?>" defer></script><?php $this->end('scripts'); ?>
