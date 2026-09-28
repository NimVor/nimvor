<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $f2b */
$canManage = $this->can(\WhaCP\Security\Permissions::SECURITY_MANAGE);
$managed = (string) ($f2b['managed_config'] ?? '');
preg_match('/bantime = (\d+)m/', $managed, $bt);
preg_match('/findtime = (\d+)m/', $managed, $ft);
preg_match('/maxretry = (\d+)/', $managed, $mr);
preg_match('/ignoreip = 127\.0\.0\.1\/8 ::1 ?(.*)/', $managed, $ig);
preg_match_all('/^\[([a-z0-9-]+)\]\nenabled = true/m', $managed, $jm);
$enabled = $managed === '' ? ['sshd', 'recidive'] : $jm[1];
$jails = ['sshd' => 'SSH login failures', 'recidive' => 'Repeat offenders (bans for a week)', 'nginx-http-auth' => 'Nginx basic-auth failures', 'nginx-botsearch' => 'Scanners probing for common exploits', 'nginx-limit-req' => 'Clients exceeding Nginx rate limits'];
$active = (array) ($f2b['jails'] ?? []);
$banned = 0;
$ips = 0;
foreach ($active as $j) {
    $banned += (int) $j['currently_banned'];
    $ips += count((array) $j['banned_ips']);
}
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($f2b !== [] && empty($f2b['installed'])): ?>
    <div class="wcp-infobar"><i class="bi bi-info-circle-fill" aria-hidden="true"></i><span>fail2ban is not installed. Install it from <a href="<?= e($this->url('packages')) ?>">Packages</a> to ban addresses that keep failing to log in.</span></div>
<?php elseif ($f2b !== []): ?>
<div class="wcp-panel mb-3">
    <div class="wcp-statusbar">
        <span class="fw-medium">Brute-force protection (fail2ban)</span>
        <span><?= !empty($f2b['running']) ? '<span class="wcp-state is-ok">Running</span>' : '<span class="wcp-state is-error">Not running</span>' ?></span>
        <span class="small"><span class="wcp-muted">Active jails</span> <?= count($active) ?></span>
        <span class="small"><span class="wcp-muted">Banned now</span> <span class="<?= $banned > 0 ? 'wcp-bad' : '' ?>"><?= $banned ?></span></span>
        <?php if ($canManage): ?><button class="btn btn-sm btn-outline-secondary ms-auto" type="button" data-bs-toggle="modal" data-bs-target="#f2b-settings"><i class="bi bi-sliders me-1"></i>Protection settings</button><?php endif; ?>
    </div>
    <?php if ($canManage): ?>
        <?= $this->modalStart('f2b-settings', 'Protection settings') ?>
            <form class="wcp-form-panel border rounded-3 p-3 mt-3" method="post" action="<?= e($this->url('security.fail2ban.configure')) ?>" data-wcp-busy>
                <?= $this->csrfField() ?>
                <div class="row g-3">
                    <div class="col-lg-6">
                        <div class="fw-semibold small mb-1">Protect</div>
                        <?php foreach ($jails as $k => $label): ?>
                            <div class="form-check"><input class="form-check-input" type="checkbox" name="jails[]" value="<?= e($k) ?>" id="j-<?= e($k) ?>"<?= in_array($k, $enabled, true) ? ' checked' : '' ?>><label class="form-check-label" for="j-<?= e($k) ?>"><?= e($label) ?> <code class="small"><?= e($k) ?></code></label></div>
                        <?php endforeach; ?>
                    </div>
                    <div class="col-lg-6">
                        <div class="row g-2">
                            <div class="col-4"><label class="form-label small" for="f-bt">Ban (min)</label><input class="form-control form-control-sm" id="f-bt" type="number" name="ban_minutes" min="1" value="<?= e($bt[1] ?? '60') ?>"></div>
                            <div class="col-4"><label class="form-label small" for="f-ft">Window (min)</label><input class="form-control form-control-sm" id="f-ft" type="number" name="find_minutes" min="1" value="<?= e($ft[1] ?? '10') ?>"></div>
                            <div class="col-4"><label class="form-label small" for="f-mr">Attempts</label><input class="form-control form-control-sm" id="f-mr" type="number" name="max_retry" min="1" value="<?= e($mr[1] ?? '5') ?>"></div>
                        </div>
                        <label class="form-label small mt-2" for="f-ig">Never ban (IP addresses or networks)</label>
                        <input class="form-control form-control-sm mb-3" id="f-ig" name="ignore" value="<?= e(trim($ig[1] ?? '')) ?>" placeholder="203.0.113.4 198.51.100.0/24">
                        <button class="btn btn-sm btn-primary">Test and apply</button>
                    </div>
                </div>
            </form>
        <?= $this->modalEnd() ?>
    <?php endif; ?>
