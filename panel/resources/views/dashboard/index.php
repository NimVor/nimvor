<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed>|null $info */
/** @var array<string, mixed>|null $summary */
/** @var \WhaCP\Agent\AgentException|null $agentError */
$os = $info['os'] ?? [];
$sys = $info['system'] ?? [];
?>
<?php $this->start(); ?>Dashboard<?php $this->end('title'); ?>
<?php $this->start(); ?><?= e($sys['hostname'] ?? 'Server overview') ?><?php $this->end('subtitle'); ?>

<?php
$hour = (int) date('G');
$greet = $hour < 12 ? 'Good morning' : ($hour < 18 ? 'Good afternoon' : 'Good evening');
$who = !empty($user) ? ($user['display_name'] !== '' ? $user['display_name'] : $user['username']) : '';
$P = \WhaCP\Security\Permissions::class;
$quick = [];
foreach ([
    ['Create website', 'plus-circle', 'sites.create', $P::WEBSITES_MANAGE],
    ['New account', 'person-plus', 'accounts.create', $P::USERS_MANAGE],
    ['Databases', 'database-add', 'databases', $P::DATABASES_VIEW],
    ['WordPress', 'wordpress', 'wordpress', $P::WEBSITES_VIEW],
    ['Backups', 'cloud-arrow-up', 'backups', $P::BACKUPS_VIEW],
    ['Security', 'shield-check', 'security', $P::SECURITY_VIEW],
    ['Packages', 'box-seam', 'packages', $P::PACKAGES_MANAGE],
    ['Settings', 'sliders', 'settings', $P::SETTINGS_VIEW],
] as [$label, $icon, $route, $perm]) {
    if ($this->hasRoute($route) && $this->can($perm)) {
        $quick[] = [$label, $icon, $this->url($route)];
    }
}
?>
<section class="wcp-hero" aria-label="Welcome">
    <div class="d-flex flex-wrap align-items-start gap-3">
        <div class="me-auto">
            <h2 class="h4"><?= e($greet) ?><?= $who !== '' ? ', ' . e($who) : '' ?></h2>
            <div class="wcp-hero-sub"><?= e($sys['hostname'] ?? 'Your server') ?><?= isset($os['pretty_name']) ? ' · ' . e($os['pretty_name']) : '' ?><?= isset($sys['uptime_seconds']) ? ' · up ' . e(format_duration((float) $sys['uptime_seconds'])) : '' ?></div>
        </div>
        <div class="d-flex flex-wrap gap-2">
            <?php if ($summary !== null): ?><a class="wcp-hero-chip" href="<?= e($this->url('websites')) ?>"><i class="bi bi-globe2" aria-hidden="true"></i><?= (int) $summary['total'] ?> websites</a><?php endif; ?>
            <?php if (!empty($alerts)): ?><a class="wcp-hero-chip" href="<?= e($this->url('monitoring')) ?>"><i class="bi bi-bell-fill" aria-hidden="true"></i><?= count($alerts) ?> alert<?= count($alerts) === 1 ? '' : 's' ?></a>
            <?php else: ?><span class="wcp-hero-chip"><i class="bi bi-check-circle-fill" aria-hidden="true"></i>All systems normal</span><?php endif; ?>
        </div>
    </div>
    <?php if ($quick !== []): ?>
    <nav class="wcp-quick" aria-label="Quick actions">
        <?php foreach ($quick as [$label, $icon, $url]): ?><a href="<?= e($url) ?>"><i class="bi bi-<?= e($icon) ?>" aria-hidden="true"></i><?= e($label) ?></a><?php endforeach; ?>
    </nav>
    <?php endif; ?>
</section>

<?php if (!empty($alerts)): ?>
    <div class="alert alert-<?= in_array('critical', array_column($alerts, 'severity'), true) ? 'danger' : 'warning' ?> d-flex align-items-start gap-2" role="alert">
        <i class="bi bi-bell-fill mt-1"></i>
        <div class="me-auto"><strong><?= count($alerts) ?> active alert<?= count($alerts) === 1 ? '' : 's' ?>:</strong>
            <?= e(implode(' · ', array_slice(array_column($alerts, 'message'), 0, 3))) ?><?= count($alerts) > 3 ? ' …' : '' ?></div>
        <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('monitoring')) ?>">Details</a>
    </div>
<?php endif; ?>

<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
<?php endif; ?>

<?php if (!empty($failures)): ?>
    <div class="card border-danger mb-3">
        <div class="card-header d-flex justify-content-between align-items-center">
            <span><i class="bi bi-x-octagon text-danger me-1"></i>Unread failures</span>
            <a class="small" href="<?= e($this->url('notifications', [], ['severity' => 'error'])) ?>">All notifications</a>
        </div>
        <ul class="list-group list-group-flush">
            <?php foreach ($failures as $f): ?>
                <li class="list-group-item d-flex justify-content-between gap-2">
                    <span class="wcp-wrap"><?= e($f['title']) ?><?php if ($f['domain'] !== null): ?> <span class="text-body-secondary small">· <?= e($f['domain']) ?></span><?php endif; ?></span>
                    <span class="small text-body-secondary text-nowrap"><?= e(gmdate('M j H:i', (int) strtotime((string) $f['created_at']))) ?> UTC</span>
                </li>
            <?php endforeach; ?>
        </ul>
    </div>
