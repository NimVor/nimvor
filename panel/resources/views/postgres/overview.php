<?php
/**
 * PostgreSQL database overview: what the database holds and how to connect to it. Read-only; every figure comes from
 * the server's catalogs through the agent (pg.db_overview).
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var array{schemas: list<string>, tables: list<array<string, mixed>>} $tables
 * @var array<string, mixed>|null $overview
 * @var \NimVor\Support\UserFacingException|null $error
 */
$id = (int) $db['id'];
$o = (array) $overview;
$conn = (array) ($o['connection'] ?? []);
$stat = static function (string $label, string $value, string $hint = ''): string {
    return '<div class="col-6 col-md-4 col-xl-3"><div class="border rounded p-3 h-100"><div class="small text-body-secondary">' . e($label) . '</div><div class="fs-5 fw-semibold">' . e($value) . '</div>' . ($hint !== '' ? '<div class="small text-body-secondary">' . e($hint) . '</div>' : '') . '</div></div>';
};
$objectsUrl = fn (array $extra = []): string => $this->url('postgres.browse') . '?' . http_build_query(['db' => $id, 'tab' => 'objects'] + $extra);
ob_start();
if ($error !== null): ?>
    <div class="nim-db-empty"><i class="bi bi-exclamation-triangle" aria-hidden="true"></i><div><?= e($error->getMessage()) ?></div><?php if ($error->reason() !== ''): ?><div class="small text-body-secondary"><?= e($error->reason()) ?></div><?php endif; ?>
        <a class="btn btn-sm btn-outline-secondary mt-2" href="">Try again</a></div>
<?php elseif ($overview !== null): ?>
    <div class="p-3">
        <div class="row g-3 mb-4">
            <?= $stat('Size on disk', format_bytes((int) ($o['size_bytes'] ?? 0)), 'indexes ' . format_bytes((int) ($o['index_bytes'] ?? 0))) ?>
            <?= $stat('Tables', number_format((int) ($o['tables'] ?? 0)), (int) ($o['partitioned_tables'] ?? 0) > 0 ? number_format((int) $o['partitioned_tables']) . ' partitioned' : '') ?>
            <?= $stat('Rows (estimate)', number_format((int) ($o['rows_estimate'] ?? 0)), 'from the planner statistics') ?>
            <?= $stat('Views', number_format((int) ($o['views'] ?? 0)), number_format((int) ($o['materialized_views'] ?? 0)) . ' materialized') ?>
            <?= $stat('Indexes', number_format((int) ($o['indexes'] ?? 0))) ?>
            <?= $stat('Sequences', number_format((int) ($o['sequences'] ?? 0))) ?>
            <?= $stat('Functions', number_format((int) ($o['functions'] ?? 0)), number_format((int) ($o['triggers'] ?? 0)) . ' triggers') ?>
            <?= $stat('Schemas', number_format((int) ($o['schemas'] ?? 0))) ?>
        </div>
        <div class="row g-4">
            <div class="col-lg-6">
                <h2 class="h6">Database</h2>
                <dl class="row small mb-0">
                    <dt class="col-sm-4">PostgreSQL</dt><dd class="col-sm-8"><?= e((string) ($o['server_version'] ?? '')) ?></dd>
                    <dt class="col-sm-4">Owner</dt><dd class="col-sm-8 font-monospace"><?= e((string) ($o['owner'] ?? '')) ?></dd>
                    <dt class="col-sm-4">Encoding</dt><dd class="col-sm-8"><?= e((string) ($o['encoding'] ?? '')) ?></dd>
                    <dt class="col-sm-4">Collation</dt><dd class="col-sm-8"><?= e((string) ($o['collation'] ?? '')) ?> / <?= e((string) ($o['ctype'] ?? '')) ?></dd>
                    <dt class="col-sm-4">Extensions</dt><dd class="col-sm-8"><?= e(implode(', ', array_map('strval', (array) ($o['extensions'] ?? [])))) ?></dd>
                </dl>
            </div>
            <div class="col-lg-6">
                <h2 class="h6">Connection</h2>
                <dl class="row small mb-0">
                    <dt class="col-sm-4">Host</dt><dd class="col-sm-8 font-monospace"><?= e((string) ($conn['host'] ?? '')) ?></dd>
                    <dt class="col-sm-4">Port</dt><dd class="col-sm-8 font-monospace"><?= (int) ($conn['port'] ?? 0) ?></dd>
                    <dt class="col-sm-4">Database</dt><dd class="col-sm-8 font-monospace"><?= e((string) ($conn['database'] ?? '')) ?></dd>
                    <dt class="col-sm-4">User</dt><dd class="col-sm-8 font-monospace"><?= e((string) ($conn['role'] ?? '')) ?> <span class="text-body-secondary">(its password is not shown here)</span></dd>
                    <dt class="col-sm-4">Encryption</dt><dd class="col-sm-8"><?= !empty($conn['remote']) ? 'TLS (' . e((string) ($conn['tls'] ?? 'require')) . ')' : 'local connection' ?></dd>
                </dl>
            </div>
        </div>
        <div class="mt-4"><a class="btn btn-sm btn-primary" href="<?= e($objectsUrl()) ?>"><i class="bi bi-list-ul" aria-hidden="true"></i> Browse all objects</a></div>
    </div>
<?php endif;
$inner = (string) ob_get_clean();
?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres')) ?>"><i class="bi bi-arrow-left"></i> PostgreSQL</a><?php $this->end('back'); ?>
<?php $this->start(); ?><i class="bi bi-table me-1"></i>Database <span class="font-monospace"><?= e((string) $db['name']) ?></span><?php $this->end('title'); ?>
<?php $this->start(); ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('postgres.backups') . '?db=' . $id) ?>"><i class="bi bi-cloud-arrow-down"></i> Backups</a><?php $this->end('actions'); ?>

<?= $this->partial('postgres/_frame', ['db' => $db, 'schema' => '', 'table' => '', 'tables' => $tables, 'tab' => 'overview', 'inner' => $inner]) ?>
<?php $this->start(); ?><script src="<?= e($this->asset('assets/js/postgres-frame.js')) ?>" defer></script><?php $this->end('scripts'); ?>
