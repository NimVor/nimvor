<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
/** @var array<string, mixed> $summary */
/** @var string|null $serverIp */
/** @var int|null $dbBytes */
use NimVor\Security\Permissions as P;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

// Workspace: the website's tools as cards in a "Hosting & DNS / Files & Databases / ..." layout, with the
// resources and the website's facts in a side panel. The website's name, status and tool tabs are in the frame.
$id = (int) $site['id'];
$canManage = $this->can(P::WEBSITES_MANAGE);
$disk = $inspect !== null ? format_bytes((int) $inspect['disk_bytes']) . (empty($inspect['disk_complete']) ? '+' : '') : null;
$lastBackup = $summary['last_backup'];
$none = '<span class="text-body-secondary">—</span>';
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'overview']) ?>
<?php $nt = ThemeContext::build($this); $groups = ThemeNav::grouped($nt['items']); ?>
<?= $this->partial('sites/overview_notices', ['site' => $site, 'inspect' => $inspect, 'inspectError' => $inspectError, 'import' => $import ?? null]) ?>

<div class="row g-4">
    <div class="col-xxl-9">
        <?php foreach ($groups as $cat => $g): ?>
            <section class="nws-panel nws-toolgroup" aria-labelledby="nws-tg-<?= e($cat) ?>">
                <h2 class="nws-panel-title" id="nws-tg-<?= e($cat) ?>"><i class="bi bi-<?= e($g['icon']) ?>" aria-hidden="true"></i><?= e($g['label']) ?></h2>
                <div class="nws-tools"><?php foreach ($g['items'] as $t): ?>
                    <a class="nws-tool" href="<?= e($t['url']) ?>"><span class="nws-tool-icon" aria-hidden="true"><i class="bi bi-<?= e($t['icon']) ?>"></i></span><span class="nws-tool-label"><?= e($t['label']) ?></span></a>
                <?php endforeach; ?></div>
            </section>
        <?php endforeach; ?>
    </div>
    <aside class="col-xxl-3" aria-label="Website details">
        <section class="nws-panel">
            <h2 class="nws-panel-title"><i class="bi bi-speedometer" aria-hidden="true"></i>Resources</h2>
            <?= $this->partial('themes/components/usage', ['rows' => $nt['account']['usage'], 'variant' => 'bars']) ?>
        </section>
        <section class="nws-panel">
            <h2 class="nws-panel-title"><i class="bi bi-info-circle" aria-hidden="true"></i>Website</h2>
            <dl class="nt-dl">
                <dt>Root directory</dt><dd class="text-break"><code><?= e($site['document_root']) ?></code></dd>
                <dt>IPv4</dt><dd><?= $serverIp !== null ? e($serverIp) : $none ?></dd>
                <dt>Site size</dt><dd><?= $disk !== null ? e($disk) : $none ?></dd>
                <dt>Database size</dt><dd><?= $dbBytes !== null ? e(format_bytes($dbBytes)) : $none ?></dd>
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
