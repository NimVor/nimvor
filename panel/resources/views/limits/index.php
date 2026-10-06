<?php
/**
 * @var \WhaCP\View\View $this
 * @var array<string, mixed>|null $status
 * @var array<string, array<string, mixed>> $desired
 * @var array<string, array<string, mixed>> $names
 * @var array<string, mixed>|null $quota
 * @var list<array<string, mixed>> $quotaRows
 * @var string $quotaJob
 */
use WhaCP\Security\Permissions as P;

$applied = (array) ($status['accounts'] ?? []);
$accounts = array_unique(array_merge(array_keys($desired), array_keys($applied)));
sort($accounts, SORT_NATURAL);
$fmt = static function (array $l): string {
    $p = [];
    if ((int) ($l['cpu_percent'] ?? 0) > 0) { $p[] = 'CPU ' . (int) $l['cpu_percent'] . '%'; }
    if ((int) ($l['memory_mb'] ?? 0) > 0) { $p[] = format_bytes((int) $l['memory_mb'] * 1048576, 0) . ' memory'; }
    if ((int) ($l['tasks_max'] ?? 0) > 0) { $p[] = (int) $l['tasks_max'] . ' processes'; }
    if ((int) ($l['io_read_mbps'] ?? 0) > 0) { $p[] = 'read ' . (int) $l['io_read_mbps'] . ' MB/s'; }
    if ((int) ($l['io_write_mbps'] ?? 0) > 0) { $p[] = 'write ' . (int) $l['io_write_mbps'] . ' MB/s'; }
    return $p === [] ? '—' : implode(' · ', $p);
};
?>
<?php $this->start(); ?>Resource limits<?php $this->end('title'); ?>
<?php $this->start(); ?>CPU, memory, processes and disk speed of hosting accounts (cgroups)<?php $this->end('subtitle'); ?>
<?php $this->start(); ?><?php if ($this->can(P::SERVER_MANAGE)): ?><form method="post" action="<?= e($this->url('limits.apply')) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-primary"><i class="bi bi-arrow-repeat me-1"></i>Apply the plans now</button></form><?php endif; ?><?php $this->end('actions'); ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>

<div class="wcp-infobar">
    <i class="bi bi-info-circle" aria-hidden="true"></i>
    <span>Limits come from each account's <a href="<?= e($this->url('plans')) ?>">plan</a> and are shared by all of its websites' PHP, cron jobs and workers (a systemd slice per account); its SSH/SFTP logins get the same limits. They survive reboots and apply at once when a plan changes. Administrators' websites are not limited.
    <?php if ($status !== null && empty($status['cgroup_v2'])): ?><strong class="text-danger">This server does not use cgroup v2, which limits need.</strong><?php endif; ?></span>
</div>

<div class="wcp-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#accounts" aria-controls="accounts" aria-selected="true"><i class="bi bi-cpu me-1"></i>Limited accounts <span class="badge text-bg-light border"><?= count($accounts) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#disk-quotas" aria-controls="disk-quotas" aria-selected="false"><i class="bi bi-hdd me-1"></i>Disk quotas<?php if ($quota !== null): ?> <span class="badge text-bg-light border"><?= ($quota['state'] ?? '') === 'on' ? 'on' : 'off' ?></span><?php endif; ?></button>
</div>
<div class="tab-content">
<div class="tab-pane fade show active" id="accounts" role="tabpanel">
<div class="wcp-panel" data-wcp-dt>
    <div class="wcp-actionbar"><div class="fw-semibold">Limited accounts <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($accounts) ?></span></div>
        <div class="wcp-actionbar-end"><div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="Search" aria-label="Search accounts" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div></div>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>Account</th><th>Limits</th><th>System users</th><th>Memory</th><th>Processes</th><th>CPU throttled</th><th>State</th></tr></thead>
            <tbody>
            <?php foreach ($accounts as $acct): $d = $desired[$acct] ?? null; $a = $applied[$acct] ?? null; $u = (array) ($a['usage'] ?? []); $n = $names[$acct] ?? null;
                $lim = (array) ($d['limits'] ?? $a['limits'] ?? []); $memMax = (int) ($lim['memory_mb'] ?? 0) * 1048576;
                $match = $d !== null && $a !== null && array_intersect_key((array) $a['limits'], (array) $d['limits']) == $d['limits']; ?>
                <tr data-wcp-row>
                    <td class="fw-semibold"><?php if ($n): ?><a href="<?= e($this->url('users.edit', ['id' => $n['user_id']])) ?>"><?= e($n['username']) ?></a><div class="small wcp-muted"><?= e($n['plan'] !== '' ? $n['plan'] : 'no plan') ?> · <span class="font-monospace">whacp-<?= e($acct) ?>.slice</span></div><?php else: ?><span class="font-monospace"><?= e($acct) ?></span><?php endif; ?></td>
                    <td class="small"><?= e($fmt($lim)) ?></td>
                    <td class="small font-monospace"><?= e(implode(', ', (array) ($d['users'] ?? $a['users'] ?? []))) ?></td>
                    <td class="small"><?php if (!empty($u['active'])): $pct = $memMax > 0 ? (int) min(100, round(100 * (int) $u['memory_bytes'] / $memMax)) : null; ?>
                        <?= e(format_bytes((int) $u['memory_bytes'])) ?><?= $pct !== null ? ' (' . $pct . '%)' : '' ?>
                        <?php if ($pct !== null): ?><progress class="wcp-meter w-100" max="100" value="<?= $pct ?>" aria-label="Memory used"><?= $pct ?>%</progress><?php endif; ?>
                        <?= (int) ($u['oom_kills'] ?? 0) > 0 ? '<div class="text-danger">' . (int) $u['oom_kills'] . ' out-of-memory kills</div>' : '' ?>
                    <?php else: ?><span class="wcp-muted">idle</span><?php endif; ?></td>
                    <td class="small"><?= !empty($u['active']) ? (int) $u['tasks'] : '—' ?></td>
                    <td class="small"><?= !empty($u['active']) && (int) ($u['cpu_throttled_usec'] ?? 0) > 0 ? e(format_duration((int) round((int) $u['cpu_throttled_usec'] / 1e6))) : '—' ?></td>
                    <td class="small"><?php if ($d === null): ?><span class="wcp-state is-warn">Being removed</span><?php elseif ($match): ?><span class="wcp-state is-ok">Applied</span><?php else: ?><span class="wcp-state is-warn">Pending</span><?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            <tr class="<?= $accounts === [] ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="7" class="text-center py-4 wcp-muted">No account has resource limits. Set CPU or memory limits on a <a href="<?= e($this->url('plans')) ?>">plan</a>.</td></tr>
            </tbody>
        </table>
    </div>
    <div class="wcp-pager" data-wcp-dt-pager></div>
