<?php
/** @var \WhaCP\View\View $this */
$os = $info['os'] ?? [];
$sys = $info['system'] ?? [];
$mem = $metrics['memory'] ?? [];
?>
<?php $this->start(); ?>Server<?php $this->end('title'); ?>
<?php $this->start(); ?><?= e($sys['hostname'] ?? '') ?><?php $this->end('subtitle'); ?>

<?php
$canManage = $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE);
$isAdmin = ($this->shared('user')['role_name'] ?? '') === 'admin';
$graphical = (array) ($graphical ?? []);
$gOn = !empty($graphical['active']) || ($graphical['default_target'] ?? '') === 'graphical.target';
?>
<div class="wcp-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#system" aria-controls="system" aria-selected="true"><i class="bi bi-info-circle me-1"></i>System</button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#disks" aria-controls="disks" aria-selected="false"><i class="bi bi-hdd me-1"></i>Disks <span class="badge text-bg-light border"><?= count($metrics['disks'] ?? []) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#interfaces" aria-controls="interfaces" aria-selected="false"><i class="bi bi-ethernet me-1"></i>Network interfaces <span class="badge text-bg-light border"><?= count($sys['interfaces'] ?? []) ?></span></button>
    <?php if ($settings !== null): ?><button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#settings" aria-controls="settings" aria-selected="false"><i class="bi bi-gear me-1"></i>Server settings<?= !empty($settings['scheduled_reboot']) || !empty($settings['reboot_required']) ? ' <span class="badge text-bg-warning">restart</span>' : '' ?></button><?php endif; ?>
