<?php
/** @var \NimVor\View\View $this */
$schemas = $db['schemas'] ?? [];
?>
<?php $this->start(); ?>Databases<?php $this->end('title'); ?>
<?php $this->start(); ?>MySQL / MariaDB and Redis<?php $this->end('subtitle'); ?>

<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<div class="row g-3">
    <div class="col-lg-4">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-database me-1"></i>Database server</div>
            <div class="card-body">
                <?php if (empty($db['installed'])): ?>
                    <p class="text-body-secondary mb-0">MySQL or MariaDB was not detected.</p>
                <?php else: ?>
                    <dl class="nim-dl">
                        <dt>Engine</dt><dd><?= e($db['engine'] === 'mariadb' ? 'MariaDB' : 'MySQL') ?> <?= e($db['version'] ?? '') ?></dd>
                        <dt>Listen</dt><dd><?= e(($db['bind_address'] ?? '') !== '' ? $db['bind_address'] : 'default') ?>:<?= (int) ($db['port'] ?? 3306) ?></dd>
                        <?php if (!empty($db['socket'])): ?><dt>Socket</dt><dd><code class="small"><?= e($db['socket']) ?></code></dd><?php endif; ?>
                        <dt>Data dir</dt><dd><code class="small"><?= e($db['data_dir'] ?? '') ?></code></dd>
                    </dl>
                <?php endif; ?>
                <?php foreach ($db['warnings'] ?? [] as $w): ?>
                    <div class="alert alert-warning small py-2 mt-2 mb-0"><?= e($w) ?></div>
                <?php endforeach; ?>
            </div>
        </div>
        <div class="card">
            <div class="card-header"><i class="bi bi-lightning-charge me-1"></i>Redis</div>
            <div class="card-body">
                <?php if (empty($redis['installed'])): ?>
                    <p class="text-body-secondary mb-0">Redis was not detected.</p>
                <?php else: ?>
                    <dl class="nim-dl">
                        <dt>Version</dt><dd><?= e(($redis['version'] ?? '') !== '' ? $redis['version'] : 'unknown') ?></dd>
                        <dt>Bind</dt><dd><?= e($redis['bind'] ?? 'default') ?><?= !empty($redis['port']) ? ' : ' . e($redis['port']) : '' ?></dd>
                        <?php if (!empty($redis['unix_socket'])): ?><dt>Socket</dt><dd><code class="small"><?= e($redis['unix_socket']) ?></code></dd><?php endif; ?>
                        <dt>Max memory</dt><dd><?= e(($redis['maxmemory'] ?? '') !== '' ? $redis['maxmemory'] . ' (' . ($redis['maxmemory_policy'] ?? 'default') . ')' : 'unlimited') ?></dd>
                        <dt>Password</dt><dd><?= !empty($redis['password_set']) ? $this->statusBadge('yes', 'set') : $this->statusBadge('warning', 'not set') ?></dd>
                        <dt>Config</dt><dd><code class="small"><?= e($redis['config_file'] ?? '') ?></code></dd>
                    </dl>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-lg-8">
        <div class="card">
            <div class="card-header"><i class="bi bi-table me-1"></i>Databases (<?= count($schemas) ?>)</div>
            <?php if ($schemas === []): ?>
                <div class="card-body text-body-secondary small">No user databases were found in the data directory.</div>
            <?php else: ?>
                <div class="table-responsive">
                    <table class="table table-hover mb-0 nim-table">
                        <thead><tr><th>Name</th><th class="text-end">Tables</th><th class="text-end">Size on disk</th></tr></thead>
                        <tbody>
                        <?php foreach ($schemas as $s): ?>
                            <tr>
                                <td class="fw-semibold"><?= e($s['name']) ?></td>
                                <td class="text-end"><?= (int) $s['tables'] ?></td>
                                <td class="text-end"><?= e(format_bytes((int) $s['size_bytes'])) ?><?= empty($s['size_complete']) ? '+' : '' ?></td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
                <div class="card-footer small text-body-secondary">Listed from the data directory without connecting to the server; no credentials are used or stored.</div>
            <?php endif; ?>
        </div>
    </div>
</div>
