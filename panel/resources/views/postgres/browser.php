<?php
/**
 * PostgreSQL table browser: a database's tables on the left, the rows of one table on the right (sorted, searched,
 * filtered, paged), with add, edit and delete where the table has a usable key, and a CSV export.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var string $schema
 * @var string $table
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
$cols = $result !== null ? (array) $result['columns'] : [];
$total = $result !== null ? (int) $result['total'] : 0;
$pages = max(1, (int) ceil($total / max(1, $per)));
$editable = $result !== null && !empty($result['editable']) && $canWrite;
$link = static function (string $sch, string $tbl) use ($base, $id): string {
    return $base . '?' . http_build_query(['db' => $id, 'schema' => $sch, 'table' => $tbl]);
};
$sortLink = static function (string $col) use ($base, $id, $schema, $table, $q, $fc, $fo, $fv, $sort, $dir, $per): string {
    $nextDir = $sort === $col && $dir === 'asc' ? 'desc' : 'asc';
    return $base . '?' . http_build_query(array_filter(['db' => $id, 'schema' => $schema, 'table' => $table, 'q' => $q, 'fc' => $fc, 'fo' => $fo, 'fv' => $fv, 'sort' => $col, 'dir' => $nextDir, 'per' => $per], static fn ($v): bool => $v !== ''));
};
?>
<?php $this->start(); ?><?= e((string) $db['name']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?>PostgreSQL database<?php $this->end('subtitle'); ?>

<p class="mb-2 d-flex gap-3"><a href="<?= e($this->url('postgres')) ?>" class="small"><i class="bi bi-arrow-left"></i> PostgreSQL</a><a href="<?= e($this->url('postgres.sql') . '?db=' . $id) ?>" class="small"><i class="bi bi-terminal"></i> SQL editor</a></p>

<div class="row g-3">
    <div class="col-lg-3">
        <div class="card">
            <div class="card-header fw-medium small"><i class="bi bi-table me-1" aria-hidden="true"></i>Tables</div>
            <?php if ($tables['tables'] === []): ?>
                <div class="card-body small text-body-secondary">This database has no tables yet. Create them from your application or with a client such as <code>psql</code>.</div>
            <?php else: ?>
                <div class="list-group list-group-flush small">
                <?php $lastSchema = null; foreach ($tables['tables'] as $t): ?>
                    <?php if ($t['schema'] !== $lastSchema): $lastSchema = $t['schema']; ?><div class="list-group-item bg-body-tertiary text-body-secondary py-1"><?= e((string) $t['schema']) ?></div><?php endif; ?>
                    <a class="list-group-item list-group-item-action d-flex align-items-center gap-1<?= $t['schema'] === $schema && $t['name'] === $table ? ' active' : '' ?>" href="<?= e($link((string) $t['schema'], (string) $t['name'])) ?>">
                        <i class="bi bi-<?= $t['kind'] === 'table' ? 'table' : 'eye' ?>" aria-hidden="true"></i>
                        <span class="text-truncate font-monospace"><?= e((string) $t['name']) ?></span>
                        <span class="ms-auto small opacity-75"><?= (int) $t['rows_estimate'] > 0 ? '~' . number_format((int) $t['rows_estimate']) : '' ?></span>
                    </a>
                <?php endforeach; ?>
                </div>
            <?php endif; ?>
        </div>
    </div>

    <div class="col-lg-9">
    <?php if ($result === null): ?>
        <div class="card"><div class="card-body small text-body-secondary">Choose a table on the left.</div></div>
    <?php else: ?>
        <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
            <h2 class="h6 mb-0 font-monospace"><?= e($schema) ?>.<?= e($table) ?></h2>
            <span class="badge text-bg-light border"><?= e((string) $result['kind'] === 'r' || (string) $result['kind'] === 'p' ? 'table' : 'read-only') ?></span>
            <span class="small text-body-secondary"><?= number_format($total) ?><?= !empty($result['total_capped']) ? '+' : '' ?> row<?= $total === 1 ? '' : 's' ?></span>
            <span class="me-auto"></span>
            <div class="dropdown">
                <button class="btn btn-sm btn-outline-secondary dropdown-toggle" type="button" data-bs-toggle="dropdown" data-bs-auto-close="outside"><i class="bi bi-layout-three-columns me-1"></i>Columns</button>
                <div class="dropdown-menu p-2" style="min-width:14rem">
                    <?php foreach ($cols as $i => $c): ?>
                        <label class="dropdown-item-text d-flex gap-2 small"><input type="checkbox" class="form-check-input" data-pgb-col="<?= (int) $i ?>" checked><span class="font-monospace"><?= e((string) $c['name']) ?></span></label>
                    <?php endforeach; ?>
                </div>
            </div>
            <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.export') . '?' . http_build_query(array_filter(['db' => $id, 'schema' => $schema, 'table' => $table, 'q' => $q, 'fc' => $fc, 'fo' => $fo, 'fv' => $fv, 'sort' => $sort, 'dir' => $dir], static fn ($v): bool => $v !== ''))) ?>"><i class="bi bi-filetype-csv me-1"></i>Export CSV</a>
            <?php if ($editable): ?><button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#pgb-row" data-pgb-mode="insert"><i class="bi bi-plus-lg me-1"></i>Add row</button><?php endif; ?>
        </div>

        <?php if (empty($result['editable'])): ?>
            <div class="alert alert-secondary small py-2"><i class="bi bi-lock me-1"></i><?= e((string) ($result['reason'] ?? '')) ?></div>
        <?php endif; ?>

        <form class="row g-2 mb-2" method="get" action="<?= e($base) ?>">
            <input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="schema" value="<?= e($schema) ?>"><input type="hidden" name="table" value="<?= e($table) ?>">
            <?php if ($sort !== ''): ?><input type="hidden" name="sort" value="<?= e($sort) ?>"><input type="hidden" name="dir" value="<?= e($dir) ?>"><?php endif; ?>
            <div class="col-md-4"><input class="form-control form-control-sm" type="search" name="q" value="<?= e((string) $q) ?>" placeholder="Search all columns" aria-label="Search all columns" maxlength="200"></div>
            <div class="col-md-3"><select class="form-select form-select-sm" name="fc" aria-label="Filter column"><option value="">Filter column…</option><?php foreach ($cols as $c): ?><option value="<?= e((string) $c['name']) ?>"<?= $fc === $c['name'] ? ' selected' : '' ?>><?= e((string) $c['name']) ?></option><?php endforeach; ?></select></div>
            <div class="col-md-2"><select class="form-select form-select-sm" name="fo" aria-label="Filter"><?php foreach (['eq' => 'equals', 'ne' => 'is not', 'contains' => 'contains', 'null' => 'is NULL', 'notnull' => 'is not NULL'] as $k => $l): ?><option value="<?= e($k) ?>"<?= $fo === $k ? ' selected' : '' ?>><?= e($l) ?></option><?php endforeach; ?></select></div>
            <div class="col-md-2"><input class="form-control form-control-sm" name="fv" value="<?= e($fv) ?>" placeholder="Value" aria-label="Filter value"></div>
            <div class="col-md-1 d-grid"><button class="btn btn-sm btn-outline-primary">Go</button></div>
        </form>

        <div class="card">
            <div class="nim-dtable table-responsive"><table class="table table-sm align-middle mb-0" id="pgb-grid">
                <thead><tr>
                    <?php if ($editable): ?><th></th><?php endif; ?>
                    <?php foreach ($cols as $i => $c): ?>
                        <th data-c="<?= (int) $i ?>" class="text-nowrap"><a class="text-decoration-none" href="<?= e($sortLink((string) $c['name'])) ?>"><?= e((string) $c['name']) ?><?= $sort === $c['name'] ? ($dir === 'desc' ? ' ↓' : ' ↑') : '' ?></a><?= !empty($c['key']) ? ' <i class="bi bi-key-fill text-warning" title="Key column" aria-hidden="true"></i>' : '' ?><div class="small fw-normal text-body-secondary"><?= e((string) $c['type']) ?></div></th>
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
                            <td class="text-nowrap">
                                <button class="btn btn-sm btn-outline-secondary py-0" type="button" title="Edit" data-bs-toggle="modal" data-bs-target="#pgb-row" data-pgb-mode="edit" data-key="<?= e((string) json_encode($key)) ?>" data-values="<?= e((string) json_encode($vals)) ?>" data-cut="<?= e((string) json_encode($cutNames)) ?>"><i class="bi bi-pencil"></i></button>
                                <form class="d-inline" method="post" action="<?= e($this->url('postgres.row_delete')) ?>" data-nim-confirm="Delete this row? This cannot be undone.">
                                    <?= $this->csrfField() ?>
                                    <input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="schema" value="<?= e($schema) ?>"><input type="hidden" name="table" value="<?= e($table) ?>"><input type="hidden" name="page" value="<?= $pageNo ?>">
                                    <?php foreach ($key as $kc => $kv): ?><input type="hidden" name="key[<?= e((string) $kc) ?>]" value="<?= e((string) $kv) ?>"><?php endforeach; ?>
                                    <button class="btn btn-sm btn-outline-danger py-0" title="Delete"><i class="bi bi-trash"></i></button>
                                </form>
                            </td>
                        <?php endif; ?>
                        <?php foreach ($cols as $ci => $c): $v = $row[$ci]; ?>
                            <td data-c="<?= (int) $ci ?>" class="small font-monospace" style="max-width:24rem">
                                <?php if ($v === null): ?><span class="badge text-bg-light border text-body-secondary">NULL</span>
                                <?php else: ?><span class="d-inline-block text-truncate align-bottom" style="max-width:24rem" title="<?= e(mb_substr((string) $v, 0, 300)) ?>"><?= e((string) $v) ?></span><?= isset($cut[$ri . ':' . $ci]) ? ' <span class="badge text-bg-warning" title="Only the first 2,048 characters are shown">cut</span>' : '' ?><?php endif; ?>
                            </td>
                        <?php endforeach; ?>
                    </tr>
                <?php endforeach; ?>
                <?php if ((array) $result['rows'] === []): ?><tr><td colspan="<?= count($cols) + ($editable ? 1 : 0) ?>" class="text-center small text-body-secondary py-4">No rows.</td></tr><?php endif; ?>
                </tbody>
            </table></div>
        </div>

        <div class="d-flex flex-wrap align-items-center gap-2 mt-2 small">
            <span class="text-body-secondary">Page <?= $pageNo ?> of <?= number_format($pages) ?></span>
            <span class="me-auto"></span>
            <div class="btn-group btn-group-sm">
                <a class="btn btn-outline-secondary<?= $pageNo <= 1 ? ' disabled' : '' ?>" href="<?= e($here(['page' => max(1, $pageNo - 1)])) ?>">Previous</a>
                <a class="btn btn-outline-secondary<?= $pageNo >= $pages ? ' disabled' : '' ?>" href="<?= e($here(['page' => $pageNo + 1])) ?>">Next</a>
            </div>
        </div>

        <?php if ($structure !== null): ?>
        <details class="card mt-3">
            <summary class="card-header small fw-medium" style="cursor:pointer">Structure</summary>
            <div class="nim-dtable table-responsive"><table class="table table-sm mb-0 small">
                <thead><tr><th>Column</th><th>Type</th><th>Null</th><th>Default</th><th>Key</th></tr></thead>
                <tbody>
                <?php foreach ((array) $structure['columns'] as $c): ?>
                    <tr><td class="font-monospace"><?= e((string) $c['name']) ?></td><td><?= e((string) $c['type']) ?></td><td><?= !empty($c['not_null']) ? 'NOT NULL' : 'null' ?></td><td class="font-monospace"><?= e((string) ($c['default'] ?? '')) ?></td><td><?= !empty($c['key']) ? 'yes' : '' ?></td></tr>
                <?php endforeach; ?>
                </tbody>
            </table></div>
            <?php if (!empty($structure['indexes'])): ?>
                <div class="card-body border-top small"><div class="fw-medium mb-1">Indexes</div>
                    <?php foreach ((array) $structure['indexes'] as $ix): ?><div class="font-monospace text-body-secondary"><?= e((string) $ix['definition']) ?></div><?php endforeach; ?>
                </div>
            <?php endif; ?>
        </details>
        <?php endif; ?>
    <?php endif; ?>
    </div>
</div>

<?php if ($editable): ?>
<div class="modal fade" id="pgb-row" data-columns="<?= e((string) json_encode($cols)) ?>" tabindex="-1" aria-labelledby="pgb-row-t" aria-hidden="true"><div class="modal-dialog modal-lg modal-dialog-scrollable"><form class="modal-content" method="post" action="<?= e($this->url('postgres.row_insert')) ?>" data-pgb-form data-insert="<?= e($this->url('postgres.row_insert')) ?>" data-update="<?= e($this->url('postgres.row_update')) ?>" data-nim-busy>
    <?= $this->csrfField() ?>
    <input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="schema" value="<?= e($schema) ?>"><input type="hidden" name="table" value="<?= e($table) ?>"><input type="hidden" name="page" value="<?= $pageNo ?>">
    <div class="modal-header"><h2 class="modal-title fs-5" id="pgb-row-t">Row</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div>
    <div class="modal-body" data-pgb-fields></div>
    <div class="modal-footer"><button type="button" class="btn btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-primary">Save</button></div>
</form></div></div>
<?php endif; ?>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/postgres-browser.js')) ?>" defer></script><?php $this->end('scripts'); ?>
