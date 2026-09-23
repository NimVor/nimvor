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

<?php if ($agentError !== null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
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
                <div class="card-header"><i class="bi bi-hdd-rack me-1"></i>Server</div>
                <div class="card-body">
                    <?php if ($info !== null): ?>
                        <dl class="wcp-dl">
                            <dt>Hostname</dt><dd><?= e($sys['hostname'] ?? '—') ?></dd>
                            <dt>OS</dt><dd><?= e($os['pretty_name'] ?? '—') ?>
                                <?php if (empty($os['supported'])): ?><span class="badge text-bg-warning ms-1">unsupported</span><?php endif; ?></dd>
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
                <ul class="list-group list-group-flush" data-services>
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
<script src="<?= e($this->asset('assets/js/dashboard.js')) ?>" defer></script>
