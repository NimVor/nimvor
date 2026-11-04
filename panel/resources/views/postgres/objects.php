<?php
/**
 * Every table, view, materialized view and foreign table of a PostgreSQL database with its statistics: filter by
 * schema and type, search by name, sort, page. Figures are estimates from the catalogs (no table is scanned).
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var array{schemas: list<string>, tables: list<array<string, mixed>>} $tables
 * @var array<string, mixed>|null $objects
 * @var array<string, mixed> $f   schema, kind, search, sort, dir
 * @var int $per
 * @var int $pageNo
 * @var \NimVor\Support\UserFacingException|null $error
 * @var bool $canWrite
 */
$id = (int) $db['id'];
$o = (array) $objects;
$items = (array) ($o['items'] ?? []);
$total = (int) ($o['total'] ?? 0);
$kinds = (array) ($o['kinds'] ?? []);
$schemas = array_map('strval', (array) ($o['schemas'] ?? []));
$pages = max(1, (int) ceil($total / max(1, $per)));
$url = function (array $over = []) use ($id, $f, $per, $pageNo): string {
    $p = ['db' => $id, 'tab' => 'objects', 'schema' => $f['schema'], 'kind' => $f['kind'], 'q' => $f['search'], 'sort' => $f['sort'], 'dir' => $f['dir'], 'per' => $per, 'page' => $pageNo];
    $p = array_filter(array_merge($p, $over), static fn ($v): bool => $v !== '' && $v !== null);
    return $this->url('postgres.browse') . '?' . http_build_query($p);
};
$sortLink = function (string $col, string $label) use ($f, $url): string {
    $next = $f['sort'] === $col && $f['dir'] === 'asc' ? 'desc' : 'asc';
    $mark = $f['sort'] === $col ? ($f['dir'] === 'asc' ? ' <i class="bi bi-caret-up-fill" aria-hidden="true"></i>' : ' <i class="bi bi-caret-down-fill" aria-hidden="true"></i>') : '';
    return '<a class="text-reset text-decoration-none" href="' . e($url(['sort' => $col, 'dir' => $next, 'page' => 1])) . '">' . e($label) . $mark . '</a>';
};
$icon = ['table' => 'table', 'partitioned table' => 'diagram-3', 'view' => 'eye', 'materialized view' => 'eye-fill', 'foreign table' => 'cloud'];
$filtered = $f['schema'] !== '' || $f['kind'] !== '' || $f['search'] !== '';
ob_start();
if ($error !== null): ?>
    <div class="nim-db-empty"><i class="bi bi-exclamation-triangle" aria-hidden="true"></i><div><?= e($error->getMessage()) ?></div><?php if ($error->reason() !== ''): ?><div class="small text-body-secondary"><?= e($error->reason()) ?></div><?php endif; ?>
        <a class="btn btn-sm btn-outline-secondary mt-2" href="<?= e($url()) ?>">Try again</a></div>
