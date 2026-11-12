<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $managed */
use NimVor\Security\Permissions as P;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeInsights;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$site = $nt['site'];
$canCreate = $this->can(P::WEBSITES_MANAGE);
$groups = ThemeNav::grouped($nt['items']);
$attention = ThemeInsights::attention($this, $managed, $backups ?? []);
?>
<?php $this->start(); ?>Home<?php $this->end('title'); ?>
<?php $this->start(); ?><?= count($managed) ?> website<?= count($managed) === 1 ? '' : 's' ?><?php $this->end('subtitle'); ?>
<?php $this->start(); ?><?php if ($canCreate): ?><a class="btn btn-sm btn-primary" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Create site</a><?php endif; ?><?php $this->end('actions'); ?>

<?php if ($managed === []): ?>
    <div class="nt-box"><div class="nim-empty-state">
        <i class="bi bi-globe2" aria-hidden="true"></i>
        <div class="fw-medium">No websites yet</div>
        <div class="small"><?= $canCreate ? 'Create your first website to get started.' : 'Your hosting provider has not set up a website for you yet.' ?></div>
        <?php if ($canCreate): ?><a class="btn btn-primary mt-3" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Create site</a><?php endif; ?>
    </div></div>
<?php else: ?>
<?php if ($attention !== []): ?>
<div class="nco-strip" role="status"><i class="bi bi-exclamation-circle" aria-hidden="true"></i><span><?= count($attention) ?> item<?= count($attention) === 1 ? '' : 's' ?> to check:</span>
    <?php foreach (array_slice($attention, 0, 4) as $a): ?><a class="nco-strip-item is-<?= e($a['level']) ?>" href="<?= e($a['url']) ?>"><?= e($a['site']) ?> · <?= e($a['text']) ?></a><?php endforeach; ?>
</div>
<?php endif; ?>
<section class="nt-box mb-3" aria-labelledby="nco-sites">
    <h2 class="nt-box-head h6 mb-0" id="nco-sites"><i class="bi bi-globe2" aria-hidden="true"></i>Your websites</h2>
    <div class="table-responsive">
        <table class="table table-sm align-middle mb-0 nco-table">
            <thead><tr><th>Domain</th><th>Status</th><th>Type</th><th>SSL</th><th>User</th><th class="text-end">Tools</th></tr></thead>
            <tbody>
            <?php foreach ($managed as $m): $id = (int) $m['id']; ?>
                <tr<?= $site !== null && $id === (int) $site['id'] ? ' class="table-active"' : '' ?>>
                    <td><a class="fw-semibold" href="<?= e($this->url('sites.show', ['id' => $id])) ?>"><?= e($m['domain']) ?></a></td>
                    <td><?= $this->partial('themes/components/site_status', ['site' => $m]) ?></td>
                    <td><?= $m['kind'] === 'php' ? 'PHP ' . e($m['php_version']) : e(\NimVor\Services\WebsiteService::KINDS[$m['kind']] ?? $m['kind']) ?></td>
                    <td><?= $m['https'] ? '<span class="nt-chip is-ok"><i class="bi bi-lock-fill" aria-hidden="true"></i>On</span>' : '<span class="nt-chip is-warn"><i class="bi bi-unlock" aria-hidden="true"></i>Off</span>' ?></td>
                    <td class="text-body-secondary"><?= e($m['system_user']) ?></td>
                    <td class="text-end text-nowrap"><a class="btn btn-sm btn-outline-secondary py-0" href="<?= e($this->url('websites', [], ['site' => $id])) ?>">Show tools</a> <a class="btn btn-sm btn-primary py-0" href="<?= e($this->url('sites.show', ['id' => $id])) ?>">Manage</a></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</section>

<?php if ($site !== null): ?>
<div class="nco-tools-head">
    <h2 class="h6 mb-0">Tools of <?= e($site['domain']) ?></h2>
    <div class="nco-filter"><label class="visually-hidden" for="nco-filter">Filter tools</label><input class="form-control form-control-sm" id="nco-filter" type="search" placeholder="Filter tools" data-nt-filter="#nco-tools" autocomplete="off"></div>
</div>
<div class="nco-tools" id="nco-tools">
    <?php foreach ($groups as $cat => $g): ?>
        <section class="nt-box nco-box nco-cat-<?= e($cat) ?>" data-nt-group aria-labelledby="nco-g-<?= e($cat) ?>">
            <h3 class="nt-box-head h6 mb-0" id="nco-g-<?= e($cat) ?>"><i class="bi bi-<?= e($g['icon']) ?>" aria-hidden="true"></i><?= e($g['label']) ?><span class="nco-count"><?= count($g['items']) ?></span></h3>
            <ul class="nco-list">
                <?php foreach ($g['items'] as $i): ?><li><a class="nt-tile" href="<?= e($i['url']) ?>" data-nt-tile data-name="<?= e(strtolower($i['label'])) ?>"><i class="bi bi-<?= e($i['icon']) ?>" aria-hidden="true"></i><span><?= e($i['label']) ?></span></a></li><?php endforeach; ?>
            </ul>
        </section>
    <?php endforeach; ?>
    <p class="text-body-secondary small" data-nt-empty hidden>No tool matches.</p>
</div>
<?php endif; ?>
<?php endif; ?>
