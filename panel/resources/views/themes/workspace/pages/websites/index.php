<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $managed */
/** @var array<int, int> $backups */
use NimVor\Security\Permissions as P;
use NimVor\View\SiteMenu;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeInsights;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$canCreate = $this->can(P::WEBSITES_MANAGE);
$quickKeys = ['files', 'databases', 'mail', 'backups', 'git', 'logs'];
$backups ??= [];
$total = count($managed);
$attention = ThemeInsights::attention($this, $managed, $backups);
$active = count(array_filter($managed, static fn (array $m): bool => $m['enabled'] && $m['status'] !== 'error'));
$secure = count(array_filter($managed, static fn (array $m): bool => $m['https']));
$withBackup = count(array_filter($managed, static fn (array $m): bool => (int) ($backups[(int) $m['id']] ?? 0) > 0));
$tint = static fn (string $domain): int => abs(crc32($domain)) % 8;
$stats = [
    ['Websites', $total, 'globe2', 'ws-blue'],
    ['Running', $active, 'activity', 'ws-green'],
    ['HTTPS secured', $secure . ' / ' . $total, 'shield-lock', 'ws-violet'],
    ['With backups', $withBackup . ' / ' . $total, 'archive', 'ws-amber'],
];
?>
<?php $this->start(); ?>Websites &amp; domains<?php $this->end('title'); ?>
<?php $this->start(); ?><?= $nt['account']['plan'] !== '' ? e($nt['account']['plan']) . ' plan' : 'Everything you host, at a glance' ?><?php $this->end('subtitle'); ?>
<?php $this->start(); ?><?php if ($canCreate): ?><a class="btn btn-primary" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Add website</a><?php endif; ?><?php $this->end('actions'); ?>

<?php if ($managed === []): ?>
    <div class="nt-box"><div class="nim-empty-state">
        <i class="bi bi-globe2" aria-hidden="true"></i>
        <div class="fw-medium">No websites yet</div>
        <div class="small"><?= $canCreate ? 'Add your first website to get started.' : 'Your hosting provider has not set up a website for you yet.' ?></div>
        <?php if ($canCreate): ?><a class="btn btn-primary mt-3" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Add website</a><?php endif; ?>
    </div></div>
<?php else: ?>
<div class="nws-stats" role="list" aria-label="Summary">
    <?php foreach ($stats as [$label, $value, $icon, $hue]): ?>
        <div class="nws-stat <?= e($hue) ?>" role="listitem"><span class="nws-stat-icon" aria-hidden="true"><i class="bi bi-<?= e($icon) ?>"></i></span><div><div class="nws-stat-value"><?= is_int($value) ? $value : e($value) ?></div><div class="nws-stat-label"><?= e($label) ?></div></div></div>
    <?php endforeach; ?>
