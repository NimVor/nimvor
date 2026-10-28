<?php /** @var \NimVor\View\View $this */ ?>
<?php $this->start(); ?>Databases<?php $this->end('title'); ?>
<?php $this->start(); ?>MySQL / MariaDB<?php $this->end('subtitle'); ?>

<?= $this->partial('databases/_manage', get_defined_vars()) ?>

<?php if (!empty($unmanaged)): ?>
    <div class="nim-panel mt-3" id="other-databases" data-nim-dt>
        <div class="nim-actionbar">
            <div class="fw-semibold"><i class="bi bi-binoculars me-1"></i>Other databases on this server <span class="nim-muted fw-normal small">(not managed by NimVor)</span></div>
            <div class="nim-actionbar-end"><div class="nim-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search other databases" data-nim-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div>
        </div>
        <div class="nim-dtable table-responsive">
            <table class="table align-middle">
                <thead><tr><th>Database name</th><th class="text-end">Tables</th><th class="text-end">Size</th><th class="text-end">Operate</th></tr></thead>
                <tbody>
                <?php foreach ($unmanaged as $d): ?>
                    <tr data-nim-row>
                        <td class="fw-semibold"><?= e($d['name']) ?></td>
                        <td class="text-end"><?= (int) $d['tables'] ?></td>
                        <td class="text-end"><?= e(format_bytes((int) $d['size_bytes'])) ?></td>
                        <td class="text-end"><?php if ($this->can(\NimVor\Security\Permissions::DATABASES_MANAGE)): ?><span class="nim-ops"><a class="nim-op" href="<?= e($this->url('databases.browser.server') . '?name=' . rawurlencode((string) $d['name'])) ?>">Browse</a></span><?php endif; ?></td>
                    </tr>
                <?php endforeach; ?>
                <tr class="d-none" data-nim-dt-empty><td colspan="4" class="text-center py-4 nim-muted">No database matches.</td></tr>
                </tbody>
            </table>
        </div>
        <div class="d-flex flex-wrap align-items-center justify-content-between gap-2">
            <div class="small nim-muted pt-2">These are left untouched. Use Import to bring them under NimVor management.</div>
            <div class="nim-pager" data-nim-dt-pager></div>
        </div>
    </div>
<?php endif; ?>

<?php if (!empty($redis) && !empty($redis['installed'])): ?>
    <div class="nim-infobar mt-3 mb-0">
        <i class="bi bi-lightning-charge-fill nim-warn" aria-hidden="true"></i>
        <span>Redis <?= e($redis['version'] ?? '') ?> on <code><?= e($redis['bind'] ?? '127.0.0.1') ?>:<?= e($redis['port'] ?? '6379') ?></code> · password <?= !empty($redis['password_set']) ? 'set' : 'not set' ?></span>
        <?php if ($this->hasRoute('redis')): ?><a class="ms-auto" href="<?= e($this->url('redis')) ?>">Manage Redis</a><?php endif; ?>
    </div>
<?php endif; ?>
