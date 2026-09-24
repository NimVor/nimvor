<?php
/** @var \WhaCP\View\View $this */
$os = $info['os'] ?? [];
$sys = $info['system'] ?? [];
$mem = $metrics['memory'] ?? [];
?>
<?php $this->start(); ?>Server<?php $this->end('title'); ?>
<?php $this->start(); ?><?= e($sys['hostname'] ?? '') ?><?php $this->end('subtitle'); ?>

<div class="row g-3">
    <div class="col-lg-6">
        <div class="card h-100">
            <div class="card-header"><i class="bi bi-info-circle me-1"></i>System</div>
            <div class="card-body">
                <dl class="wcp-dl">
                    <dt>Hostname</dt><dd><?= e($sys['hostname'] ?? '—') ?></dd>
                    <dt>Operating system</dt><dd><?= e($os['pretty_name'] ?? '—') ?>
                        <?= !empty($os['supported']) ? $this->statusBadge('ok', 'supported') : $this->statusBadge('warning', 'not officially supported') ?></dd>
                    <dt>OS family</dt><dd><?= e($os['family'] ?? '—') ?></dd>
                    <dt>Kernel</dt><dd><?= e($sys['kernel'] ?? '—') ?></dd>
                    <dt>Architecture</dt><dd><?= e($sys['architecture'] ?? '—') ?></dd>
                    <dt>CPU</dt><dd><?= e(($sys['cpu']['cores'] ?? '?') . ' × ' . ($sys['cpu']['model'] ?? 'unknown')) ?></dd>
                    <dt>Memory</dt><dd><?= e(format_bytes($mem['total_bytes'] ?? null)) ?> <span class="text-body-secondary">(<?= e(format_bytes($mem['used_bytes'] ?? null)) ?> used)</span></dd>
                    <dt>Swap</dt><dd><?= e(format_bytes($mem['swap_total_bytes'] ?? null)) ?></dd>
                    <dt>Uptime</dt><dd><?= e(format_duration((float) ($sys['uptime_seconds'] ?? 0))) ?></dd>
                    <dt>Booted</dt><dd><?= e($sys['boot_time'] ?? '—') ?></dd>
                    <dt>Timezone</dt><dd><?= e($sys['timezone'] ?? '—') ?></dd>
                    <dt>Load average</dt><dd><?= e(sprintf('%.2f / %.2f / %.2f', $metrics['load']['load1'] ?? 0, $metrics['load']['load5'] ?? 0, $metrics['load']['load15'] ?? 0)) ?></dd>
                    <dt>Firewall</dt><dd><?= e($firewall['backend'] ?? 'unknown') ?>
                        <?php if (($firewall['backend'] ?? 'none') === 'ufw'): ?>
                            <?= !empty($firewall['enabled']) ? $this->statusBadge('active', 'enabled') : $this->statusBadge('warning', 'disabled') ?>
                        <?php endif; ?></dd>
                    <dt>Agent</dt><dd><?= e($info['agent_version'] ?? '—') ?></dd>
                </dl>
            </div>
        </div>
    </div>
    <div class="col-lg-6">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-hdd me-1"></i>Disks</div>
            <div class="table-responsive">
                <table class="table table-sm align-middle mb-0 wcp-table">
                    <thead><tr><th>Mount</th><th>Type</th><th>Used</th><th class="w-25">Usage</th></tr></thead>
                    <tbody>
                    <?php foreach ($metrics['disks'] ?? [] as $d): ?>
                        <tr>
                            <td><code><?= e($d['mount_point']) ?></code><div class="small text-body-secondary"><?= e($d['device']) ?></div></td>
                            <td><?= e($d['fs_type']) ?></td>
                            <td class="small"><?= e(format_bytes($d['used_bytes'])) ?> / <?= e(format_bytes($d['total_bytes'])) ?></td>
                            <td>
                                <div class="progress" role="progressbar" aria-valuenow="<?= e($d['used_percent']) ?>" aria-valuemin="0" aria-valuemax="100">
                                    <div class="progress-bar <?= $d['used_percent'] >= 90 ? 'bg-danger' : ($d['used_percent'] >= 75 ? 'bg-warning' : '') ?>" data-width="<?= e($d['used_percent']) ?>"></div>
                                </div>
                                <div class="small text-body-secondary"><?= e($d['used_percent']) ?>%</div>
                            </td>
                        </tr>
                    <?php endforeach; ?>
                    </tbody>
                </table>
            </div>
        </div>
        <div class="card">
            <div class="card-header"><i class="bi bi-ethernet me-1"></i>Network interfaces</div>
            <ul class="list-group list-group-flush small">
                <?php foreach ($sys['interfaces'] ?? [] as $i): ?>
                    <li class="list-group-item">
                        <div class="d-flex justify-content-between">
                            <strong><?= e($i['name']) ?></strong>
                            <?= !empty($i['up']) ? $this->statusBadge('active', 'up') : $this->statusBadge('inactive', 'down') ?>
                        </div>
                        <?php foreach ($i['addresses'] as $a): ?><code class="me-2"><?= e($a) ?></code><?php endforeach; ?>
                        <?php if (!empty($i['mac'])): ?><div class="text-body-secondary">MAC <?= e($i['mac']) ?></div><?php endif; ?>
                    </li>
                <?php endforeach; ?>
            </ul>
        </div>
    </div>
