<?php /** @var \WhaCP\View\View $this */ ?>
<?php $this->start(); ?>Databases<?php $this->end('title'); ?>
<?php $this->start(); ?>MySQL / MariaDB<?php $this->end('subtitle'); ?>

<?= $this->partial('databases/_manage', get_defined_vars()) ?>

<?php if (!empty($unmanaged)): ?>
    <div class="card mt-3">
        <div class="card-header"><i class="bi bi-binoculars me-1"></i>Other databases on this server (not managed by WhaCP)</div>
        <div class="table-responsive">
            <table class="table mb-0 wcp-table">
                <thead><tr><th>Name</th><th class="text-end">Tables</th><th class="text-end">Size</th><th></th></tr></thead>
                <tbody>
                <?php foreach ($unmanaged as $d): ?>
                    <tr><td><?= e($d['name']) ?></td><td class="text-end"><?= (int) $d['tables'] ?></td><td class="text-end"><?= e(format_bytes((int) $d['size_bytes'])) ?></td><td class="text-end"><?php if ($this->can(\WhaCP\Security\Permissions::DATABASES_MANAGE)): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('databases.browser.server') . '?name=' . rawurlencode((string) $d['name'])) ?>"><i class="bi bi-table"></i> Browse</a><?php endif; ?></td></tr>
                <?php endforeach; ?>
                </tbody>
            </table>
        </div>
        <div class="card-footer small text-body-secondary">These are left untouched. Use Import to bring them under WhaCP management.</div>
    </div>
<?php endif; ?>

<?php if (!empty($redis) && !empty($redis['installed'])): ?>
    <div class="card mt-3">
        <div class="card-header"><i class="bi bi-lightning-charge me-1"></i>Redis</div>
        <div class="card-body small">
            Redis <?= e($redis['version'] ?? '') ?> on <?= e($redis['bind'] ?? '127.0.0.1') ?>:<?= e($redis['port'] ?? '6379') ?>
            · password <?= !empty($redis['password_set']) ? 'set' : 'not set' ?>
            <?php if ($this->hasRoute('redis')): ?> · <a href="<?= e($this->url('redis')) ?>">Manage Redis</a><?php endif; ?>
        </div>
    </div>
<?php endif; ?>
