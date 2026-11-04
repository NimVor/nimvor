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
    <details class="border-bottom p-2"><summary class="btn btn-sm btn-outline-primary"><i class="bi bi-plus-lg"></i> New sequence</summary>
        <form class="row g-1 mt-1" method="post" action="<?= e($this->url('postgres.sequence_alter')) ?>"><?= $this->csrfField() ?><?= $hid ?><input type="hidden" name="op" value="create">
            <div class="col-6 col-md-2"><input class="form-control form-control-sm" name="schema" value="public" maxlength="63" aria-label="Schema"></div>
            <div class="col-6 col-md-3"><input class="form-control form-control-sm" name="name" placeholder="name" maxlength="63" required aria-label="Name"></div>
            <div class="col-4 col-md-2"><input class="form-control form-control-sm" name="start" placeholder="start" aria-label="Start"></div>
            <div class="col-4 col-md-2"><input class="form-control form-control-sm" name="increment" placeholder="step" aria-label="Increment"></div>
            <div class="col-4 col-md-2"><button class="btn btn-sm btn-primary w-100">Create</button></div>
        </form>
    </details>
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
                <?php if ($canWrite): ?><td class="text-end text-nowrap">
                    <details class="d-inline-block"><summary class="btn btn-sm btn-outline-secondary">Change</summary>
                        <div class="border rounded p-2 mt-1 text-start bg-body">
                            <form class="d-flex gap-1 mb-1" method="post" action="<?= e($this->url('postgres.sequence_alter')) ?>"><?= $this->csrfField() ?><?= $fh ?><input type="hidden" name="op" value="set_value">
                                <input class="form-control form-control-sm" name="value" placeholder="last value used" aria-label="Last value used by <?= e($n) ?>"><button class="btn btn-sm btn-outline-primary text-nowrap">Set value</button></form>
                            <form class="d-flex gap-1 mb-1" method="post" action="<?= e($this->url('postgres.sequence_alter')) ?>"><?= $this->csrfField() ?><?= $fh ?><input type="hidden" name="op" value="restart">
                                <input class="form-control form-control-sm" name="value" placeholder="next value to return" aria-label="Next value of <?= e($n) ?>"><button class="btn btn-sm btn-outline-primary text-nowrap">Restart at</button></form>
                            <?php if ($own !== ''): ?><form class="mb-1" method="post" action="<?= e($this->url('postgres.sequence_alter')) ?>"><?= $this->csrfField() ?><?= $fh ?><input type="hidden" name="op" value="sync"><button class="btn btn-sm btn-outline-secondary" title="Next value becomes the highest value in <?= e($own) ?> plus one">Follow <?= e($own) ?> again</button></form><?php endif; ?>
                            <form class="row g-1 mb-1" method="post" action="<?= e($this->url('postgres.sequence_alter')) ?>"><?= $this->csrfField() ?><?= $fh ?><input type="hidden" name="op" value="alter">
                                <div class="col-4"><input class="form-control form-control-sm" name="increment" placeholder="step" aria-label="Step"></div><div class="col-4"><input class="form-control form-control-sm" name="min" placeholder="min" aria-label="Minimum"></div><div class="col-4"><input class="form-control form-control-sm" name="max" placeholder="max" aria-label="Maximum"></div>
                                <div class="col-4"><input class="form-control form-control-sm" name="cache" placeholder="cache" aria-label="Cache"></div>
                                <div class="col-4"><select class="form-select form-select-sm" name="cycle" aria-label="Cycle"><option value="">cycle: keep</option><option value="yes">cycle</option><option value="no">no cycle</option></select></div>
                                <div class="col-4"><button class="btn btn-sm btn-outline-primary w-100">Apply</button></div>
                            </form>
                            <?php if ($own === ''): ?><form class="d-flex gap-1" method="post" action="<?= e($this->url('postgres.sequence_alter')) ?>"><?= $this->csrfField() ?><?= $fh ?><input type="hidden" name="op" value="drop">
                                <input class="form-control form-control-sm" name="confirm" placeholder="type <?= e($n) ?> to drop" autocomplete="off" aria-label="Type <?= e($n) ?> to confirm"><button class="btn btn-sm btn-danger">Drop</button></form><?php endif; ?>
                        </div>
                    </details>
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

<?= $this->partial('postgres/_frame', ['db' => $db, 'schema' => '', 'table' => '', 'tables' => $tables, 'tab' => 'sequences', 'inner' => $inner]) ?>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/postgres-frame.js')) ?>" defer></script><?php $this->end('scripts'); ?>