</div>
<?php if ($settings !== null): ?>
<?php $canManage = $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE); ?>
<div class="card mt-3">
    <div class="card-header"><i class="bi bi-gear me-1"></i>Server settings</div>
    <div class="card-body">
        <?php if (!empty($settings['scheduled_reboot'])): ?>
            <div class="alert alert-warning d-flex align-items-center gap-2"><i class="bi bi-arrow-clockwise"></i>
                <div class="me-auto">A restart is scheduled for <?= e(substr((string) $settings['scheduled_reboot'], 0, 16)) ?> UTC.</div>
                <?php if ($canManage): ?><form method="post" action="<?= e($this->url('server.reboot_cancel')) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary">Cancel restart</button></form><?php endif; ?>
            </div>
        <?php elseif (!empty($settings['reboot_required'])): ?>
            <div class="alert alert-info small"><i class="bi bi-info-circle me-1"></i>A restart is required to finish installing updates<?= !empty($settings['reboot_required_by']) ? ' (' . e(implode(', ', (array) $settings['reboot_required_by'])) . ')' : '' ?>.</div>
        <?php endif; ?>
        <div class="row g-4">
            <div class="col-md-4">
                <form method="post" action="<?= e($this->url('server.hostname')) ?>">
                    <?= $this->csrfField() ?>
                    <label class="form-label" for="s-host">Host name</label>
                    <div class="input-group input-group-sm"><input class="form-control" id="s-host" name="hostname" value="<?= e($settings['hostname'] ?? '') ?>"<?= $canManage ? '' : ' disabled' ?>><?php if ($canManage): ?><button class="btn btn-outline-primary">Save</button><?php endif; ?></div>
                </form>
            </div>
            <div class="col-md-4">
                <form method="post" action="<?= e($this->url('server.timezone')) ?>">
                    <?= $this->csrfField() ?>
                    <label class="form-label" for="s-tz">Time zone</label>
                    <div class="input-group input-group-sm">
                        <select class="form-select" id="s-tz" name="timezone"<?= $canManage ? '' : ' disabled' ?>>
                            <?php foreach ($timezones ?: [$settings['timezone'] ?? 'UTC'] as $tz): ?><option<?= $tz === ($settings['timezone'] ?? '') ? ' selected' : '' ?>><?= e($tz) ?></option><?php endforeach; ?>
                        </select>
                        <?php if ($canManage): ?><button class="btn btn-outline-primary">Save</button><?php endif; ?>
                    </div>
                    <div class="form-text">Clock sync: <?= !empty($settings['ntp_synchronized']) ? 'synchronised' : (!empty($settings['ntp_enabled']) ? 'enabled, not yet synchronised' : 'off') ?></div>
                </form>
            </div>
            <?php if ($canManage && empty($settings['scheduled_reboot'])): ?>
                <div class="col-md-4">
                    <form method="post" action="<?= e($this->url('server.reboot')) ?>">
                        <?= $this->csrfField() ?>
                        <label class="form-label" for="s-reboot">Restart the server</label>
                        <div class="input-group input-group-sm">
                            <select class="form-select" name="minutes" aria-label="When"><option value="1">in 1 minute</option><option value="5">in 5 minutes</option><option value="30">in 30 minutes</option><option value="60">in 1 hour</option></select>
                            <input class="form-control" id="s-reboot" name="confirm" placeholder="type <?= e($settings['hostname'] ?? '') ?>" autocomplete="off" required>
                            <button class="btn btn-outline-danger">Restart</button>
                        </div>
                        <div class="form-text">Logged-in users are warned; you can cancel until then.</div>
                    </form>
                </div>
            <?php endif; ?>
        </div>
    </div>
</div>
<?php endif; ?>
