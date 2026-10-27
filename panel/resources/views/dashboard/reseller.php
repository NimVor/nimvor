<?php
/**
 * A reseller's home: their customers, websites and plan use.
 *
 * @var \WhaCP\View\View $this
 * @var list<array<string, mixed>> $customers
 * @var list<array<string, mixed>> $sites
 * @var list<array{key: string, label: string, used: int, limit: int}> $usage
 * @var list<array<string, mixed>> $recent
 */
use WhaCP\Security\Permissions as P;

$who = !empty($user) ? ($user['display_name'] !== '' ? $user['display_name'] : $user['username']) : '';
$hour = (int) date('G');
$greet = $hour < 12 ? 'Good morning' : ($hour < 18 ? 'Good afternoon' : 'Good evening');
$active = count(array_filter($customers, static fn ($c): bool => empty($c['suspended'])));
$suspended = count($customers) - $active;
$https = count(array_filter($sites, static fn ($s): bool => !empty($s['https'])));
$quick = [];
foreach ([
    ['New account', 'person-plus', 'accounts.create', P::USERS_MANAGE],
    ['Create website', 'plus-circle', 'sites.create', P::WEBSITES_MANAGE],
    ['Customers', 'people', 'users', P::USERS_VIEW],
    ['Plans', 'card-checklist', 'plans', P::PLANS_MANAGE],
    ['Databases', 'database', 'databases', P::DATABASES_VIEW],
    ['WordPress', 'wordpress', 'wordpress', P::WEBSITES_VIEW],
    ['Backups', 'cloud-arrow-up', 'backups', P::BACKUPS_VIEW],
] as [$label, $icon, $route, $perm]) {
    if ($this->hasRoute($route) && $this->can($perm) && $this->routeAllowed($route)) {
        $quick[] = [$label, $icon, $this->url($route)];
    }
}
$fmt = static fn (string $key, int $n): string => in_array($key, ['disk', 'bandwidth'], true) ? format_bytes($n * 1048576) : (string) $n;
?>
<?php $this->start(); ?>Dashboard<?php $this->end('title'); ?>
<?php $this->start(); ?>Your customers, websites and plan<?php $this->end('subtitle'); ?>

<section class="wcp-hero" aria-label="Welcome">
    <div class="d-flex flex-wrap align-items-start gap-3">
        <div class="me-auto">
            <h2 class="h4"><?= e($greet) ?><?= $who !== '' ? ', ' . e($who) : '' ?></h2>
            <div class="wcp-hero-sub">Reseller account · <?= count($customers) ?> customer<?= count($customers) === 1 ? '' : 's' ?> · <?= count($sites) ?> website<?= count($sites) === 1 ? '' : 's' ?></div>
        </div>
        <?php if ($suspended > 0): ?><span class="wcp-hero-chip"><i class="bi bi-pause-circle" aria-hidden="true"></i><?= $suspended ?> suspended</span><?php endif; ?>
    </div>
    <?php if ($quick !== []): ?>
    <nav class="wcp-quick" aria-label="Quick actions">
        <?php foreach ($quick as [$label, $icon, $url]): ?><a href="<?= e($url) ?>"><i class="bi bi-<?= e($icon) ?>" aria-hidden="true"></i><?= e($label) ?></a><?php endforeach; ?>
    </nav>
    <?php endif; ?>
</section>

<div class="row g-3 mb-3">
    <?php foreach ([
        ['Customers', count($customers), 'people', 1, $active . ' active'],
        ['Websites', count($sites), 'globe2', 0, $https . ' with HTTPS'],
        ['Suspended', $suspended, 'pause-circle', $suspended > 0 ? 3 : 5, $suspended > 0 ? 'need attention' : 'none'],
        ['Notifications', count(array_filter($recent, static fn ($n): bool => empty($n['is_read']))), 'bell', 2, 'unread of the latest'],
    ] as [$label, $value, $icon, $tone, $detail]): ?>
        <div class="col-6 col-xl-3">
            <div class="card h-100"><div class="card-body wcp-kstat">
                <span class="wcp-tool-icon wcp-tone-<?= (int) $tone ?>"><i class="bi bi-<?= e($icon) ?>" aria-hidden="true"></i></span>
                <div><div class="small text-body-secondary"><?= e($label) ?></div><div class="wcp-stat-value"><?= (int) $value ?></div><div class="small text-body-secondary"><?= e($detail) ?></div></div>
            </div></div>
        </div>
    <?php endforeach; ?>
</div>