</div>
<div class="row g-4">
    <div class="col-xxl-9">
        <div class="nws-filter">
            <label class="visually-hidden" for="nws-filter">Filter websites</label>
            <i class="bi bi-search" aria-hidden="true"></i>
            <input class="form-control" id="nws-filter" type="search" placeholder="Filter websites" data-nt-filter="#nws-cards" autocomplete="off">
        </div>
        <div class="nws-cards" id="nws-cards">
            <?php foreach ($managed as $m):
                $id = (int) $m['id'];
                $tools = ThemeNav::pick(SiteMenu::items($this, $m), $quickKeys);
                $scheme = $m['https'] ? 'https://' : 'http://';
                $n = (int) ($backups[$id] ?? 0);
                ?>
                <article class="nws-card" data-nt-tile data-name="<?= e(strtolower($m['domain'] . ' ' . implode(' ', $m['aliases']))) ?>">
                    <a class="nws-thumb-lg nws-tint-<?= $tint((string) $m['domain']) ?>" href="<?= e($this->url('sites.show', ['id' => $id])) ?>" tabindex="-1" aria-hidden="true">
                        <span class="nws-chrome"><i></i><i></i><i></i><em><?= e($m['domain']) ?></em></span>
                        <b><?= e(strtoupper(substr((string) $m['domain'], 0, 1))) ?></b>
                    </a>
                    <div class="nws-card-body">
                        <div class="nws-card-head">
                            <a class="nws-card-title" href="<?= e($this->url('sites.show', ['id' => $id])) ?>"><?= e($m['domain']) ?></a>
                            <?= $this->partial('themes/components/site_status', ['site' => $m]) ?>
                        </div>
                        <div class="nws-chips">
                            <span class="nws-chip"><i class="bi bi-<?= $m['kind'] === 'php' ? 'filetype-php' : 'file-earmark-code' ?>" aria-hidden="true"></i><?= $m['kind'] === 'php' ? 'PHP ' . e($m['php_version']) : e(\NimVor\Services\WebsiteService::KINDS[$m['kind']] ?? $m['kind']) ?></span>
                            <span class="nws-chip <?= $m['https'] ? 'is-ok' : 'is-warn' ?>"><i class="bi bi-<?= $m['https'] ? 'lock-fill' : 'unlock' ?>" aria-hidden="true"></i>SSL <?= $m['https'] ? 'on' : 'off' ?></span>
                            <span class="nws-chip"><i class="bi bi-archive" aria-hidden="true"></i><?= $n > 0 ? $n . ' backup' . ($n === 1 ? '' : 's') : 'No backups' ?></span>
                        </div>
                        <?php if ($m['aliases'] !== []): ?><div class="small text-body-secondary text-truncate">+ <?= e(implode(', ', array_slice($m['aliases'], 0, 3))) ?></div><?php endif; ?>
                        <div class="nws-quick" role="group" aria-label="Quick actions for <?= e($m['domain']) ?>">
                            <?php foreach ($tools as $t): ?><a class="nws-quick-link" href="<?= e($t['url']) ?>" title="<?= e($t['label']) ?>"><i class="bi bi-<?= e($t['icon']) ?>" aria-hidden="true"></i><span><?= e($t['label']) ?></span></a><?php endforeach; ?>
                        </div>
                        <div class="nws-card-foot">
                            <a class="btn btn-sm btn-primary" href="<?= e($this->url('sites.show', ['id' => $id])) ?>">Manage</a>
                            <?php if ($m['enabled']): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($scheme . $m['domain']) ?>/" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right me-1" aria-hidden="true"></i>Open</a><?php endif; ?>
                        </div>
                    </div>
                </article>
            <?php endforeach; ?>
            <p class="text-body-secondary" data-nt-empty hidden>No website matches.</p>
        </div>
    </div>
    <aside class="col-xxl-3" aria-label="Plan">
        <section class="nws-panel nws-attention">
            <h2 class="nws-panel-title"><i class="bi bi-bell" aria-hidden="true"></i>Needs attention</h2>
            <?php if ($attention === []): ?>
                <p class="small text-body-secondary mb-0"><i class="bi bi-check-circle-fill text-success me-1" aria-hidden="true"></i>Everything looks good.</p>
            <?php else: ?>
                <ul class="nws-attn-list">
                    <?php foreach (array_slice($attention, 0, 6) as $a): ?>
                        <li class="is-<?= e($a['level']) ?>"><i class="bi bi-<?= e($a['icon']) ?>" aria-hidden="true"></i><div><div class="fw-medium"><?= e($a['site']) ?></div><div class="small text-body-secondary"><?= e($a['text']) ?></div></div><a class="btn btn-sm btn-outline-secondary" href="<?= e($a['url']) ?>"><?= e($a['action']) ?></a></li>
                    <?php endforeach; ?>
                </ul>
            <?php endif; ?>
        </section>
        <section class="nws-panel">
            <h2 class="nws-panel-title"><i class="bi bi-speedometer" aria-hidden="true"></i>Resources<?= $nt['account']['plan'] !== '' ? ' · ' . e($nt['account']['plan']) : '' ?></h2>
            <?= $this->partial('themes/components/usage', ['rows' => $nt['account']['usage'], 'variant' => 'bars']) ?>
        </section>
    </aside>
</div>
<?php endif; ?>
