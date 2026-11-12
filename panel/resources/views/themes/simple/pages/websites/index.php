<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $managed */
/** @var array<int, int> $backups */
use NimVor\Security\Permissions as P;
use NimVor\View\ThemeContext;
use NimVor\View\ThemeInsights;
use NimVor\View\ThemeNav;

$nt = ThemeContext::build($this);
$site = $nt['site'];
$canCreate = $this->can(P::WEBSITES_MANAGE);
$backups ??= [];
$todo = ThemeInsights::attention($this, $managed, $backups);
$who = !empty($user) ? ($user['display_name'] !== '' ? $user['display_name'] : $user['username']) : '';
$hour = (int) date('G');
$greet = $hour < 12 ? 'Good morning' : ($hour < 18 ? 'Good afternoon' : 'Good evening');
$labels = ['dns' => 'Domains & DNS', 'php' => 'PHP settings', 'stats' => 'Usage statistics', 'ssl' => 'SSL / HTTPS'];
$tools = [];
foreach (ThemeNav::pick($nt['items'], ['files', 'mail', 'databases', 'dns', 'ssl', 'backups', 'php', 'stats']) as $t) {
    $t['label'] = $labels[$t['key']] ?? $t['label'];
    $tools[] = $t;
}
$url = static fn (string $key): string => ThemeNav::pick($nt['items'], [$key])[0]['url'] ?? '#';
$has = static fn (string $key): bool => ThemeNav::pick($nt['items'], [$key]) !== [];
$rings = array_values(array_filter($nt['account']['usage'], static fn (array $u): bool => $u['limit'] > 0));
$rings = array_slice($rings, 0, 4);
?>
<?php $this->start(); ?><?= e($greet) ?><?= $who !== '' ? ', ' . e($who) : '' ?><?php $this->end('title'); ?>
<?php $this->start(); ?><?= count($managed) ?> website<?= count($managed) === 1 ? '' : 's' ?> · here is how they are doing<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><?php if ($canCreate): ?><a class="btn btn-primary" href="<?= e($this->url('sites.create')) ?>"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>New website</a><?php endif; ?><?php $this->end('actions'); ?>

<?php if ($site === null): ?>
    <div class="nsi-card text-center py-5">
        <i class="bi bi-globe2 display-5 text-body-secondary" aria-hidden="true"></i>
        <h2 class="h5 mt-3">No websites yet</h2>
        <p class="text-body-secondary"><?= $canCreate ? 'Create your first website to get started.' : 'Your hosting provider has not set up a website for you yet.' ?></p>
        <?php if ($canCreate): ?><a class="btn btn-primary" href="<?= e($this->url('sites.create')) ?>">Create a website</a><?php endif; ?>
    </div>