</div>
<div class="tab-content">
<div class="tab-pane fade show active" id="system" role="tabpanel">
<div class="card">
    <div class="card-header"><i class="bi bi-info-circle me-1"></i>System</div>
    <div class="card-body">
        <dl class="wcp-dl">
            <dt>Hostname</dt><dd><?= e($sys['hostname'] ?? '—') ?></dd>
            <dt>Operating system</dt><dd><?= e($os['pretty_name'] ?? '—') ?>
                <?= empty($os['supported']) ? $this->statusBadge('warning', 'not officially supported') : (!empty($os['preview']) ? '<span class="badge text-bg-info" title="Tested, but not yet recommended for production servers">preview</span>' : $this->statusBadge('ok', 'supported')) ?></dd>
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
<div class="tab-pane fade" id="disks" role="tabpanel">
<div class="card">
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
</div>
<div class="tab-pane fade" id="interfaces" role="tabpanel">
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
<?php if ($settings !== null): ?>
<div class="tab-pane fade" id="settings" role="tabpanel">
    <?php if (!empty($settings['scheduled_reboot'])): ?>
        <div class="alert alert-warning d-flex align-items-center gap-2"><i class="bi bi-arrow-clockwise"></i>
            <div class="me-auto">A restart is scheduled for <?= e(substr((string) $settings['scheduled_reboot'], 0, 16)) ?> UTC.</div>
            <?php if ($canManage): ?><form method="post" action="<?= e($this->url('server.reboot_cancel')) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary">Cancel restart</button></form><?php endif; ?>
        </div>
    <?php elseif (!empty($settings['reboot_required'])): ?>
        <div class="alert alert-info small"><i class="bi bi-info-circle me-1"></i>A restart is required to finish installing updates<?= !empty($settings['reboot_required_by']) ? ' (' . e(implode(', ', (array) $settings['reboot_required_by'])) . ')' : '' ?>.</div>
    <?php endif; ?>
    <div class="wcp-panel">
        <div class="wcp-dtable table-responsive">
        <table class="table align-middle mb-0 wcp-settings-table">
            <thead><tr><th>Setting</th><th>Current</th><th class="text-end"></th></tr></thead>
            <tbody>
            <tr>
                <td class="fw-semibold"><i class="bi bi-pc-display me-1"></i>Host name</td>
                <td class="font-monospace"><?= e((string) (($settings['hostname'] ?? '') !== '' ? $settings['hostname'] : ($sys['hostname'] ?? '—'))) ?></td>
                <td class="text-end"><?php if ($canManage): ?><button type="button" class="btn btn-sm btn-outline-primary" data-bs-toggle="modal" data-bs-target="#set-hostname">Change</button><?php endif; ?></td>
            </tr>
            <tr>
                <td class="fw-semibold"><i class="bi bi-clock me-1"></i>Time zone</td>
                <td><?= e((string) ($settings['timezone'] ?? '—')) ?> <span class="small wcp-muted">· clock sync: <?= !empty($settings['ntp_synchronized']) ? 'synchronised' : (!empty($settings['ntp_enabled']) ? 'enabled, not yet synchronised' : 'off') ?></span></td>
                <td class="text-end"><?php if ($canManage): ?><button type="button" class="btn btn-sm btn-outline-primary" data-bs-toggle="modal" data-bs-target="#set-timezone">Change</button><?php endif; ?></td>
            </tr>
            <?php if ($canManage && $isAdmin): ?>
            <tr>
                <td class="fw-semibold"><i class="bi bi-key me-1"></i>Root password</td>
                <td class="small wcp-muted">Not stored by WhaCP</td>
                <td class="text-end"><button type="button" class="btn btn-sm btn-outline-danger" data-bs-toggle="modal" data-bs-target="#root-password">Change</button></td>
            </tr>
            <?php endif; ?>
            <?php if (!empty($graphical['display_manager'])): ?>
            <tr>
                <td class="fw-semibold"><i class="bi bi-display me-1"></i>Graphical login screen</td>
                <td><span class="wcp-state <?= $gOn ? 'is-warn' : 'is-ok' ?>"><?= $gOn ? 'on' : 'off' ?></span> <span class="small wcp-muted"><?= e((string) $graphical['display_manager']) ?><?= !empty($graphical['active']) ? ', running' : '' ?> · boots to <?= e((string) ($graphical['default_target'] ?? '?')) ?></span></td>
                <td class="text-end"><?php if ($canManage): ?><button type="button" class="btn btn-sm btn-outline-primary" data-bs-toggle="modal" data-bs-target="#graphical-login"><?= $gOn ? 'Turn off…' : 'Turn on…' ?></button><?php endif; ?></td>
            </tr>
            <?php endif; ?>
            <tr>
                <td class="fw-semibold"><i class="bi bi-arrow-clockwise me-1"></i>Restart the server</td>
                <td class="small"><?= !empty($settings['scheduled_reboot']) ? 'Scheduled for ' . e(substr((string) $settings['scheduled_reboot'], 0, 16)) . ' UTC' : (!empty($settings['reboot_required']) ? '<span class="wcp-state is-warn">Required by updates</span>' : '<span class="wcp-muted">Up ' . e(format_duration((float) ($sys['uptime_seconds'] ?? 0))) . '</span>') ?></td>
                <td class="text-end"><?php if ($canManage && empty($settings['scheduled_reboot'])): ?><button type="button" class="btn btn-sm btn-outline-danger" data-bs-toggle="modal" data-bs-target="#server-restart">Restart…</button><?php endif; ?></td>
            </tr>
            </tbody>
        </table>
        </div>
    </div>
</div>
<?php endif; ?>
</div>

<?php if ($settings !== null && $canManage): ?>
<?= $this->modalStart('set-hostname', 'Host name', false, '') ?>
    <form method="post" action="<?= e($this->url('server.hostname')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?>
        <label class="form-label" for="s-host">Host name</label>
        <input class="form-control" id="s-host" name="hostname" value="<?= e((string) (($settings['hostname'] ?? '') !== '' ? $settings['hostname'] : ($sys['hostname'] ?? ''))) ?>" required>
        <div class="form-text">A fully qualified name such as server1.example.com. Point its DNS A record at this server before issuing its certificate. WhaCP updates <code>/etc/hosts</code> and, if mail is set up, the mail server for you. After a rename, <a href="<?= e($this->url('server.hostname_references')) ?>">find the old name</a> left in DNS zones and configs.</div>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save</button></div>
    </form>
