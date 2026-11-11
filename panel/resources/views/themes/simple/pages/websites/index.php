<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $managed */
use NimVor\Security\Permissions as P;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$site = $nt['site'];
$canCreate = $this->can(P::WEBSITES_MANAGE);
$who = !empty($user) ? ($user['display_name'] !== '' ? $user['display_name'] : $user['username']) : '';
$labels = ['dns' => 'Domains & DNS', 'php' => $site !== null && $site['kind'] === 'php' ? 'PHP ' . $site['php_version'] : 'PHP', 'stats' => 'Usage statistics'];
$tools = [];
foreach (ThemeNav::pick($nt['items'], ['files', 'mail', 'databases', 'dns', 'ssl', 'backups', 'php', 'stats']) as $t) {
    $t['label'] = $labels[$t['key']] ?? $t['label'];
    $tools[] = $t;
}
?>
<?php $this->start(); ?>Hello<?= $who !== '' ? ', ' . e($who) : '' ?><?php $this->end('title'); ?>
<?php $this->start(); ?><?= count($managed) ?> website<?= count($managed) === 1 ? '' : 's' ?><?php $this->end('subtitle'); ?>
<?php $this->start(); ?><?php if ($canCreate): ?><a class="btn btn-primary" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>New website</a><?php endif; ?><?php $this->end('actions'); ?>

<?php if ($site === null): ?>
    <div class="nsi-card text-center py-5">
        <i class="bi bi-globe2 display-5 text-body-secondary" aria-hidden="true"></i>
        <h2 class="h5 mt-3">No websites yet</h2>
        <p class="text-body-secondary"><?= $canCreate ? 'Create your first website to get started.' : 'Your hosting provider has not set up a website for you yet.' ?></p>
        <?php if ($canCreate): ?><a class="btn btn-primary" href="<?= e($this->url('sites.create')) ?>">Create a website</a><?php endif; ?>
    </div>
<?php else: ?>
    <section class="nsi-card nsi-hero" aria-label="Website">
        <div class="nsi-hero-main">
            <div class="small text-body-secondary">Your website</div>
            <h2 class="h4 mb-1"><?= e($site['domain']) ?> <?= $this->partial('themes/components/site_status', ['site' => $site]) ?></h2>
            <div class="small text-body-secondary"><?= $site['kind'] === 'php' ? 'PHP ' . e($site['php_version']) . ' · ' : '' ?>SSL <?= $site['https'] ? 'on' : 'off' ?></div>
        </div>
        <div class="nsi-hero-actions">
            <a class="btn btn-outline-primary" href="<?= e($this->url('sites.show', ['id' => (int) $site['id']])) ?>">Overview</a>
            <?php if ($site['enabled']): ?><a class="btn btn-outline-secondary" href="<?= e(($site['https'] ? 'https://' : 'http://') . $site['domain']) ?>/" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right me-1" aria-hidden="true"></i>Visit</a><?php endif; ?>
        </div>
        <?php if (count($managed) > 1): ?>
            <div class="nsi-sites" role="list" aria-label="Your websites">
                <?php foreach ($managed as $m): ?><a role="listitem" class="nsi-sitechip<?= (int) $m['id'] === (int) $site['id'] ? ' active' : '' ?>" href="<?= e($this->url('websites', [], ['site' => (int) $m['id']])) ?>"><?= e($m['domain']) ?></a><?php endforeach; ?>
            </div>
        <?php endif; ?>
    </section>

    <h2 class="nsi-h">Quick access</h2>
    <div class="nsi-quick">
        <?php foreach ($tools as $t): ?>
            <a class="nsi-quick-tile" href="<?= e($t['url']) ?>"><span class="nsi-quick-icon"><i class="bi bi-<?= e($t['icon']) ?>" aria-hidden="true"></i></span><span><?= e($t['label']) ?></span></a>
        <?php endforeach; ?>
        <button class="nsi-quick-tile" type="button" data-bs-toggle="offcanvas" data-bs-target="#nsi-tools" aria-controls="nsi-tools"><span class="nsi-quick-icon"><i class="bi bi-grid" aria-hidden="true"></i></span><span>All tools</span></button>
    </div>

    <h2 class="nsi-h">Your plan<?= $nt['account']['plan'] !== '' ? ' · ' . e($nt['account']['plan']) : '' ?></h2>
    <section class="nsi-card"><?= $this->partial('themes/components/usage', ['rows' => $nt['account']['usage'], 'variant' => 'inline']) ?></section>
<?php endif; ?>
