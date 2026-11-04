<?php
/**
 * The functions, procedures and aggregates of a PostgreSQL database: read their definition, drop them. Writing a
 * routine is code, so creating or changing one is done on the SQL tab.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var array{schemas: list<string>, tables: list<array<string, mixed>>} $tables
 * @var list<array<string, mixed>>|null $routines
 * @var string|null $definition
 * @var array<string, mixed> $f
 * @var bool $canWrite
 * @var \NimVor\Support\UserFacingException|null $error
 */
$id = (int) $db['id'];
$base = $this->url('postgres.browse');
ob_start();
if ($error !== null): ?>
    <div class="nim-db-empty"><i class="bi bi-exclamation-triangle" aria-hidden="true"></i><div><?= e($error->getMessage()) ?></div><a class="btn btn-sm btn-outline-secondary mt-2" href="">Try again</a></div>
<?php else: ?>
    <form class="d-flex flex-wrap gap-2 align-items-center p-2 border-bottom" method="get" action="<?= e($base) ?>" role="search">
        <input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="tab" value="routines">
        <input class="form-control form-control-sm w-auto" type="search" name="q" value="<?= e((string) $f['search']) ?>" placeholder="Search by name" aria-label="Search by name" maxlength="100">
        <button class="btn btn-sm btn-outline-secondary"><i class="bi bi-search" aria-hidden="true"></i> Search</button>
        <span class="small text-body-secondary ms-auto">To create or change a routine, use the SQL tab.</span>
    </form>
    <?php if ((array) $routines === []): ?>
        <div class="nim-db-empty"><i class="bi bi-braces" aria-hidden="true"></i><div>No functions or procedures<?= $f['search'] !== '' ? ' match this search' : ' in this database' ?>.</div></div>
    <?php else: ?>
    <div class="table-responsive"><table class="table table-sm table-hover align-middle mb-0">
        <thead><tr><th>Name</th><th>Kind</th><th>Arguments</th><th>Returns</th><th>Language</th><th>Properties</th><th></th></tr></thead>
        <tbody>
        <?php foreach ((array) $routines as $r): $label = $r['schema'] . '.' . $r['name'] . '(' . $r['arguments'] . ')'; ?>
            <tr>
                <td class="font-monospace"><?= e(($r['schema'] !== 'public' ? $r['schema'] . '.' : '') . $r['name']) ?><?php if (($r['comment'] ?? '') !== ''): ?><div class="small text-body-secondary"><?= e((string) $r['comment']) ?></div><?php endif; ?></td>
                <td class="small"><?= e((string) $r['kind']) ?><?= !empty($r['trigger_function']) ? ' <span class="badge text-bg-info">trigger</span>' : '' ?></td>
                <td class="font-monospace small"><?= e((string) $r['arguments']) ?></td><td class="font-monospace small"><?= e((string) $r['returns']) ?></td><td><?= e((string) $r['language']) ?></td>
                <td class="small"><?= e((string) $r['volatility']) ?><?= !empty($r['security_definer']) ? ' · security definer' : '' ?> · owner <?= e((string) $r['owner']) ?></td>
                <td class="text-end text-nowrap">
                    <a class="btn btn-sm btn-outline-secondary" href="<?= e($base . '?' . http_build_query(['db' => $id, 'tab' => 'routines', 'def' => (int) $r['oid']])) ?>">Definition</a>
                    <?php if ($canWrite): ?>
                    <button type="button" class="btn btn-sm btn-outline-danger" data-pgd-open="routine_drop" data-pgd-set="<?= e((string) json_encode(['oid' => (int) $r['oid'], 'label' => $label, 'word' => (string) $db['name']])) ?>">Drop</button>
                    <?php endif; ?>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table></div>
    <?php endif; ?>
    <?php if ($definition !== null): ?><div class="p-2 border-top"><h2 class="h6">Definition</h2><pre class="nim-db-create mb-0"><?= e($definition) ?></pre></div><?php endif; ?>
<?php endif;
$inner = (string) ob_get_clean();
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres')) ?>"><i class="bi bi-arrow-left"></i> PostgreSQL</a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-table me-1"></i>Database <span class="font-monospace"><?= e((string) $db['name']) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.backups') . '?db=' . $id) ?>"><i class="bi bi-cloud-arrow-down"></i> Backups</a><?php $this->end('actions'); ?>

<?php if ($canWrite): ?><?= $this->partial('postgres/_dialogs_routines', ['db' => $db]) ?><?php endif; ?>
<?= $this->partial('postgres/_frame', ['db' => $db, 'schema' => '', 'table' => '', 'tables' => $tables, 'tab' => 'routines', 'inner' => $inner]) ?>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/postgres-frame.js')) ?>" defer></script><?php if ($canWrite): ?><script src="<?= e($this->asset('assets/js/postgres-dialogs.js')) ?>" defer></script><?php endif; ?><?php $this->end('scripts'); ?>