<?php endif; ?>

<div id="wcp-dashboard" data-live-url="<?= e($this->url('dashboard.live')) ?>" data-refresh="<?= (int) $refreshSeconds ?>">
    <div class="row g-3 mb-3">
        <?php foreach ([
            ['cpu', 'CPU', 'cpu', 'cpu'],
            ['mem', 'Memory', 'memory', 'mem'],
            ['disk', 'Disk (/)', 'hdd', 'disk'],
            ['load', 'Load average', 'speedometer', 'load1'],
        ] as [$key, $label, $icon, $series]): ?>
            <div class="col-6 col-xl-3">
                <div class="card wcp-metric h-100" data-metric="<?= e($key) ?>">
                    <div class="card-body">
                        <div class="d-flex justify-content-between align-items-start">
                            <div>
                                <div class="wcp-metric-label"><i class="bi bi-<?= e($icon) ?> me-1"></i><?= e($label) ?></div>
                                <div class="wcp-metric-value" data-value>—</div>
                                <div class="wcp-metric-detail" data-detail>&nbsp;</div>
                            </div>
                        </div>
                        <div class="wcp-spark" data-spark="<?= e($series) ?>" aria-hidden="true"></div>
                    </div>
                </div>
            </div>
        <?php endforeach; ?>
    </div>

    <div class="row g-3 mb-3">
        <div class="col-lg-4">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center"><span><i class="bi bi-hdd-rack me-1"></i>Server</span><?= $this->partial('partials/server_tools') ?></div>
                <div class="card-body">
                    <?php if ($info !== null): ?>
                        <dl class="wcp-dl">
                            <dt>Hostname</dt><dd><?= e($sys['hostname'] ?? '—') ?></dd>
                            <dt>OS</dt><dd><?= e($os['pretty_name'] ?? '—') ?>
                                <?php if (empty($os['supported'])): ?><span class="badge text-bg-warning ms-1">unsupported</span><?php elseif (!empty($os['preview'])): ?><span class="badge text-bg-info ms-1" title="Tested, but not yet recommended for production servers">preview</span><?php endif; ?></dd>
                            <dt>Kernel</dt><dd><?= e($sys['kernel'] ?? '—') ?></dd>
                            <dt>CPU</dt><dd><?= e(($sys['cpu']['cores'] ?? '?') . ' × ' . ($sys['cpu']['model'] ?? 'unknown')) ?></dd>
                            <dt>Uptime</dt><dd data-uptime><?= e(format_duration((float) ($sys['uptime_seconds'] ?? 0))) ?></dd>
                            <dt>Timezone</dt><dd><?= e($sys['timezone'] ?? '—') ?></dd>
                            <dt>Agent</dt><dd><?= e($info['agent_version'] ?? '—') ?></dd>
                        </dl>
                    <?php else: ?>
                        <p class="text-body-secondary mb-0">Server information is unavailable while the agent is unreachable.</p>
                    <?php endif; ?>
                </div>
            </div>
        </div>
        <div class="col-lg-4">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span><i class="bi bi-cpu me-1"></i>Services</span>
                    <?php if ($this->can(\WhaCP\Security\Permissions::SERVICES_VIEW)): ?>
                        <a class="small" href="<?= e($this->url('services')) ?>">All services</a>
                    <?php endif; ?>
                </div>
                <ul class="list-group list-group-flush" data-services<?php if ($this->can(\WhaCP\Security\Permissions::SERVICES_MANAGE)): ?> data-services-control="<?= e($this->url('services.control')) ?>"<?php endif; ?>>
                    <li class="list-group-item text-body-secondary"><span class="placeholder col-8"></span></li>
                    <li class="list-group-item text-body-secondary"><span class="placeholder col-6"></span></li>
                    <li class="list-group-item text-body-secondary"><span class="placeholder col-7"></span></li>
                </ul>
            </div>
        </div>
        <div class="col-lg-4">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span><i class="bi bi-globe2 me-1"></i>Websites</span>
                    <a class="small" href="<?= e($this->url('websites')) ?>">View all</a>
                </div>
                <div class="card-body">
                    <?php if ($summary !== null): ?>
                        <div class="row g-2 text-center mb-3">
                            <div class="col-4"><div class="wcp-kpi"><?= (int) $summary['total'] ?></div><div class="wcp-kpi-label">Total</div></div>
                            <div class="col-4"><div class="wcp-kpi"><?= (int) $summary['enabled'] ?></div><div class="wcp-kpi-label">Enabled</div></div>
                            <div class="col-4"><div class="wcp-kpi"><?= (int) $summary['https'] ?></div><div class="wcp-kpi-label">HTTPS</div></div>
                        </div>
                        <?php if ($summary['expiring'] > 0): ?>
                            <div class="alert alert-warning py-2 small"><i class="bi bi-exclamation-triangle me-1"></i><?= (int) $summary['expiring'] ?> certificate(s) expiring soon or expired.</div>
                        <?php endif; ?>
                        <div class="small text-body-secondary mb-1">PHP versions in use</div>
                        <?php if ($summary['php_versions'] === []): ?>
                            <div class="small">No PHP websites detected.</div>
                        <?php else: ?>
                            <?php foreach ($summary['php_versions'] as $v => $n): ?>
                                <span class="badge text-bg-light border me-1">PHP <?= e($v) ?> · <?= (int) $n ?></span>
                            <?php endforeach; ?>
                        <?php endif; ?>
                        <div class="small text-body-secondary mt-3">Discovered <?= e(time_ago($summary['discovered_at'])) ?></div>
                    <?php else: ?>
                        <p class="text-body-secondary mb-0">Website discovery is unavailable.</p>
                    <?php endif; ?>
                </div>
            </div>
        </div>
    </div>

    <div class="row g-3">
        <div class="col-lg-8">
            <div class="card h-100">
                <div class="card-header"><i class="bi bi-diagram-3 me-1"></i>Network</div>
                <div class="card-body">
                    <div class="d-flex gap-4 mb-2">
                        <div><span class="wcp-dot wcp-dot-rx"></span>In <strong data-net-rx>—</strong></div>
                        <div><span class="wcp-dot wcp-dot-tx"></span>Out <strong data-net-tx>—</strong></div>
                    </div>
                    <div class="wcp-spark wcp-spark-lg" data-spark="net" aria-hidden="true"></div>
                </div>
            </div>
        </div>
        <div class="col-lg-4">
            <div class="card h-100">
                <div class="card-header d-flex justify-content-between align-items-center">
                    <span><i class="bi bi-list-check me-1"></i>Recent activity</span>
                    <?php if ($this->can(\WhaCP\Security\Permissions::AUDIT_VIEW)): ?>
                        <a class="small" href="<?= e($this->url('audit')) ?>">Audit log</a>
                    <?php endif; ?>
                </div>
                <ul class="list-group list-group-flush small">
                    <?php if ($recentAudit === []): ?>
                        <li class="list-group-item text-body-secondary">No recent activity to show.</li>
                    <?php endif; ?>
                    <?php foreach ($recentAudit as $row): ?>
                        <li class="list-group-item d-flex justify-content-between gap-2">
                            <span><code><?= e($row['action']) ?></code> <span class="text-body-secondary"><?= e($row['username']) ?></span></span>
                            <span class="text-nowrap"><?= $this->statusBadge((string) $row['result']) ?> <span class="text-body-secondary"><?= e(time_ago((string) $row['created_at'])) ?></span></span>
                        </li>
                    <?php endforeach; ?>
                </ul>
            </div>
        </div>
    </div>