<div class="row g-3 mb-3">
    <div class="col-lg-5">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-pie-chart me-1" aria-hidden="true"></i>Plan use</div>
            <div class="card-body">
                <?php $limited = array_values(array_filter($usage, static fn ($u): bool => $u['limit'] > 0)); $open = array_values(array_filter($usage, static fn ($u): bool => $u['limit'] <= 0)); ?>
                <?php if ($usage === []): ?><div class="text-body-secondary small">No plan is assigned to your account.</div><?php endif; ?>
                <?php foreach ($limited as $u): $pct = min(100, (int) round($u['used'] * 100 / $u['limit'])); ?>
                    <div class="wcp-usage">
                        <div class="d-flex justify-content-between small"><span><?= e(preg_replace('/ \(MB\)$/', '', $u['label'])) ?></span><span class="text-body-secondary"><?= e($fmt($u['key'], $u['used'])) ?> / <?= e($fmt($u['key'], $u['limit'])) ?></span></div>
                        <div class="progress mt-1" role="progressbar" aria-label="<?= e($u['label']) ?>" aria-valuenow="<?= $pct ?>" aria-valuemin="0" aria-valuemax="100"><div class="progress-bar<?= $pct >= 90 ? ' bg-danger' : ($pct >= 75 ? ' bg-warning' : '') ?>" data-width="<?= $pct ?>"></div></div>
                    </div>
                <?php endforeach; ?>
                <?php if ($open !== []): ?>
                    <div class="small text-body-secondary mt-3 mb-1">Unlimited in your plan</div>
                    <dl class="wcp-dl wcp-dl-compact small mb-0 wcp-usage-open">
                        <?php foreach ($open as $u): ?><dt class="fw-normal"><?= e(preg_replace('/ \(MB\)$/', '', $u['label'])) ?></dt><dd class="text-end mb-0"><?= e($fmt($u['key'], $u['used'])) ?> used</dd><?php endforeach; ?>
                    </dl>
                <?php endif; ?>
            </div>
        </div>
    </div>
    <div class="col-lg-7">
        <div class="card h-100">
            <div class="card-header d-flex justify-content-between align-items-center">
                <span><i class="bi bi-people me-1" aria-hidden="true"></i>Customers</span>
                <?php if ($this->hasRoute('users')): ?><a class="small" href="<?= e($this->url('users')) ?>">View all</a><?php endif; ?>
            </div>
            <?php if ($customers === []): ?>
                <div class="card-body"><div class="wcp-empty-state"><i class="bi bi-person-plus" aria-hidden="true"></i><div class="fw-medium">No customers yet</div>
                    <?php if ($this->hasRoute('accounts.create') && $this->can(P::USERS_MANAGE)): ?><a class="btn btn-sm btn-primary mt-2" href="<?= e($this->url('accounts.create')) ?>">Create a hosting account</a><?php endif; ?></div></div>
            <?php else: ?>
                <div class="table-responsive"><table class="table align-middle mb-0 small">
                    <thead><tr><th>Customer</th><th>Plan</th><th>Websites</th><th>Status</th></tr></thead>
                    <tbody>
                    <?php foreach (array_slice($customers, 0, 8) as $c): $n = count(array_filter($sites, static fn ($s): bool => (int) ($s['owner_id'] ?? 0) === (int) $c['id'])); ?>
                        <tr>
                            <td><div class="d-flex align-items-center gap-2"><span class="wcp-avatar wcp-avatar-sm" aria-hidden="true"><?= e(mb_strtoupper(mb_substr((string) $c['username'], 0, 1))) ?></span><div><div class="fw-medium"><?= e($c['username']) ?></div><div class="text-body-secondary"><?= e($c['email']) ?></div></div></div></td>
                            <td><?= e($c['plan_name'] ?? '—') ?></td>
                            <td><?= $n ?></td>
                            <td><?= !empty($c['suspended']) ? '<span class="wcp-state is-warn">Suspended</span>' : '<span class="wcp-state is-ok">Active</span>' ?></td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table></div>
            <?php endif; ?>
        </div>
    </div>
</div>

<div class="row g-3">
    <div class="col-lg-7">
        <div class="card h-100">
            <div class="card-header d-flex justify-content-between align-items-center">
                <span><i class="bi bi-globe2 me-1" aria-hidden="true"></i>Websites</span>
                <a class="small" href="<?= e($this->url('websites')) ?>">View all</a>
            </div>
            <?php if ($sites === []): ?>
                <div class="card-body text-body-secondary small">No websites yet.</div>
            <?php else: ?>
                <ul class="list-group list-group-flush small">
                    <?php foreach (array_slice($sites, 0, 8) as $s): ?>
                        <li class="list-group-item d-flex align-items-center gap-2">
                            <i class="bi bi-<?= !empty($s['https']) ? 'lock-fill text-success' : 'unlock text-body-secondary' ?>" aria-hidden="true"></i>
                            <a class="fw-medium me-auto text-decoration-none" href="<?= e($this->url('sites.show', ['id' => (int) $s['id']])) ?>"><?= e($s['domain']) ?></a>
                            <span class="text-body-secondary"><?= e((string) ($s['owner_name'] ?? '')) ?></span>
                            <?= ($s['status'] ?? '') === 'active' ? '<span class="wcp-state is-ok">Active</span>' : '<span class="wcp-state is-off">' . e(ucfirst((string) ($s['status'] ?? ''))) . '</span>' ?>
                        </li>
                    <?php endforeach; ?>
                </ul>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-lg-5">
        <div class="card h-100">
            <div class="card-header d-flex justify-content-between align-items-center">
                <span><i class="bi bi-bell me-1" aria-hidden="true"></i>Recent notifications</span>
                <?php if ($this->hasRoute('notifications')): ?><a class="small" href="<?= e($this->url('notifications')) ?>">All</a><?php endif; ?>
            </div>
            <ul class="list-group list-group-flush small">
                <?php if ($recent === []): ?><li class="list-group-item text-body-secondary">Nothing yet.</li><?php endif; ?>
                <?php foreach ($recent as $n): ?>
                    <li class="list-group-item d-flex justify-content-between gap-2<?= empty($n['is_read']) ? ' fw-medium' : '' ?>">
                        <span class="wcp-wrap"><?= e($n['title']) ?><?php if (($n['domain'] ?? null) !== null): ?> <span class="text-body-secondary fw-normal">· <?= e($n['domain']) ?></span><?php endif; ?></span>
                        <span class="text-body-secondary text-nowrap fw-normal"><?= e(time_ago((string) $n['created_at'])) ?></span>
                    </li>
                <?php endforeach; ?>
            </ul>
        </div>
    </div>
</div>
