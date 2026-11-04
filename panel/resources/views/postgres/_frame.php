<?php
/**
 * The frame every PostgreSQL database page shares, laid out like the MySQL database browser: the table list on the
 * left (with a filter), the tab strip (Browse, Structure, Insert, SQL) on top and the page's own content in the pane.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var string $schema     selected schema ('' when no table is selected)
 * @var string $table      selected table ('' when none)
 * @var array{schemas: list<string>, tables: list<array<string, mixed>>} $tables
 * @var string $tab        browse | structure | insert | sql
 * @var string $inner      the pane's HTML, already built by the page
 */
$id = (int) $db['id'];
$hasTable = $table !== '';
$sel = $hasTable ? ['schema' => $schema, 'table' => $table] : [];
$tabUrl = function (string $name) use ($id, $sel): string {
    if ($name === 'sql') {
        return $this->url('postgres.sql') . '?' . http_build_query(['db' => $id] + $sel);
    }
    return $this->url('postgres.browse') . '?' . http_build_query(['db' => $id] + $sel + ($name === 'browse' ? [] : ['tab' => $name]));
};
$tableUrl = function (string $sch, string $tbl) use ($id): string {
    return $this->url('postgres.browse') . '?' . http_build_query(['db' => $id, 'schema' => $sch, 'table' => $tbl]);
};
$tabs = [
    ['browse', 'bi-grid-3x3', 'Browse', true],
    ['structure', 'bi-diagram-3', 'Structure', true],
    ['insert', 'bi-plus-lg', 'Insert', true],
    ['sql', 'bi-terminal', 'SQL', false],
];
$list = (array) ($tables['tables'] ?? []);
$multi = count((array) ($tables['schemas'] ?? [])) > 1;
?>
<div class="nim-db" id="nim-db">
    <aside class="nim-db-side">
        <div class="p-2 border-bottom"><input class="form-control form-control-sm" type="search" placeholder="Filter tables" aria-label="Filter tables" data-pgf-filter></div>
        <div class="list-group list-group-flush" role="list" aria-label="Tables" data-pgf-tables>
            <?php $lastSchema = null; foreach ($list as $t): ?>
                <?php if ($multi && $t['schema'] !== $lastSchema): $lastSchema = $t['schema']; ?><div class="list-group-item bg-body-tertiary text-body-secondary small py-1" data-pgf-schema="<?= e((string) $t['schema']) ?>"><?= e((string) $t['schema']) ?></div><?php endif; ?>
                <a class="list-group-item list-group-item-action nim-db-table<?= $hasTable && $t['schema'] === $schema && $t['name'] === $table ? ' active' : '' ?>" role="listitem" data-pgf-name="<?= e(strtolower((string) $t['schema'] . '.' . (string) $t['name'])) ?>" href="<?= e($tableUrl((string) $t['schema'], (string) $t['name'])) ?>">
                    <i class="bi bi-<?= $t['kind'] === 'table' ? 'table' : 'eye' ?> me-2" aria-hidden="true"></i>
                    <span class="text-truncate flex-grow-1"><?= e((string) $t['name']) ?></span>
                    <span class="small text-body-secondary ms-2"><?= (int) $t['rows_estimate'] > 0 ? '~' . number_format((int) $t['rows_estimate']) : '' ?></span>
                </a>
            <?php endforeach; ?>
        </div>
        <div class="small text-body-secondary px-2 py-2" data-pgf-count><?= $list === [] ? 'No tables yet. Create them from your application, the SQL tab or a client such as psql.' : count($list) . ' table' . (count($list) === 1 ? '' : 's') ?></div>
    </aside>
    <section class="nim-db-main">
        <nav class="nim-db-tabs" aria-label="Views">
            <?php foreach ($tabs as [$key, $icon, $label, $needsTable]): $off = $needsTable && !$hasTable; ?>
                <a class="btn btn-sm <?= $tab === $key ? 'btn-primary' : 'btn-outline-secondary' ?><?= $off ? ' disabled' : '' ?>"<?= $off ? ' aria-disabled="true" tabindex="-1"' : ' href="' . e($tabUrl($key)) . '"' ?><?= $tab === $key ? ' aria-current="page"' : '' ?>><i class="bi <?= e($icon) ?>" aria-hidden="true"></i> <?= e($label) ?></a>
            <?php endforeach; ?>
            <span class="ms-auto small text-body-secondary font-monospace text-truncate"><?= $hasTable ? e($schema . '.' . $table) : '' ?></span>
        </nav>
        <div class="nim-db-pane" aria-live="polite">
            <?= $inner ?>
        </div>
    </section>
</div>