<?php else: ?>
    <section class="nsi-hero" aria-label="Website">
        <div class="nsi-hero-main">
            <span class="nsi-hero-eyebrow">Your website</span>
            <h2 class="nsi-hero-title"><?= e($site['domain']) ?></h2>
            <div class="nsi-hero-meta">
                <?php if (!empty($site['account_suspended'])): ?><span class="nsi-pill is-bad">Suspended</span><?php elseif ($site['status'] === 'error'): ?><span class="nsi-pill is-bad">Needs attention</span><?php elseif ($site['enabled']): ?><span class="nsi-pill is-ok"><i class="bi bi-circle-fill" aria-hidden="true"></i>Online</span><?php else: ?><span class="nsi-pill">Disabled</span><?php endif; ?>
                <?php if ($site['kind'] === 'php'): ?><span class="nsi-pill">PHP <?= e($site['php_version']) ?></span><?php endif; ?>
            </div>
        </div>
        <div class="nsi-hero-actions">
            <?php if ($site['enabled']): ?><a class="btn btn-light" href="<?= e(($site['https'] ? 'https://' : 'http://') . $site['domain']) ?>/" target="_blank" rel="noopener noreferrer"><i class="bi bi-box-arrow-up-right me-1" aria-hidden="true"></i>Visit site</a><?php endif; ?>
            <a class="btn btn-outline-light" href="<?= e($this->url('sites.show', ['id' => (int) $site['id']])) ?>">Overview</a>
        </div>
        <?php if (count($managed) > 1): ?>
            <div class="nsi-sites" role="list" aria-label="Your websites">
                <?php foreach ($managed as $m): ?><a role="listitem" class="nsi-sitechip<?= (int) $m['id'] === (int) $site['id'] ? ' active' : '' ?>" href="<?= e($this->url('websites', [], ['site' => (int) $m['id']])) ?>"><?= e($m['domain']) ?></a><?php endforeach; ?>
            </div>
        <?php endif; ?>
    </section>

    <div class="nsi-status">
        <article class="nsi-card nsi-status-card">
            <span class="nsi-status-icon <?= $site['https'] ? 'is-ok' : 'is-warn' ?>"><i class="bi bi-<?= $site['https'] ? 'shield-lock-fill' : 'shield-exclamation' ?>" aria-hidden="true"></i></span>
            <div class="nsi-status-body"><div class="nsi-status-label">SSL certificate</div><div class="nsi-status-value"><?= $site['https'] ? 'Secured with HTTPS' : 'Not secured yet' ?></div></div>
            <?php if ($has('ssl')): ?><a class="nsi-status-link" href="<?= e($url('ssl')) ?>"><?= $site['https'] ? 'Manage' : 'Secure it' ?><i class="bi bi-arrow-right" aria-hidden="true"></i></a><?php endif; ?>
        </article>
        <?php if ($site['kind'] === 'php'): ?>
        <article class="nsi-card nsi-status-card">
            <span class="nsi-status-icon"><i class="bi bi-filetype-php" aria-hidden="true"></i></span>
            <div class="nsi-status-body"><div class="nsi-status-label">PHP version</div><div class="nsi-status-value">PHP <?= e($site['php_version']) ?></div></div>
            <?php if ($has('php')): ?><a class="nsi-status-link" href="<?= e($url('php')) ?>">Change<i class="bi bi-arrow-right" aria-hidden="true"></i></a><?php endif; ?>
        </article>
        <?php endif; ?>
        <article class="nsi-card nsi-status-card">
            <?php $n = (int) ($backups[(int) $site['id']] ?? 0); ?>
            <span class="nsi-status-icon <?= $n > 0 ? 'is-ok' : 'is-warn' ?>"><i class="bi bi-archive-fill" aria-hidden="true"></i></span>
            <div class="nsi-status-body"><div class="nsi-status-label">Backups</div><div class="nsi-status-value"><?= $n > 0 ? $n . ' saved' : 'None yet' ?></div></div>
            <?php if ($has('backups')): ?><a class="nsi-status-link" href="<?= e($url('backups')) ?>"><?= $n > 0 ? 'Manage' : 'Back up now' ?><i class="bi bi-arrow-right" aria-hidden="true"></i></a><?php endif; ?>
        </article>
    </div>

    <?php if ($todo !== []): ?>
        <h2 class="nsi-h">To do</h2>
        <ul class="nsi-todo">
            <?php foreach (array_slice($todo, 0, 4) as $t): ?>
                <li class="nsi-card is-<?= e($t['level']) ?>"><span class="nsi-todo-icon"><i class="bi bi-<?= e($t['icon']) ?>" aria-hidden="true"></i></span><span class="nsi-todo-text"><strong><?= e($t['site']) ?></strong> · <?= e($t['text']) ?></span><a class="btn btn-sm btn-primary" href="<?= e($t['url']) ?>"><?= e($t['action']) ?></a></li>
            <?php endforeach; ?>
        </ul>
    <?php endif; ?>

    <h2 class="nsi-h">Quick access</h2>
    <div class="nsi-quick">
        <?php foreach ($tools as $t): ?>
            <a class="nsi-quick-tile" href="<?= e($t['url']) ?>"><span class="nsi-quick-icon"><i class="bi bi-<?= e($t['icon']) ?>" aria-hidden="true"></i></span><span><?= e($t['label']) ?></span></a>
        <?php endforeach; ?>
        <button class="nsi-quick-tile" type="button" data-bs-toggle="offcanvas" data-bs-target="#nsi-tools" aria-controls="nsi-tools"><span class="nsi-quick-icon"><i class="bi bi-grid" aria-hidden="true"></i></span><span>All tools</span></button>
    </div>

    <h2 class="nsi-h">Your plan<?= $nt['account']['plan'] !== '' ? ' · ' . e($nt['account']['plan']) : '' ?></h2>
    <?php if ($rings !== []): ?>
        <section class="nsi-rings">
            <?php foreach ($rings as $u): $pct = (int) min(100, round(100 * $u['used'] / max(1, $u['limit']))); ?>
                <div class="nsi-ringbox">
                    <div class="nsi-ring nsi-ring-<?= (int) (round($pct / 5) * 5) ?><?= $pct >= 100 ? ' is-full' : ($pct >= 80 ? ' is-high' : '') ?>" role="img" aria-label="<?= e($u['label']) ?>: <?= (int) $u['used'] ?> of <?= (int) $u['limit'] ?>"><span><?= $pct ?>%</span></div>
                    <div class="nsi-ring-label"><?= e($u['label']) ?></div>
                    <div class="nsi-ring-sub"><?= (int) $u['used'] ?> of <?= (int) $u['limit'] ?></div>
                </div>
            <?php endforeach; ?>
        </section>
    <?php else: ?>
        <section class="nsi-card"><?= $this->partial('themes/components/usage', ['rows' => $nt['account']['usage'], 'variant' => 'inline']) ?></section>
    <?php endif; ?>
<?php endif; ?>
