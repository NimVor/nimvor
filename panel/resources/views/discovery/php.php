<?php
/** @var \WhaCP\View\View $this */
$versions = $php['versions'] ?? [];
$pools = $php['pools'] ?? [];
?>
<?php $this->start(); ?>PHP<?php $this->end('title'); ?>
<?php $this->start(); ?>Installed versions and PHP-FPM pools<?php $this->end('subtitle'); ?>

<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<div class="row g-3 mb-3">
    <?php if ($versions === []): ?>
        <div class="col-12"><div class="card wcp-empty"><div class="card-body text-center py-5 text-body-secondary">
            <i class="bi bi-filetype-php display-6"></i><p class="mt-3 mb-0">No PHP installation was detected.</p>
        </div></div></div>
    <?php endif; ?>
    <?php foreach ($versions as $v): ?>
        <div class="col-sm-6 col-xl-3">
            <div class="card h-100">
                <div class="card-body">
                    <div class="d-flex justify-content-between align-items-center mb-2">
                        <span class="h4 mb-0">PHP <?= e($v['version']) ?></span>
                        <?php if (!empty($v['is_default'])): ?><span class="badge text-bg-primary">CLI default</span><?php endif; ?>
                    </div>
                    <dl class="wcp-dl wcp-dl-compact small mb-0">
                        <dt>CLI</dt><dd><?= !empty($v['cli_binary']) ? '<code>' . e($v['cli_binary']) . '</code>' : '—' ?></dd>
                        <dt>FPM</dt><dd><?= !empty($v['fpm_binary']) ? '<code>' . e($v['fpm_binary']) . '</code>' : '<span class="text-body-secondary">not installed</span>' ?></dd>
                        <dt>Pools</dt><dd><?= (int) $v['pool_count'] ?></dd>
                        <dt>Websites</dt><dd><?= count($usage[$v['version']] ?? []) ?></dd>
                    </dl>
                </div>
            </div>
        </div>
    <?php endforeach; ?>
</div>

<div class="card">
    <div class="card-header"><i class="bi bi-diagram-2 me-1"></i>PHP-FPM pools</div>
    <?php if ($pools === []): ?>
        <div class="card-body text-body-secondary small">No PHP-FPM pools were found.</div>
    <?php else: ?>
        <div class="table-responsive">
            <table class="table table-hover align-middle mb-0 wcp-table">
                <thead><tr><th>Pool</th><th>PHP</th><th>User</th><th>Listen</th><th>Process manager</th><th class="d-none d-lg-table-cell">open_basedir</th><th class="d-none d-xl-table-cell">File</th></tr></thead>
                <tbody>
                <?php foreach ($pools as $p): ?>
                    <tr>
                        <td class="fw-semibold"><?= e($p['name']) ?></td>
                        <td><?= e($p['version'] !== '' ? $p['version'] : '?') ?></td>
                        <td><?= e(($p['user'] ?? '') . (($p['group'] ?? '') !== '' ? ':' . $p['group'] : '')) ?></td>
                        <td><code class="small"><?= e($p['listen']) ?></code></td>
                        <td><?= e($p['pm'] ?? '') ?><?php if (!empty($p['max_children'])): ?> <span class="text-body-secondary small">(max <?= e($p['max_children']) ?>)</span><?php endif; ?></td>
                        <td class="d-none d-lg-table-cell small"><?= !empty($p['open_basedir']) ? '<code>' . e($p['open_basedir']) . '</code>' : '<span class="text-body-secondary">not set</span>' ?></td>
                        <td class="d-none d-xl-table-cell small"><code><?= e($p['file']) ?></code></td>
                    </tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
    <?php endif; ?>
</div>