</div>
<div class="small text-body-secondary mt-3" data-live-status>Waiting for live data…</div>
<?php $toolGroups = \WhaCP\View\Navigation::visible($this); unset($toolGroups['Overview']); ?>
<?php if ($toolGroups !== []): ?>
<section aria-labelledby="tools-title">
    <div class="wcp-tools-head"><h2 id="tools-title">Tools &amp; settings</h2><span class="small text-body-secondary">Everything you can manage, in one place · press <kbd>Ctrl</kbd> <kbd>K</kbd> to search</span></div>
    <?php $tone = 0; foreach ($toolGroups as $group => $items): ?>
        <div class="wcp-tools-group"><?= e($group) ?></div>
        <div class="wcp-tools">
            <?php
            // Security's and SSL's pages each get their own card (as in the sidebar).
            $cards = [];
            foreach ($items as $i) {
                $details = $group === 'Security' ? \WhaCP\View\Navigation::SECURITY_CARDS : ($i['route'] === 'ssl' ? \WhaCP\View\Navigation::SSL_CARDS : null);
                if ($details === null || $i['children'] === []) {
                    $cards[] = $i;
                    continue;
                }
                foreach ($i['children'] as $c) {
                    [$icon, $desc] = $details[$c['route']] ?? [$i['icon'], $i['description']];
                    $cards[] = ['label' => $c['label'] === 'Overview' ? 'Security overview' : $c['label'], 'url' => $c['url'], 'icon' => $icon, 'description' => $desc];
                }
            }
            $items = $cards;
            ?>
            <?php foreach ($items as $i): ?>
                <a class="wcp-tool-card" href="<?= e($i['url']) ?>">
                    <span class="wcp-tool-icon wcp-tone-<?= $tone % 6 ?>"><i class="bi bi-<?= e($i['icon']) ?>" aria-hidden="true"></i></span>
                    <span class="min-w-0"><span class="d-block fw-semibold"><?= e($i['label']) ?></span><span class="d-block small text-body-secondary"><?= e($i['description']) ?></span></span>
                </a>
            <?php endforeach; ?>
        </div>
    <?php $tone++; endforeach; ?>
</section>
<?php endif; ?>

<script src="<?= e($this->asset('assets/js/dashboard.js')) ?>" defer></script>
