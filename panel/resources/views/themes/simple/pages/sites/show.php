<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $site */
/** @var array<string, mixed> $summary */
/** @var string|null $serverIp */
/** @var int|null $dbBytes */
use NimVor\Security\Permissions as P;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

// Simple: the website as a friendly summary (status cards), its everyday tools as big tiles and the rest, grouped, below.
$id = (int) $site['id'];
$canManage = $this->can(P::WEBSITES_MANAGE);
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'overview']) ?>
<?php
$nt = ThemeContext::build($this);
$groups = ThemeNav::grouped($nt['items']);
$url = static fn (string $key): string => ThemeNav::pick($nt['items'], [$key])[0]['url'] ?? '#';
$has = static fn (string $key): bool => ThemeNav::pick($nt['items'], [$key]) !== [];
$n = (int) $summary['backups'];
$disk = $inspect !== null ? format_bytes((int) $inspect['disk_bytes']) . (empty($inspect['disk_complete']) ? '+' : '') : null;
$rings = array_slice(array_values(array_filter($nt['account']['usage'], static fn (array $u): bool => $u['limit'] > 0)), 0, 4);
?>
<?= $this->partial('sites/overview_notices', ['site' => $site, 'inspect' => $inspect, 'inspectError' => $inspectError, 'import' => $import ?? null]) ?>

<section class="nsi-hero" aria-label="Website">
    <div class="nsi-hero-main">
        <span class="nsi-hero-eyebrow">Website</span>
        <h2 class="nsi-hero-title"><?= e($site['domain']) ?></h2>
        <div class="nsi-hero-meta">
            <?php if (!empty($site['account_suspended'])): ?><span class="nsi-pill is-bad">Suspended</span><?php elseif ($site['status'] === 'error'): ?><span class="nsi-pill is-bad">Needs attention</span><?php elseif ($site['enabled']): ?><span class="nsi-pill is-ok"><i class="bi bi-circle-fill" aria-hidden="true"></i>Online</span><?php else: ?><span class="nsi-pill">Disabled</span><?php endif; ?>
            <?php if ($site['kind'] === 'php'): ?><span class="nsi-pill">PHP <?= e($site['php_version']) ?></span><?php endif; ?>
            <?php if ($disk !== null): ?><span class="nsi-pill"><?= e($disk) ?> used</span><?php endif; ?>
        </div>
    </div>
    <div class="nsi-hero-actions">
        <?php if ($site['enabled']): ?><a class="btn btn-light" href="<?= e(($site['https'] ? 'https://' : 'http://') . $site['domain']) ?>/" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right me-1" aria-hidden="true"></i>Visit site</a><?php endif; ?>
    </div>
</section>

<div class="nsi-status">
    <article class="nsi-card nsi-status-card">
        <span class="nsi-status-icon <?= $site['https'] ? 'is-ok' : 'is-warn' ?>"><i class="bi bi-<?= $site['https'] ? 'shield-lock-fill' : 'shield-exclamation' ?>" aria-hidden="true"></i></span>
        <div class="nsi-status-body"><div class="nsi-status-label">SSL certificate</div><div class="nsi-status-value"><?= $site['https'] ? 'Secured with HTTPS' : 'Not secured yet' ?></div></div>
        <?php if ($has('ssl')): ?><a class="nsi-status-link" href="<?= e($url('ssl')) ?>"><?= $site['https'] ? 'Manage' : 'Secure it' ?><i class="bi bi-arrow-right" aria-hidden="true"></i></a><?php endif; ?>
    </article>
    <article class="nsi-card nsi-status-card">
        <span class="nsi-status-icon <?= $n > 0 ? 'is-ok' : 'is-warn' ?>"><i class="bi bi-archive-fill" aria-hidden="true"></i></span>
        <div class="nsi-status-body"><div class="nsi-status-label">Backups</div><div class="nsi-status-value"><?= $n > 0 ? $n . ' saved' : 'None yet' ?></div></div>
        <?php if ($has('backups')): ?><a class="nsi-status-link" href="<?= e($url('backups')) ?>"><?= $n > 0 ? 'Manage' : 'Back up now' ?><i class="bi bi-arrow-right" aria-hidden="true"></i></a><?php endif; ?>
    </article>
    <article class="nsi-card nsi-status-card">
        <span class="nsi-status-icon"><i class="bi bi-database" aria-hidden="true"></i></span>
        <div class="nsi-status-body"><div class="nsi-status-label">Databases</div><div class="nsi-status-value"><?= (int) $summary['databases'] ?><?= $dbBytes !== null ? ' · ' . e(format_bytes($dbBytes)) : '' ?></div></div>
        <?php if ($has('databases')): ?><a class="nsi-status-link" href="<?= e($url('databases')) ?>">Open<i class="bi bi-arrow-right" aria-hidden="true"></i></a><?php endif; ?>
    </article>
