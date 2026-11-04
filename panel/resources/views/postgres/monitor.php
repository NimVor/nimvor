<?php
/**
 * PostgreSQL server health for administrators.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed>|null $m
 * @var \Throwable|null $error
 * @var list<array<string, mixed>> $servers
 * @var int $chosen
 */
?>
<?php $this->start(); ?>PostgreSQL health<?php $this->end('title'); ?>
<?php $this->start(); ?>Connections, sizes and slow statements<?php $this->end('subtitle'); ?>

<p class="mb-2 d-flex flex-wrap gap-3 align-items-center"><a href="<?= e($this->url('postgres')) ?>" class="small"><i class="bi bi-arrow-left"></i> PostgreSQL</a>
    <?php if (count($servers) > 1): foreach ($servers as $sv): ?>
        <a class="small<?= (int) $sv['id'] === $chosen ? ' fw-semibold text-decoration-none' : '' ?>" href="<?= e($this->url('postgres.monitor') . '?server=' . (int) $sv['id']) ?>"><?= e((string) $sv['name']) ?></a>
    <?php endforeach; endif; ?></p>

<?php if ($error !== null || $m === null): ?>
    <div class="alert alert-warning small">The server could not be read<?= $error !== null ? ' (' . e($error->getMessage()) . ')' : '' ?>.</div>
<?php else:
    $conn = (array) $m['connections'];
    $max = max(1, (int) $m['max_connections']);
    $up = (int) $m['uptime_seconds'];
    ?>
    <div class="row g-3 mb-3">
        <div class="col-6 col-md-3"><div class="card"><div class="card-body py-2"><div class="small text-body-secondary">Version</div><div class="fw-medium small text-break"><?= e(preg_replace('/\s+on\s.*$/', '', (string) $m['version'])) ?></div></div></div></div>
        <div class="col-6 col-md-3"><div class="card"><div class="card-body py-2"><div class="small text-body-secondary">Running for</div><div class="fw-medium"><?= $up >= 86400 ? floor($up / 86400) . ' d ' : '' ?><?= floor(($up % 86400) / 3600) ?> h <?= floor(($up % 3600) / 60) ?> min</div></div></div></div>
        <div class="col-6 col-md-3"><div class="card"><div class="card-body py-2"><div class="small text-body-secondary">Connections</div><div class="fw-medium"><?= (int) $conn['total'] ?> of <?= $max ?></div>
            <div class="progress nim-progress-thin"><div class="progress-bar" data-width="<?= min(100, (int) round(((int) $conn['total']) * 100 / $max)) ?>"></div></div></div></div></div>
        <div class="col-6 col-md-3"><div class="card"><div class="card-body py-2"><div class="small text-body-secondary">Waiting for a lock</div><div class="fw-medium<?= (int) $m['lock_waits'] > 0 ? ' text-warning' : '' ?>"><?= (int) $m['lock_waits'] ?></div></div></div></div>
    </div>

    <div class="card mb-3">
        <div class="card-header py-2 fw-medium">Databases</div>
        <div class="table-responsive"><table class="table table-sm align-middle mb-0 small">
            <thead><tr><th>Database</th><th class="text-end">Size</th><th class="text-end">Connections</th><th class="text-end">Commits</th><th class="text-end">Rollbacks</th><th class="text-end">Cache hits</th><th class="text-end">Deadlocks</th></tr></thead>
            <tbody>
            <?php foreach ((array) $m['databases'] as $d): ?>
                <tr>
                    <td class="font-monospace"><?= e((string) $d['name']) ?></td>
                    <td class="text-end"><?= e(format_bytes((int) $d['size_bytes'])) ?></td>
                    <td class="text-end"><?= (int) $d['connections'] ?></td>
                    <td class="text-end"><?= number_format((int) $d['commits']) ?></td>
                    <td class="text-end"><?= number_format((int) $d['rollbacks']) ?></td>
                    <td class="text-end"><?= number_format((float) $d['cache_hit_percent'], 1) ?>%</td>
                    <td class="text-end<?= (int) $d['deadlocks'] > 0 ? ' text-warning' : '' ?>"><?= (int) $d['deadlocks'] ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    </div>

    <div class="card">
        <div class="card-header py-2 fw-medium">Statements running for more than 30 seconds</div>
        <div class="table-responsive"><table class="table table-sm align-middle mb-0 small">
            <thead><tr><th>Database</th><th>User</th><th>Kind</th><th>State</th><th class="text-end">Seconds</th></tr></thead>
            <tbody>
            <?php if (!(array) $m['long_running']): ?><tr><td colspan="5" class="p-3 text-body-secondary">None.</td></tr><?php endif; ?>
            <?php foreach ((array) $m['long_running'] as $q): ?>
                <tr><td class="font-monospace"><?= e((string) $q['database']) ?></td><td class="font-monospace"><?= e((string) $q['user']) ?></td><td><?= e((string) $q['statement']) ?></td><td><?= e((string) $q['state']) ?></td><td class="text-end"><?= (int) $q['seconds'] ?></td></tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
        <div class="card-footer small text-body-secondary">Only the kind of statement is shown, never its text, because it can contain customers' data.</div>
    </div>
<?php endif; ?>
