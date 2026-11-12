<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
/** @var array<string, mixed> $summary */
/** @var string|null $serverIp */
/** @var int|null $dbBytes */
use NimVor\Security\Permissions as P;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

// The Classic version of a website's overview: its details in a "General information" panel, then every tool of the
// website in collapsible, colour-coded groups (the same arrangement as the home page).
$id = (int) $site['id'];
$canManage = $this->can(P::WEBSITES_MANAGE);
$disk = $inspect !== null ? format_bytes((int) $inspect['disk_bytes']) . (empty($inspect['disk_complete']) ? '+' : '') : null;
$sslLabel = match ((string) $site['ssl_mode']) {
    'letsencrypt' => "Let's Encrypt",
    'custom' => 'Own certificate',
    default => $site['https'] ? 'Installed' : 'Not installed',
};
$handler = match ((string) $site['kind']) {
    'php' => 'PHP-FPM',
    'proxy' => 'Reverse proxy',
    default => 'Static files',
};
$lastBackup = $summary['last_backup'];
$none = '<span class="text-body-secondary">—</span>';
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'overview']) ?>
<?php $nt = ThemeContext::build($this); $groups = ThemeNav::grouped($nt['items']); ?>
<?= $this->partial('sites/overview_notices', ['site' => $site, 'inspect' => $inspect, 'inspectError' => $inspectError, 'import' => $import ?? null]) ?>

<div class="row g-4">
    <div class="col-xl-9 order-2 order-xl-1">
        <div id="ncl-tools">
            <?php foreach ($groups as $cat => $g): ?>
                <section class="ncl-group ncl-cat-<?= e($cat) ?>" data-nt-group data-nt-remember="ncl-<?= e($cat) ?>" aria-labelledby="ncl-g-<?= e($cat) ?>">
                    <h2 class="ncl-group-head" id="ncl-g-<?= e($cat) ?>">
                        <button class="ncl-group-toggle" type="button" data-nt-fold="#ncl-body-<?= e($cat) ?>" aria-expanded="true" aria-controls="ncl-body-<?= e($cat) ?>">
                            <span class="ncl-group-icon"><i class="bi bi-<?= e($g['icon']) ?>" aria-hidden="true"></i></span>
                            <span class="ncl-group-title"><?= e($g['label']) ?></span>
                            <span class="ncl-group-count"><?= count($g['items']) ?></span>
                            <i class="bi bi-chevron-down ncl-group-chev" aria-hidden="true"></i>
                        </button>
                    </h2>
                    <div class="ncl-grid" id="ncl-body-<?= e($cat) ?>"><?php foreach ($g['items'] as $tool): ?><?= $this->partial('themes/components/tile', ['tool' => $tool]) ?><?php endforeach; ?></div>
                </section>
            <?php endforeach; ?>
        </div>
    </div>
    <aside class="col-xl-3 order-1 order-xl-2" aria-label="Website details">
        <section class="ncl-card ncl-info">
            <h2 class="ncl-card-title">General information</h2>
            <dl class="nt-dl">
                <dt>Domain</dt><dd class="fw-medium text-break"><?= e($site['domain']) ?></dd>
                <dt>Status</dt><dd><?= $this->partial('themes/components/site_status', ['site' => $site]) ?></dd>
                <dt>Handler</dt><dd><?= e($handler) ?><?php if ($site['kind'] === 'php'): ?> · PHP <?= e($site['php_version']) ?><?php endif; ?></dd>
                <dt>HTTPS</dt><dd><?= $site['https'] ? '<span class="nt-chip is-ok"><i class="bi bi-lock-fill" aria-hidden="true"></i>On</span>' : '<span class="nt-chip is-warn"><i class="bi bi-unlock" aria-hidden="true"></i>Off</span>' ?> <span class="small text-body-secondary"><?= e($sslLabel) ?></span></dd>
                <dt>System user</dt><dd><code><?= e($site['system_user']) ?></code></dd>
                <dt>Home directory</dt><dd class="text-break"><code><?= e($site['document_root']) ?></code></dd>
                <dt>IPv4</dt><dd><?= $serverIp !== null ? e($serverIp) : $none ?></dd>
                <dt>Site size</dt><dd><?= $disk !== null ? e($disk) : $none ?></dd>
                <dt>Database size</dt><dd><?= $dbBytes !== null ? e(format_bytes($dbBytes)) : $none ?> <span class="small text-body-secondary">(<?= (int) $summary['databases'] ?>)</span></dd>
                <dt>Last backup</dt><dd><?= $lastBackup !== null ? e(time_ago((string) $lastBackup['created_at'])) : '<span class="text-body-secondary">None yet</span>' ?></dd>
            </dl>
            <?php if ($canManage && ($this->mayToggleSite() || $this->mayDeleteSite($site))): ?>
                <div class="d-flex flex-wrap gap-2 mt-3">
                    <?php if ($this->mayToggleSite()): ?>
                        <form method="post" action="<?= e($this->url('sites.toggle', ['id' => $id])) ?>" data-nim-busy<?= $site['enabled'] ? ' data-nim-confirm="Disable ' . e($site['domain']) . '? Visitors will no longer reach it. Files, databases and settings are kept."' : '' ?>>
                            <?= $this->csrfField() ?><input type="hidden" name="return_to" value="overview">
                            <button class="btn btn-sm btn-outline-secondary" type="submit"><i class="bi bi-power me-1" aria-hidden="true"></i><?= $site['enabled'] ? 'Disable' : 'Enable' ?></button>
                        </form>
                    <?php endif; ?>
                    <?php if ($this->mayDeleteSite($site)): ?>
                        <a class="btn btn-sm btn-outline-danger" href="<?= e($this->url('sites.advanced', ['id' => $id])) ?>#delete-website"><i class="bi bi-trash me-1" aria-hidden="true"></i>Delete</a>
                    <?php endif; ?>
                </div>
            <?php endif; ?>
        </section>
    </aside>
</div>
