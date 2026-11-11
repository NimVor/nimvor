<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $managed */
/** @var array<int, int> $backups */
use NimVor\Security\Permissions as P;
use NimVor\View\SiteMenu;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$canCreate = $this->can(P::WEBSITES_MANAGE);
$quickKeys = ['files', 'databases', 'mail', 'backups', 'git', 'logs'];
$backups ??= [];
?>
<?php $this->start(); ?>Websites &amp; domains<?php $this->end('title'); ?>
<?php $this->start(); ?><?= count($managed) ?> website<?= count($managed) === 1 ? '' : 's' ?><?= $nt['account']['plan'] !== '' ? ' · ' . e($nt['account']['plan']) . ' plan' : '' ?><?php $this->end('subtitle'); ?>
<?php $this->start(); ?><?php if ($canCreate): ?><a class="btn btn-sm btn-primary" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Add website</a><?php endif; ?><?php $this->end('actions'); ?>

<?php if ($managed === []): ?>
    <div class="nt-box"><div class="nim-empty-state">
        <i class="bi bi-globe2" aria-hidden="true"></i>
        <div class="fw-medium">No websites yet</div>
        <div class="small"><?= $canCreate ? 'Add your first website to get started.' : 'Your hosting provider has not set up a website for you yet.' ?></div>
        <?php if ($canCreate): ?><a class="btn btn-primary mt-3" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Add website</a><?php endif; ?>
    </div></div>
<?php else: ?>
<div class="row g-3">
    <div class="col-xl-9">
        <div class="nws-filter mb-3">
            <label class="visually-hidden" for="nws-filter">Filter websites</label>
            <div class="input-group input-group-sm"><span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span><input class="form-control" id="nws-filter" type="search" placeholder="Filter websites" data-nt-filter="#nws-cards" autocomplete="off"></div>
        </div>
        <div class="nws-cards" id="nws-cards">
            <?php foreach ($managed as $m):
                $id = (int) $m['id'];
                $tools = ThemeNav::pick(SiteMenu::items($this, $m), $quickKeys);
                $scheme = $m['https'] ? 'https://' : 'http://';
                $n = (int) ($backups[$id] ?? 0);
                ?>
                <article class="nws-card" data-nt-tile data-name="<?= e(strtolower($m['domain'] . ' ' . implode(' ', $m['aliases']))) ?>">
                    <header class="nws-card-head">
                        <a class="nws-card-title" href="<?= e($this->url('sites.show', ['id' => $id])) ?>"><i class="bi bi-globe2" aria-hidden="true"></i><?= e($m['domain']) ?></a>
                        <?= $this->partial('themes/components/site_status', ['site' => $m]) ?>
                    </header>
                    <dl class="nws-meta">
                        <div><dt>Type</dt><dd><?= $m['kind'] === 'php' ? 'PHP ' . e($m['php_version']) : e(\NimVor\Services\WebsiteService::KINDS[$m['kind']] ?? $m['kind']) ?></dd></div>
                        <div><dt>SSL</dt><dd><?= $m['https'] ? '<span class="nt-chip is-ok"><i class="bi bi-lock-fill" aria-hidden="true"></i>On</span>' : '<span class="nt-chip is-warn"><i class="bi bi-unlock" aria-hidden="true"></i>Off</span>' ?></dd></div>
                        <div><dt>Backups</dt><dd><?= $n > 0 ? $n : 'None yet' ?></dd></div>
                        <div><dt>User</dt><dd class="text-truncate"><?= e($m['system_user']) ?></dd></div>
                    </dl>
                    <?php if ($m['aliases'] !== []): ?><div class="small text-body-secondary text-truncate">+ <?= e(implode(', ', array_slice($m['aliases'], 0, 3))) ?></div><?php endif; ?>
                    <div class="nws-quick" role="group" aria-label="Quick actions for <?= e($m['domain']) ?>">
                        <?php foreach ($tools as $t): ?><a class="nws-quick-link" href="<?= e($t['url']) ?>"><i class="bi bi-<?= e($t['icon']) ?>" aria-hidden="true"></i><span><?= e($t['label']) ?></span></a><?php endforeach; ?>
                    </div>
                    <footer class="nws-card-foot">
                        <a class="btn btn-sm btn-primary" href="<?= e($this->url('sites.show', ['id' => $id])) ?>">Manage</a>
                        <?php if ($m['enabled']): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($scheme . $m['domain']) ?>/" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right me-1" aria-hidden="true"></i>Open</a><?php endif; ?>
                    </footer>
                </article>
            <?php endforeach; ?>
            <p class="text-body-secondary" data-nt-empty hidden>No website matches.</p>
        </div>
    </div>
    <aside class="col-xl-3" aria-label="Plan">
        <section class="nt-box">
            <h2 class="nt-box-head h6 mb-0"><i class="bi bi-speedometer" aria-hidden="true"></i>Resources<?= $nt['account']['plan'] !== '' ? ' · ' . e($nt['account']['plan']) : '' ?></h2>
            <div class="nt-box-body"><?= $this->partial('themes/components/usage', ['rows' => $nt['account']['usage'], 'variant' => 'bars']) ?></div>
        </section>
    </aside>
</div>
<?php endif; ?>
