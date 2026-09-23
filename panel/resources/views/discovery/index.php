<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $inventory */
$sites = $inventory['websites']['websites'] ?? [];
$cards = [
    ['Websites', 'globe2', count($sites), 'websites', \WhaCP\Security\Permissions::WEBSITES_VIEW],
    ['PHP versions', 'filetype-php', count($inventory['php']['versions'] ?? []), 'php', \WhaCP\Security\Permissions::PHP_VIEW],
    ['PHP-FPM pools', 'diagram-2', count($inventory['php']['pools'] ?? []), 'php', \WhaCP\Security\Permissions::PHP_VIEW],
    ['Databases', 'database', count($inventory['databases']['schemas'] ?? []), 'databases', \WhaCP\Security\Permissions::DATABASES_VIEW],
    ["Let's Encrypt certificates", 'shield-lock', count($inventory['certificates'] ?? []), 'ssl', \WhaCP\Security\Permissions::SSL_VIEW],
    ['Cron jobs', 'clock-history', count($inventory['cron']['jobs'] ?? []), 'cron', \WhaCP\Security\Permissions::CRON_VIEW],
    ['Services', 'cpu', count($inventory['services'] ?? []), 'services', \WhaCP\Security\Permissions::SERVICES_VIEW],
    ['System accounts', 'people', count(array_filter($inventory['users'] ?? [], static fn ($u) => empty($u['system']))), 'system_users', \WhaCP\Security\Permissions::SYSTEM_USERS_VIEW],
];
$warnings = array_merge($inventory['websites']['warnings'] ?? [], $inventory['php']['warnings'] ?? [], $inventory['databases']['warnings'] ?? [], $inventory['redis']['warnings'] ?? [], $inventory['cron']['warnings'] ?? []);
$os = $inventory['os'] ?? [];
?>
<?php $this->start(); ?>Discovery<?php $this->end('title'); ?>
<?php $this->start(); ?>Read-only inventory of the existing server<?php $this->end('subtitle'); ?>

<?= $this->partial('partials/discovery_bar', ['discoveredAt' => $discoveredAt]) ?>

<div class="alert alert-info small">
    <i class="bi bi-shield-check me-1"></i>
    Discovery only <strong>reads</strong> configuration files and service state. It never writes, reloads, migrates or deletes anything.
    The agent itself runs with a read-only view of the filesystem enforced by systemd.
</div>

<?php if (!empty($os) && empty($os['supported'])): ?>
    <div class="alert alert-warning small"><i class="bi bi-exclamation-triangle me-1"></i>
        <?= e($os['pretty_name'] ?? 'This OS') ?> is not an officially supported release. Discovery runs in best-effort mode.
    </div>
<?php endif; ?>

<div class="row g-3 mb-3">
    <?php foreach ($cards as [$label, $icon, $count, $route, $perm]): ?>
        <div class="col-6 col-md-4 col-xl-3">
            <?php if ($this->can($perm)): ?><a class="text-decoration-none" href="<?= e($this->url($route)) ?>"><?php endif; ?>
            <div class="card wcp-count-card h-100">
                <div class="card-body">
                    <i class="bi bi-<?= e($icon) ?>"></i>
                    <div class="wcp-kpi"><?= (int) $count ?></div>
                    <div class="wcp-kpi-label"><?= e($label) ?></div>
                </div>
            </div>
            <?php if ($this->can($perm)): ?></a><?php endif; ?>
        </div>
    <?php endforeach; ?>
</div>

<div class="row g-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-exclamation-diamond me-1"></i>Discovery notes</div>
            <ul class="list-group list-group-flush small">
                <?php if ($warnings === []): ?>
                    <li class="list-group-item text-body-secondary">No problems were encountered while reading the configuration.</li>
                <?php endif; ?>
                <?php foreach ($warnings as $w): ?>
                    <li class="list-group-item"><?= e($w) ?></li>
                <?php endforeach; ?>
            </ul>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-clock me-1"></i>Snapshot history</div>
            <div class="table-responsive">
                <table class="table table-sm mb-0 wcp-table">
                    <thead><tr><th>When</th><th>Websites</th><th>By</th><th>Checksum</th></tr></thead>
                    <tbody>
                    <?php foreach ($history as $h): ?>
                        <tr>
                            <td><?= e(gmdate('Y-m-d H:i', (int) strtotime((string) $h['created_at']))) ?></td>
                            <td><?= (int) $h['website_count'] ?></td>
                            <td><?= e($h['username'] ?? 'automatic') ?></td>
                            <td><code class="small"><?= e(substr((string) $h['checksum'], 0, 12)) ?></code></td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table>
            </div>
            <div class="card-footer small text-body-secondary">A changed checksum means the server configuration changed between snapshots.</div>
        </div>
    </div>
</div>
