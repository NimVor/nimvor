<?php
/**
 * The sequences of a PostgreSQL database: their settings and last value, and the changes that are safe to make from
 * here (set the value, follow the column again, change the settings, create, drop).
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var array{schemas: list<string>, tables: list<array<string, mixed>>} $tables
 * @var list<array<string, mixed>>|null $sequences
 * @var bool $canWrite
 * @var \NimVor\Support\UserFacingException|null $error
 */
$id = (int) $db['id'];
$hid = '<input type="hidden" name="db" value="' . $id . '">';
$num = static fn ($v): string => number_format((int) $v);
ob_start();
if ($error !== null): ?>
    <div class="nim-db-empty"><i class="bi bi-exclamation-triangle" aria-hidden="true"></i><div><?= e($error->getMessage()) ?></div><a class="btn btn-sm btn-outline-secondary mt-2" href="">Try again</a></div>
<?php else: ?>
    <?php if ($canWrite): ?>
    <div class="border-bottom p-2"><button type="button" class="btn btn-sm btn-outline-primary" data-pgd-open="seq_create"><i class="bi bi-plus-lg"></i> New sequence</button></div>
    <?php endif; ?>
    <?php if ((array) $sequences === []): ?>
        <div class="nim-db-empty"><i class="bi bi-123" aria-hidden="true"></i><div>This database has no sequences. Serial and identity columns create them automatically.</div></div>
    <?php else: ?>
    <div class="table-responsive"><table class="table table-sm table-hover align-middle mb-0">
        <thead><tr><th>Name</th><th>Type</th><th class="text-end">Last value</th><th class="text-end">Step</th><th class="text-end">Min</th><th class="text-end">Max</th><th>Cycle</th><th>Used by</th><?php if ($canWrite): ?><th></th><?php endif; ?></tr></thead>
        <tbody>
        <?php foreach ((array) $sequences as $q): $n = (string) $q['name']; $sch = (string) $q['schema']; $own = (string) $q['owned_by']; $fh = $hid . '<input type="hidden" name="schema" value="' . e($sch) . '"><input type="hidden" name="name" value="' . e($n) . '">'; ?>
            <tr>
                <td class="font-monospace"><?= e(($sch !== 'public' ? $sch . '.' : '') . $n) ?></td><td><?= e((string) $q['type']) ?></td>
                <td class="text-end"><?= isset($q['last_value']) ? $num($q['last_value']) : '<span class="text-body-secondary">not used yet</span>' ?></td>
                <td class="text-end"><?= $num($q['increment']) ?></td><td class="text-end"><?= $num($q['min']) ?></td><td class="text-end"><?= $num($q['max']) ?></td><td><?= !empty($q['cycle']) ? 'yes' : 'no' ?></td>
                <td class="font-monospace small"><?= $own !== '' ? e($own) : '<span class="text-body-secondary">free-standing</span>' ?></td>
                <?php if ($canWrite): $set = (string) json_encode(['schema' => $sch, 'name' => $n, 'owned_by' => $own]); ?><td class="text-end text-nowrap">
                    <div class="dropdown d-inline-block">
                        <button class="btn btn-sm btn-outline-secondary dropdown-toggle" type="button" data-bs-toggle="dropdown" aria-expanded="false">Change</button>
                        <ul class="dropdown-menu dropdown-menu-end">
                            <li><button class="dropdown-item" type="button" data-pgd-open="seq_set" data-pgd-set="<?= e($set) ?>">Set the value…</button></li>
                            <li><button class="dropdown-item" type="button" data-pgd-open="seq_restart" data-pgd-set="<?= e($set) ?>">Restart at…</button></li>
                            <li><button class="dropdown-item" type="button" data-pgd-open="seq_alter" data-pgd-set="<?= e($set) ?>">Change settings…</button></li>
                            <?php if ($own !== ''): ?><li><button class="dropdown-item" type="button" data-pgd-open="seq_sync" data-pgd-set="<?= e($set) ?>">Follow <?= e($own) ?> again…</button></li><?php endif; ?>
                            <?php if ($own === ''): ?><li><hr class="dropdown-divider"></li><li><button class="dropdown-item text-danger" type="button" data-pgd-open="seq_drop" data-pgd-set="<?= e($set) ?>">Drop…</button></li><?php endif; ?>
                        </ul>
                    </div>
                </td><?php endif; ?>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table></div>
    <?php endif; ?>
<?php endif;
$inner = (string) ob_get_clean();
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres')) ?>"><i class="bi bi-arrow-left"></i> PostgreSQL</a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-table me-1"></i>Database <span class="font-monospace"><?= e((string) $db['name']) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.backups') . '?db=' . $id) ?>"><i class="bi bi-cloud-arrow-down"></i> Backups</a><?php $this->end('actions'); ?>

<?php if ($canWrite): ?><?= $this->partial('postgres/_dialogs_sequences', ['db' => $db]) ?><?php endif; ?>
<?= $this->partial('postgres/_frame', ['db' => $db, 'schema' => '', 'table' => '', 'tables' => $tables, 'tab' => 'sequences', 'inner' => $inner]) ?>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/postgres-frame.js')) ?>" defer></script><?php if ($canWrite): ?><script src="<?= e($this->asset('assets/js/postgres-dialogs.js')) ?>" defer></script><?php endif; ?><?php $this->end('scripts'); ?>
