<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $managed */
use NimVor\Security\Permissions as P;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$site = $nt['site'];
$groups = ThemeNav::grouped($nt['items']);
$canCreate = $this->can(P::WEBSITES_MANAGE);
$who = !empty($user) ? ($user['display_name'] !== '' ? $user['display_name'] : $user['username']) : '';
$initial = strtoupper(substr($who !== '' ? $who : '?', 0, 1));
$accountTools = [];
foreach ($nt['accountPages'] as $p) {
    $accountTools[] = ['key' => 'account', 'label' => $p['label'], 'icon' => $p['icon'], 'url' => $p['url']];
}
?>
<?php $this->start(); ?>Home<?php $this->end('title'); ?>
<?php $this->start(); ?><?= count($managed) ?> website<?= count($managed) === 1 ? '' : 's' ?> on this account<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><?php if ($site !== null): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('sites.show', ['id' => (int) $site['id']])) ?>"><i class="bi bi-speedometer2 me-1" aria-hidden="true"></i>Overview</a><?php if ($site['enabled']): ?> <a class="btn btn-sm btn-outline-secondary" href="<?= e(($site['https'] ? 'https://' : 'http://') . $site['domain']) ?>/" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right me-1" aria-hidden="true"></i>Visit</a><?php endif; ?><?php endif; ?><?php if ($canCreate): ?> <a class="btn btn-sm btn-primary" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Create site</a><?php endif; ?><?php $this->end('actions'); ?>

<?php if ($site === null): ?>
    <div class="ncl-card"><div class="nim-empty-state">
        <i class="bi bi-globe2" aria-hidden="true"></i>
        <div class="fw-medium">No websites yet</div>
        <div class="small"><?= $canCreate ? 'Create your first website to get started.' : 'Your hosting provider has not set up a website for you yet.' ?></div>
        <?php if ($canCreate): ?><a class="btn btn-primary mt-3" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Create site</a><?php endif; ?>
    </div></div>
<?php else: ?>

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
            <?php if ($accountTools !== []): ?>
                <section class="ncl-group ncl-cat-account" data-nt-group data-nt-remember="ncl-account" aria-labelledby="ncl-g-account">
                    <h2 class="ncl-group-head" id="ncl-g-account">
                        <button class="ncl-group-toggle" type="button" data-nt-fold="#ncl-body-account" aria-expanded="true" aria-controls="ncl-body-account">
                            <span class="ncl-group-icon"><i class="bi bi-person-gear" aria-hidden="true"></i></span>
                            <span class="ncl-group-title">Account</span>
                            <span class="ncl-group-count"><?= count($accountTools) ?></span>
                            <i class="bi bi-chevron-down ncl-group-chev" aria-hidden="true"></i>
                        </button>
                    </h2>
                    <div class="ncl-grid" id="ncl-body-account"><?php foreach ($accountTools as $tool): ?><?= $this->partial('themes/components/tile', ['tool' => $tool]) ?><?php endforeach; ?></div>
                </section>
            <?php endif; ?>
            <p class="text-body-secondary" data-nt-empty hidden>No tool matches your search.</p>
        </div>
    </div>
    <aside class="col-xl-3 order-1 order-xl-2" aria-label="Account">
        <section class="ncl-card ncl-info">
            <h2 class="ncl-card-title">General information</h2>
            <div class="ncl-user"><span class="ncl-avatar" aria-hidden="true"><?= e($initial) ?></span><div><div class="fw-semibold"><?= e($who) ?></div><div class="small text-body-secondary"><?= e($user['role_label'] ?? '') ?></div></div></div>
            <?php if (count($managed) > 1): ?>
                <label class="form-label small text-body-secondary mb-1" for="ncl-site">Website</label>
                <select class="form-select form-select-sm mb-3" id="ncl-site" data-nt-jump>
                    <?php foreach ($managed as $m): ?><option value="<?= e($this->url('websites', [], ['site' => (int) $m['id']])) ?>"<?= (int) $m['id'] === (int) $site['id'] ? ' selected' : '' ?>><?= e($m['domain']) ?></option><?php endforeach; ?>
                </select>
            <?php endif; ?>
            <dl class="nt-dl">
                <dt>Primary domain</dt><dd class="fw-medium"><?= e($site['domain']) ?></dd>
                <?php if ($nt['account']['plan'] !== ''): ?><dt>Package</dt><dd><?= e($nt['account']['plan']) ?></dd><?php endif; ?>
                <dt>Status</dt><dd><?= $this->partial('themes/components/site_status', ['site' => $site]) ?></dd>
                <?php if ($site['kind'] === 'php'): ?><dt>PHP version</dt><dd><?= e($site['php_version']) ?></dd><?php endif; ?>
                <dt>HTTPS</dt><dd><?= $site['https'] ? '<span class="nt-chip is-ok"><i class="bi bi-lock-fill" aria-hidden="true"></i>On</span>' : '<span class="nt-chip is-warn"><i class="bi bi-unlock" aria-hidden="true"></i>Off</span>' ?></dd>
                <dt>System user</dt><dd><?= e($site['system_user']) ?></dd>
                <?php if (!empty($user['last_login_at'])): ?><dt>Last sign-in</dt><dd><?= e(time_ago($user['last_login_at'])) ?></dd><?php endif; ?>
                <dt>Theme</dt><dd>Classic · <a href="<?= e($this->url('appearance')) ?>">change</a></dd>
            </dl>
        </section>
        <section class="ncl-card">
            <h2 class="ncl-card-title">Hosting package</h2>
            <?= $this->partial('themes/components/package', ['plan' => $nt['account']['plan'], 'rows' => $nt['account']['usage']]) ?>
        </section>
        <section class="ncl-card">
            <h2 class="ncl-card-title">Statistics</h2>
            <?= $this->partial('themes/components/usage', ['rows' => $nt['account']['usage'], 'variant' => 'bars']) ?>
        </section>
    </aside>
</div>
<?php endif; ?>
