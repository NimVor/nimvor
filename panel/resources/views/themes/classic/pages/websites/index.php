<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $managed */
use NimVor\Security\Permissions as P;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$site = $nt['site'];
$groups = ThemeNav::grouped($nt['items']);
$frequent = ThemeNav::pick($nt['items'], ['files', 'mail', 'databases', 'ssl', 'backups', 'php', 'dns']);
$canCreate = $this->can(P::WEBSITES_MANAGE);
$who = !empty($user) ? ($user['display_name'] !== '' ? $user['display_name'] : $user['username']) : '';
?>
<?php $this->start(); ?>Home<?php $this->end('title'); ?>
<?php $this->start(); ?><?= count($managed) ?> website<?= count($managed) === 1 ? '' : 's' ?> on this account<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><?php if ($canCreate): ?><a class="btn btn-sm btn-primary" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Create site</a><?php endif; ?><?php $this->end('actions'); ?>

<?php if ($site === null): ?>
    <div class="nt-box"><div class="nim-empty-state">
        <i class="bi bi-globe2" aria-hidden="true"></i>
        <div class="fw-medium">No websites yet</div>
        <div class="small"><?= $canCreate ? 'Create your first website to get started.' : 'Your hosting provider has not set up a website for you yet.' ?></div>
        <?php if ($canCreate): ?><a class="btn btn-primary mt-3" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Create site</a><?php endif; ?>
    </div></div>
<?php else: ?>
<div class="row g-3">
    <aside class="col-lg-3" aria-label="Account">
        <section class="nt-box mb-3">
            <h2 class="nt-box-head h6 mb-0"><i class="bi bi-person-badge" aria-hidden="true"></i>General information</h2>
            <div class="nt-box-body">
                <dl class="nt-dl">
                    <dt>Account</dt><dd><?= e($who) ?></dd>
                    <?php if ($nt['account']['plan'] !== ''): ?><dt>Package</dt><dd><?= e($nt['account']['plan']) ?></dd><?php endif; ?>
                    <dt>Websites</dt><dd><?= count($managed) ?></dd>
                    <dt>Website</dt><dd><?= e($site['domain']) ?> <?= $this->partial('themes/components/site_status', ['site' => $site]) ?></dd>
                    <?php if ($site['kind'] === 'php'): ?><dt>PHP</dt><dd><?= e($site['php_version']) ?></dd><?php endif; ?>
                    <dt>HTTPS</dt><dd><?= $site['https'] ? 'On' : 'Off' ?></dd>
                </dl>
            </div>
        </section>
        <section class="nt-box">
            <h2 class="nt-box-head h6 mb-0"><i class="bi bi-speedometer" aria-hidden="true"></i>Statistics</h2>
            <div class="nt-box-body"><?= $this->partial('themes/components/usage', ['rows' => $nt['account']['usage'], 'variant' => 'bars']) ?></div>
        </section>
    </aside>
    <div class="col-lg-9">
        <section class="nt-box mb-3">
            <div class="nt-box-body ncl-toolbar">
                <div class="ncl-pick">
                    <?php if (count($managed) > 1): ?>
                        <label class="form-label small mb-1" for="ncl-site">Website</label>
                        <select class="form-select" id="ncl-site" data-nt-jump>
                            <?php foreach ($managed as $m): ?><option value="<?= e($this->url('websites', [], ['site' => (int) $m['id']])) ?>"<?= (int) $m['id'] === (int) $site['id'] ? ' selected' : '' ?>><?= e($m['domain']) ?></option><?php endforeach; ?>
                        </select>
                    <?php else: ?>
                        <div class="small text-body-secondary">Website</div><div class="fw-semibold"><?= e($site['domain']) ?></div>
                    <?php endif; ?>
                </div>
                <div class="ncl-search">
                    <label class="form-label small mb-1" for="ncl-filter">Find a tool</label>
                    <div class="input-group"><span class="input-group-text"><i class="bi bi-search" aria-hidden="true"></i></span><input class="form-control" id="ncl-filter" type="search" placeholder="Files, email, SSL, backups…" data-nt-filter="#ncl-tools" autocomplete="off"></div>
                </div>
                <div class="ncl-open">
                    <a class="btn btn-outline-primary" href="<?= e($this->url('sites.show', ['id' => (int) $site['id']])) ?>"><i class="bi bi-speedometer2 me-1" aria-hidden="true"></i>Overview</a>
                    <?php if ($site['enabled']): ?><a class="btn btn-outline-secondary" href="<?= e(($site['https'] ? 'https://' : 'http://') . $site['domain']) ?>/" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right me-1" aria-hidden="true"></i>Visit</a><?php endif; ?>
                </div>
            </div>
        </section>

        <div id="ncl-tools">
            <?php if ($frequent !== []): ?>
                <section class="ncl-group" data-nt-group aria-labelledby="ncl-g-frequent">
                    <h2 class="ncl-group-title" id="ncl-g-frequent"><i class="bi bi-star" aria-hidden="true"></i>Frequently used</h2>
                    <div class="ncl-grid"><?php foreach ($frequent as $tool): ?><?= $this->partial('themes/components/tile', ['tool' => $tool]) ?><?php endforeach; ?></div>
                </section>
            <?php endif; ?>
            <?php foreach ($groups as $cat => $g): ?>
                <section class="ncl-group" data-nt-group aria-labelledby="ncl-g-<?= e($cat) ?>">
                    <h2 class="ncl-group-title" id="ncl-g-<?= e($cat) ?>"><i class="bi bi-<?= e($g['icon']) ?>" aria-hidden="true"></i><?= e($g['label']) ?></h2>
                    <div class="ncl-grid"><?php foreach ($g['items'] as $tool): ?><?= $this->partial('themes/components/tile', ['tool' => $tool]) ?><?php endforeach; ?></div>
                </section>
            <?php endforeach; ?>
            <?php if ($nt['accountPages'] !== []): ?>
                <section class="ncl-group" data-nt-group aria-labelledby="ncl-g-account">
                    <h2 class="ncl-group-title" id="ncl-g-account"><i class="bi bi-person-gear" aria-hidden="true"></i>Account</h2>
                    <div class="ncl-grid"><?php foreach ($nt['accountPages'] as $p): ?><?= $this->partial('themes/components/tile', ['tool' => ['key' => 'account', 'label' => $p['label'], 'icon' => $p['icon'], 'url' => $p['url']]]) ?><?php endforeach; ?></div>
                </section>
            <?php endif; ?>
            <p class="text-body-secondary" data-nt-empty hidden>No tool matches your search.</p>
        </div>
    </div>
</div>
<?php endif; ?>
