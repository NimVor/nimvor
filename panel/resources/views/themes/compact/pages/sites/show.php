<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
/** @var array<string, mixed> $summary */
/** @var string|null $serverIp */
/** @var int|null $dbBytes */
use NimVor\Security\Permissions as P;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

// Compact: a dense facts table for the website, then every tool in small, colour-coded boxes (filterable).
$id = (int) $site['id'];
$canManage = $this->can(P::WEBSITES_MANAGE);
$disk = $inspect !== null ? format_bytes((int) $inspect['disk_bytes']) . (empty($inspect['disk_complete']) ? '+' : '') : null;
$lastBackup = $summary['last_backup'];
$none = '<span class="text-body-secondary">—</span>';
$handler = match ((string) $site['kind']) {
    'php' => 'PHP-FPM · PHP ' . $site['php_version'],
    'proxy' => 'Reverse proxy',
    default => 'Static files',
};
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'overview']) ?>
<?php $nt = ThemeContext::build($this); $groups = ThemeNav::grouped($nt['items']); ?>
<?= $this->partial('sites/overview_notices', ['site' => $site, 'inspect' => $inspect, 'inspectError' => $inspectError, 'import' => $import ?? null]) ?>

<section class="nt-box mb-3" aria-labelledby="nco-info">
    <h2 class="nt-box-head h6 mb-0" id="nco-info"><i class="bi bi-info-circle" aria-hidden="true"></i>Website information</h2>
    <div class="table-responsive">
        <table class="table table-sm align-middle mb-0 nco-table nco-facts">
            <tbody>
                <tr><th scope="row">Domain</th><td><?= e($site['domain']) ?> <?= $this->partial('themes/components/site_status', ['site' => $site]) ?></td><th scope="row">Handler</th><td><?= e($handler) ?></td></tr>
                <tr><th scope="row">System user</th><td><code><?= e($site['system_user']) ?></code></td><th scope="row">HTTPS</th><td><?= $site['https'] ? '<span class="nt-chip is-ok"><i class="bi bi-lock-fill" aria-hidden="true"></i>On</span>' : '<span class="nt-chip is-warn"><i class="bi bi-unlock" aria-hidden="true"></i>Off</span>' ?></td></tr>
                <tr><th scope="row">Root directory</th><td class="text-break"><code><?= e($site['document_root']) ?></code></td><th scope="row">IPv4</th><td><?= $serverIp !== null ? e($serverIp) : $none ?></td></tr>
                <tr><th scope="row">Site size</th><td><?= $disk !== null ? e($disk) : $none ?></td><th scope="row">Database size</th><td><?= $dbBytes !== null ? e(format_bytes($dbBytes)) : $none ?> <span class="small text-body-secondary">(<?= (int) $summary['databases'] ?>)</span></td></tr>
                <tr><th scope="row">Last backup</th><td colspan="3"><?= $lastBackup !== null ? e(time_ago((string) $lastBackup['created_at'])) : '<span class="text-body-secondary">None yet</span>' ?></td></tr>
            </tbody>
        </table>
    </div>
    <?php if ($canManage && ($this->mayToggleSite() || $this->mayDeleteSite($site))): ?>
        <div class="d-flex flex-wrap gap-2 p-2">
            <?php if ($this->mayToggleSite()): ?>
                <form method="post" action="<?= e($this->url('sites.toggle', ['id' => $id])) ?>" data-nim-busy<?= $site['enabled'] ? ' data-nim-confirm="Disable ' . e($site['domain']) . '? Visitors will no longer reach it. Files, databases and settings are kept."' : '' ?>>
                    <?= $this->csrfField() ?><input type="hidden" name="return_to" value="overview">
                    <button class="btn btn-sm btn-outline-secondary py-0" type="submit"><i class="bi bi-power me-1" aria-hidden="true"></i><?= $site['enabled'] ? 'Disable' : 'Enable' ?></button>
                </form>
            <?php endif; ?>
            <?php if ($this->mayDeleteSite($site)): ?>
                <a class="btn btn-sm btn-outline-danger py-0" href="<?= e($this->url('sites.advanced', ['id' => $id])) ?>#delete-website"><i class="bi bi-trash me-1" aria-hidden="true"></i>Delete</a>
            <?php endif; ?>
        </div>
    <?php endif; ?>
</section>

<div class="nco-tools-head">
    <h2 class="h6 mb-0">Tools</h2>
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