</div>

<?php foreach ($groups as $cat => $g): ?>
    <h2 class="nsi-h"><?= e($g['label']) ?></h2>
    <div class="nsi-quick">
        <?php foreach ($g['items'] as $t): ?>
            <a class="nsi-quick-tile" href="<?= e($t['url']) ?>"><span class="nsi-quick-icon"><i class="bi bi-<?= e($t['icon']) ?>" aria-hidden="true"></i></span><span><?= e($t['label']) ?></span></a>
        <?php endforeach; ?>
    </div>
<?php endforeach; ?>

<?php if ($rings !== []): ?>
    <h2 class="nsi-h">Your plan<?= $nt['account']['plan'] !== '' ? ' · ' . e($nt['account']['plan']) : '' ?></h2>
    <section class="nsi-rings">
        <?php foreach ($rings as $u): $pct = (int) min(100, round(100 * $u['used'] / max(1, $u['limit']))); ?>
            <div class="nsi-ringbox">
                <div class="nsi-ring nsi-ring-<?= (int) (round($pct / 5) * 5) ?><?= $pct >= 100 ? ' is-full' : ($pct >= 80 ? ' is-high' : '') ?>" role="img" aria-label="<?= e($u['label']) ?>: <?= (int) $u['used'] ?> of <?= (int) $u['limit'] ?>"><span><?= $pct ?>%</span></div>
                <div class="nsi-ring-label"><?= e($u['label']) ?></div>
                <div class="nsi-ring-sub"><?= (int) $u['used'] ?> of <?= (int) $u['limit'] ?></div>
            </div>
        <?php endforeach; ?>
    </section>
<?php endif; ?>

<?php if ($canManage && ($this->mayToggleSite() || $this->mayDeleteSite($site))): ?>
    <h2 class="nsi-h">Website settings</h2>
    <div class="d-flex flex-wrap gap-2">
        <?php if ($this->mayToggleSite()): ?>
            <form method="post" action="<?= e($this->url('sites.toggle', ['id' => $id])) ?>" data-nim-busy<?= $site['enabled'] ? ' data-nim-confirm="Disable ' . e($site['domain']) . '? Visitors will no longer reach it. Files, databases and settings are kept."' : '' ?>>
                <?= $this->csrfField() ?><input type="hidden" name="return_to" value="overview">
                <button class="btn btn-outline-secondary" type="submit"><i class="bi bi-power me-1" aria-hidden="true"></i><?= $site['enabled'] ? 'Disable the website' : 'Enable the website' ?></button>
            </form>
        <?php endif; ?>
        <?php if ($this->mayDeleteSite($site)): ?>
            <a class="btn btn-outline-danger" href="<?= e($this->url('sites.advanced', ['id' => $id])) ?>#delete-website"><i class="bi bi-trash me-1" aria-hidden="true"></i>Delete the website</a>
        <?php endif; ?>
    </div>
<?php endif; ?>