</div>

<div class="row g-3">
    <div class="col-xl-5">
        <div class="wcp-panel h-100">
            <div class="fw-semibold mb-2">Jails</div>
            <div class="wcp-dtable table-responsive">
                <table class="table align-middle">
                    <thead><tr><th>Jail</th><th class="text-end">Banned now</th><th class="text-end">Total</th><th class="text-end">Failing</th></tr></thead>
                    <tbody>
                    <?php foreach ($active as $j): ?>
                        <tr><td class="fw-semibold" title="<?= e($jails[$j['name']] ?? '') ?>"><?= e($j['name']) ?></td><td class="text-end <?= (int) $j['currently_banned'] > 0 ? 'wcp-bad' : '' ?>"><?= (int) $j['currently_banned'] ?></td><td class="text-end"><?= (int) $j['total_banned'] ?></td><td class="text-end"><?= (int) $j['currently_failed'] ?></td></tr>
                    <?php endforeach; ?>
                    <?php if ($active === []): ?><tr><td colspan="4" class="text-center py-4 wcp-muted">No active jails.</td></tr><?php endif; ?>
                    </tbody>
                </table>
            </div>
        </div>
    </div>
    <div class="col-xl-7">
        <div class="wcp-panel h-100" data-wcp-dt id="f2b-banned">
            <div class="wcp-actionbar">
                <div class="fw-semibold">Banned addresses</div>
                <div class="wcp-actionbar-end"><div class="wcp-search"><input class="form-control form-control-sm" type="search" placeholder="IP address" aria-label="Search banned addresses" data-wcp-dt-search><i class="bi bi-search" aria-hidden="true"></i></div></div>
            </div>
            <div class="wcp-dtable table-responsive">
                <table class="table align-middle">
                    <thead><tr><th>IP address</th><th>Jail</th><th class="text-end">Operate</th></tr></thead>
                    <tbody>
                    <?php foreach ($active as $j): foreach ((array) $j['banned_ips'] as $ip): ?>
                        <tr data-wcp-row>
                            <td><code><?= e($ip) ?></code></td>
                            <td><?= e($j['name']) ?></td>
                            <td class="text-end"><?php if ($canManage): ?><span class="wcp-ops"><form method="post" action="<?= e($this->url('security.fail2ban.unban')) ?>"><?= $this->csrfField() ?><input type="hidden" name="jail" value="<?= e($j['name']) ?>"><input type="hidden" name="ip" value="<?= e($ip) ?>"><button class="wcp-op" aria-label="Unban <?= e($ip) ?>">Unban</button></form></span><?php endif; ?></td>
                        </tr>
                    <?php endforeach; endforeach; ?>
                    <tr class="<?= $ips === 0 ? '' : 'd-none' ?>" data-wcp-dt-empty><td colspan="3" class="text-center py-4 wcp-muted"><?= $ips === 0 ? 'No address is banned right now.' : 'No address matches.' ?></td></tr>
                    </tbody>
                </table>
            </div>
            <div class="wcp-pager" data-wcp-dt-pager></div>
        </div>
    </div>
</div>
<?php endif; ?>