<?php else: ?>
    <form class="d-flex flex-wrap gap-2 align-items-center p-2 border-bottom" method="get" action="<?= e($this->url('postgres.browse')) ?>" role="search">
        <input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="tab" value="objects"><input type="hidden" name="sort" value="<?= e((string) $f['sort']) ?>"><input type="hidden" name="dir" value="<?= e((string) $f['dir']) ?>">
        <input class="form-control form-control-sm w-auto" type="search" name="q" value="<?= e((string) $f['search']) ?>" placeholder="Search by name" aria-label="Search by name" maxlength="100">
        <select class="form-select form-select-sm w-auto" name="kind" aria-label="Type" data-nim-autosubmit>
            <option value="">All types (<?= number_format(array_sum(array_map('intval', $kinds))) ?>)</option>
            <?php foreach ($kinds as $k => $n): ?><option value="<?= e((string) $k) ?>"<?= $f['kind'] === $k ? ' selected' : '' ?>><?= e(ucfirst((string) $k)) ?> (<?= (int) $n ?>)</option><?php endforeach; ?>
        </select>
        <?php if (count($schemas) > 1): ?>
        <select class="form-select form-select-sm w-auto" name="schema" aria-label="Schema" data-nim-autosubmit>
            <option value="">All schemas</option>
            <?php foreach ($schemas as $s): ?><option value="<?= e($s) ?>"<?= $f['schema'] === $s ? ' selected' : '' ?>><?= e($s) ?></option><?php endforeach; ?>
        </select>
        <?php endif; ?>
        <button class="btn btn-sm btn-outline-secondary"><i class="bi bi-search" aria-hidden="true"></i> Search</button>
        <?php if ($filtered): ?><a class="btn btn-sm btn-link" href="<?= e($this->url('postgres.browse') . '?' . http_build_query(['db' => $id, 'tab' => 'objects'])) ?>">Clear</a><?php endif; ?>
        <a class="btn btn-sm btn-outline-secondary ms-auto" href="<?= e($url()) ?>" title="Reload the figures"><i class="bi bi-arrow-clockwise" aria-hidden="true"></i> Refresh</a>
    </form>
    <?php if ($canWrite): ?>
    <div class="border-bottom p-2"><button type="button" class="btn btn-sm btn-outline-primary" data-pgd-open="new_table"><i class="bi bi-plus-lg"></i> New table</button></div>
    <?php endif; ?>
    <?php if ($items === []): ?>
        <div class="nim-db-empty"><i class="bi bi-table" aria-hidden="true"></i><div><?= $filtered ? 'No object matches this filter.' : 'This database has no tables yet. Create them from your application, the SQL tab or a client such as psql.' ?></div></div>
    <?php else: ?>
    <form method="post" action="<?= e($this->url('postgres.objects_bulk')) ?>" data-pgo-form>
    <?= $this->csrfField() ?>
    <input type="hidden" name="db" value="<?= $id ?>">
    <div class="table-responsive">
        <table class="table table-sm table-hover align-middle mb-0">
            <thead><tr>
                <?php if ($canWrite): ?><th scope="col" class="nim-col-check"><input class="form-check-input" type="checkbox" aria-label="Select all on this page" data-pgo-all></th><?php endif; ?>
                <th scope="col"><?= $sortLink('name', 'Name') ?></th><th scope="col"><?= $sortLink('kind', 'Type') ?></th>
                <th scope="col" class="text-end"><?= $sortLink('rows', 'Rows (est.)') ?></th><th scope="col" class="text-end"><?= $sortLink('size', 'Size') ?></th>
                <th scope="col" class="text-end">Columns</th><th scope="col" class="text-end">Indexes</th><th scope="col" class="text-end">Constraints</th><th scope="col" class="text-end">Triggers</th><th scope="col">Last analyzed</th>
            </tr></thead>
            <tbody>
            <?php foreach ($items as $it): $it = (array) $it; $isTable = in_array($it['kind'], ['table', 'partitioned table', 'foreign table'], true) || $it['kind'] === 'materialized view'; ?>
                <tr>
                    <?php if ($canWrite): ?><td class="nim-col-check"><input class="form-check-input" type="checkbox" name="sel[]" value="<?= e((string) json_encode([(string) $it['schema'], (string) $it['name']])) ?>" aria-label="Select <?= e((string) $it['name']) ?>" data-pgo-row></td><?php endif; ?>
                    <td><i class="bi bi-<?= e($icon[(string) $it['kind']] ?? 'table') ?> me-1" aria-hidden="true"></i><a class="font-monospace" href="<?= e($this->url('postgres.browse') . '?' . http_build_query(['db' => $id, 'schema' => $it['schema'], 'table' => $it['name']])) ?>"><?= e(($it['schema'] !== 'public' ? $it['schema'] . '.' : '') . $it['name']) ?></a>
                        <?php if (($it['partition_of'] ?? '') !== ''): ?><span class="badge text-bg-light border ms-1">partition of <?= e((string) $it['partition_of']) ?></span><?php endif; ?>
                        <?php if (($it['comment'] ?? '') !== ''): ?><div class="small text-body-secondary text-truncate nim-ellipsis-cell"><?= e((string) $it['comment']) ?></div><?php endif; ?></td>
                    <td class="small"><?= e((string) $it['kind']) ?></td>
                    <td class="text-end"><?= $isTable ? ((bool) ($it['rows_from_statistics'] ?? false) ? '~' : '') . number_format((int) $it['rows_estimate']) : '' ?></td>
                    <td class="text-end"><?= $it['kind'] === 'view' ? '' : e(format_bytes((int) $it['total_bytes'])) ?></td>
                    <td class="text-end"><?= (int) $it['columns'] ?></td>
                    <td class="text-end"><?= (int) $it['indexes'] ?></td>
                    <td class="text-end"><?= (int) $it['constraints'] ?></td>
                    <td class="text-end"><?= (int) $it['triggers'] ?></td>
                    <td class="small text-body-secondary"><?= e((string) ($it['last_analyze'] ?? '') !== '' ? str_replace('T', ' ', rtrim((string) $it['last_analyze'], 'Z')) . ' UTC' : ($isTable ? 'never' : '')) ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <?php if ($canWrite): ?>
    <div class="d-flex flex-wrap align-items-center gap-2 p-2 border-top bg-body-tertiary small" data-pgo-bar>
        <span data-pgo-count>None selected</span>
        <select class="form-select form-select-sm w-auto" name="op" aria-label="Action for the selected objects" data-pgo-op>
            <option value="">Action…</option>
            <?php foreach (\NimVor\Services\PostgresService::BULK_OPS as $key => $label): ?><option value="<?= e($key) ?>"><?= e($label) ?></option><?php endforeach; ?>
        </select>
        <span class="d-none d-flex flex-wrap align-items-center gap-2" data-pgo-danger>
            <label class="form-check form-check-inline mb-0"><input class="form-check-input" type="checkbox" name="cascade" value="1"> also dependent objects (CASCADE)</label>
            <label class="form-check form-check-inline mb-0" data-pgo-restart><input class="form-check-input" type="checkbox" name="restart_identity" value="1"> reset counters</label>
            <input class="form-control form-control-sm w-auto" type="text" name="confirm" placeholder="Type <?= e((string) $db['name']) ?> to confirm" autocomplete="off" aria-label="Type the database name to confirm">
        </span>
        <button class="btn btn-sm btn-primary" data-pgo-go disabled>Apply to selected</button>
        <span class="text-body-secondary">Each object is done on its own; one that fails does not stop the others.</span>
    </div>
    <?php endif; ?>
    </form>
    <div class="d-flex flex-wrap align-items-center gap-2 p-2 border-top small">
        <span><?= number_format((($pageNo - 1) * $per) + 1) ?>–<?= number_format(min($total, $pageNo * $per)) ?> of <?= number_format($total) ?></span>
        <span class="ms-auto d-flex gap-1">
            <?php if ($pageNo > 1): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($url(['page' => $pageNo - 1])) ?>">Previous</a><?php endif; ?>
            <?php if ($pageNo < $pages): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($url(['page' => $pageNo + 1])) ?>">Next</a><?php endif; ?>
        </span>
    </div>
    <?php endif; ?>
<?php endif;
$inner = (string) ob_get_clean();
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres')) ?>"><i class="bi bi-arrow-left"></i> PostgreSQL</a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-table me-1"></i>Database <span class="font-monospace"><?= e((string) $db['name']) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.backups') . '?db=' . $id) ?>"><i class="bi bi-cloud-arrow-down"></i> Backups</a><?php $this->end('actions'); ?>

<?php if ($canWrite): ?><?= $this->partial('postgres/_dialogs_objects', ['db' => $db, 'schemas' => $schemas]) ?><?php endif; ?>
<?= $this->partial('postgres/_frame', ['db' => $db, 'schema' => '', 'table' => '', 'tables' => $tables, 'tab' => 'objects', 'inner' => $inner]) ?>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/postgres-frame.js')) ?>" defer></script><script src="<?= e($this->asset('assets/js/postgres-objects.js')) ?>" defer></script><?php if ($canWrite): ?><script src="<?= e($this->asset('assets/js/postgres-dialogs.js')) ?>" defer></script><?php endif; ?><?php $this->end('scripts'); ?>