</div>

</div>
<div class="tab-pane fade" id="disk-quotas" role="tabpanel">
<div class="wcp-panel">
    <div class="wcp-actionbar"><div class="fw-semibold">Disk quotas</div>
        <div class="wcp-actionbar-end">
            <?php if ($quotaJob !== ''): ?><button type="button" class="btn btn-sm btn-link" data-wcp-job-open="<?= e($quotaJob) ?>">Last log</button><?php endif; ?>
            <?php if ($quota !== null && in_array($quota['state'] ?? '', ['off'], true) && $this->can(P::SERVER_MANAGE)): ?>
                <form method="post" action="<?= e($this->url('limits.quota.enable')) ?>" data-wcp-confirm="Turn on disk quotas for <?= e((string) $quota['mount_point']) ?> (<?= e((string) $quota['fstype']) ?>)? <?= ($quota['fstype'] ?? '') === 'xfs' ? 'WhaCP adds uquota to /etc/fstab' . (($quota['mount_point'] ?? '') === '/' ? ' and rootflags=uquota to the kernel options' : '') . '; quotas start after you reboot the server.' : 'The file system is remounted with user quotas (no downtime), every file is counted once (this can take a while on a large disk) and /etc/fstab is updated so they stay on after a reboot. The quota tools, and on some cloud kernels the kernel\'s extra modules, are installed if missing.' ?> Then each account's plan disk space is enforced: writes beyond it fail with “Disk quota exceeded”."><?= $this->csrfField() ?><button class="btn btn-sm btn-primary"><i class="bi bi-hdd me-1"></i>Turn on disk quotas</button></form>
            <?php endif; ?>
        </div></div>
    <div class="p-3 border-bottom small">
        <?php if ($quota === null): ?>
            <span class="wcp-muted">The agent did not report the disk quota state.</span>
        <?php else: $st = (string) ($quota['state'] ?? ''); ?>
            <?php if ($st === 'on'): ?><span class="wcp-state is-ok">On</span>
            <?php elseif ($st === 'pending_reboot'): ?><span class="wcp-state is-warn">After the next reboot</span>
            <?php elseif ($st === 'unsupported'): ?><span class="wcp-state is-error">Not supported</span>
            <?php else: ?><span class="wcp-state is-warn">Off</span><?php endif; ?>
            <span class="ms-2">Websites in <code><?= e((string) ($quota['path'] ?? '')) ?></code> on <code><?= e((string) ($quota['mount_point'] ?? '')) ?></code> (<?= e((string) ($quota['fstype'] ?? '')) ?>, <?= e((string) ($quota['device'] ?? '')) ?>).</span>
            <?php if (!empty($quota['note'])): ?><div class="mt-1"><?= e((string) $quota['note']) ?></div><?php endif; ?>
            <div class="mt-1 wcp-muted"><?= $st === 'on'
                ? 'Each account\'s plan disk space (with its overrides) is enforced on the files its websites\' system users own; an account with several system users shares it between them, re-divided every few minutes. A reseller\'s disk space covers its customers. Email and databases are not counted here.'
                : 'While quotas are off, disk space is only measured: accounts over their plan get alerts and cannot add websites, but files can still be written.' ?></div>
        <?php endif; ?>
    </div>
    <?php if ($quotaRows !== []): ?>
    <div class="wcp-dtable table-responsive">
        <table class="table align-middle">
            <thead><tr><th>System user</th><th>Account</th><th class="text-end">Used</th><th class="text-end">Limit</th><th class="wcp-stat-bar">Use</th></tr></thead>
            <tbody>
            <?php foreach ($quotaRows as $q): $used = (int) $q['used_kb'] * 1024; $hard = (int) $q['hard_kb'] * 1024; $pct = $hard > 0 ? (int) min(100, round(100 * $used / $hard)) : null; ?>
                <tr>
                    <td class="font-monospace small"><?= e((string) $q['system_user']) ?></td>
                    <td class="small"><?= e((string) ($q['account'] ?? '—')) ?></td>
                    <td class="text-end small"><?= e(format_bytes($used)) ?></td>
                    <td class="text-end small"><?= $hard > 0 ? e(format_bytes($hard)) : '<span class="wcp-muted">none</span>' ?></td>
                    <td><?php if ($pct !== null): ?><progress class="wcp-meter w-100" max="100" value="<?= $pct ?>" aria-label="Disk used"><?= $pct ?>%</progress><?php endif; ?></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <?php endif; ?>
</div>
</div>
</div>