<?= $this->modalEnd() ?>
<?= $this->modalStart('set-timezone', 'Time zone', false, '') ?>
    <form method="post" action="<?= e($this->url('server.timezone')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?>
        <label class="form-label" for="s-tz">Time zone</label>
        <select class="form-select" id="s-tz" name="timezone">
            <?php foreach ($timezones ?: [$settings['timezone'] ?? 'UTC'] as $tz): ?><option<?= $tz === ($settings['timezone'] ?? '') ? ' selected' : '' ?>><?= e($tz) ?></option><?php endforeach; ?>
        </select>
        <div class="form-text">Clock sync: <?= !empty($settings['ntp_synchronized']) ? 'synchronised' : (!empty($settings['ntp_enabled']) ? 'enabled, not yet synchronised' : 'off') ?>.</div>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Save</button></div>
    </form>
<?= $this->modalEnd() ?>
<?php if ($isAdmin): ?>
<?= $this->modalStart('root-password', 'Root password', false, '') ?>
    <form method="post" action="<?= e($this->url('server.root_password')) ?>" autocomplete="off" data-wcp-busy data-wcp-confirm="Change the root password of this server? Keep the new one somewhere safe: WhaCP does not store it.">
        <?= $this->csrfField() ?>
        <label class="form-label" for="s-root1">New password</label>
        <input class="form-control mb-2" type="password" id="s-root1" name="root_password" minlength="14" maxlength="512" autocomplete="new-password" required>
        <label class="form-label" for="s-root2">Repeat it</label>
        <input class="form-control mb-2" type="password" id="s-root2" name="root_password_confirmation" minlength="14" maxlength="512" autocomplete="new-password" required>
        <label class="form-label" for="s-root3">Your panel password</label>
        <input class="form-control" type="password" id="s-root3" name="current_password" autocomplete="current-password" required>
        <div class="form-text">At least 14 characters with three of: lower case, upper case, digits, symbols. It is never logged or stored by WhaCP; SSH may still refuse root password logins by its own settings.</div>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-danger"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Change root password</button></div>
    </form>
<?= $this->modalEnd() ?>
<?php endif; ?>
<?php if (!empty($graphical['display_manager'])): ?>
<?= $this->modalStart('graphical-login', 'Graphical login screen', false, '') ?>
    <p class="mb-2">Now: <span class="wcp-state <?= $gOn ? 'is-warn' : 'is-ok' ?>"><?= $gOn ? 'on' : 'off' ?></span> <span class="small wcp-muted"><?= e((string) $graphical['display_manager']) ?><?= !empty($graphical['active']) ? ', running' : '' ?> · boots to <?= e((string) ($graphical['default_target'] ?? '?')) ?></span></p>
    <p class="small wcp-muted">A server does not need a desktop login screen: turned off, GNOME Shell and its helpers no longer run (often 250–400 MB of memory). The server then boots to the text console; anyone signed in on the screen is signed out. SSH and the panel are not affected. Nothing is uninstalled.</p>
    <form method="post" action="<?= e($this->url('server.graphical')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?><input type="hidden" name="enabled" value="<?= $gOn ? '0' : '1' ?>">
        <div class="d-flex justify-content-end gap-2"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $gOn ? 'Turn off' : 'Turn on' ?></button></div>
    </form>
<?= $this->modalEnd() ?>
<?php endif; ?>
<?php if (empty($settings['scheduled_reboot'])): ?>
<?= $this->modalStart('server-restart', 'Restart the server', false, '') ?>
    <form method="post" action="<?= e($this->url('server.reboot')) ?>" data-wcp-busy>
        <?= $this->csrfField() ?>
        <label class="form-label" for="s-when">When</label>
        <select class="form-select mb-2" id="s-when" name="minutes"><option value="1">in 1 minute</option><option value="5">in 5 minutes</option><option value="30">in 30 minutes</option><option value="60">in 1 hour</option></select>
        <label class="form-label" for="s-reboot">Type the host name to confirm</label>
        <input class="form-control" id="s-reboot" name="confirm" placeholder="<?= e($settings['hostname'] ?? '') ?>" autocomplete="off" required>
        <div class="form-text">Logged-in users are warned; you can cancel until then. Websites are down while the server restarts.</div>
        <div class="d-flex justify-content-end gap-2 mt-3"><button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button><button class="btn btn-sm btn-danger"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Restart</button></div>
    </form>
<?= $this->modalEnd() ?>
<?php endif; ?>
<?php endif; ?>
