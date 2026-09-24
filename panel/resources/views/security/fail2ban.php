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
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($f2b !== [] && empty($f2b['installed'])): ?>
    <div class="alert alert-info">fail2ban is not installed. Install it from <a href="<?= e($this->url('packages')) ?>">Packages</a>.</div>
<?php elseif ($f2b !== []): ?>
<div class="row g-3">
    <div class="col-xl-7">
        <div class="card">
            <div class="card-header"><i class="bi bi-slash-circle me-1"></i>Jails <?= !empty($f2b['running']) ? $this->statusBadge('active', 'running') : $this->statusBadge('inactive', 'not running') ?></div>
            <?php if (empty($f2b['jails'])): ?>
                <div class="card-body text-body-secondary">No active jails.</div>
            <?php else: ?>
                <ul class="list-group list-group-flush">
                    <?php foreach ((array) $f2b['jails'] as $j): ?>
                        <li class="list-group-item">
                            <div class="d-flex"><strong><?= e($j['name']) ?></strong><span class="ms-auto small text-body-secondary"><?= (int) $j['currently_banned'] ?> banned now · <?= (int) $j['total_banned'] ?> total · <?= (int) $j['currently_failed'] ?> failing</span></div>
                            <?php foreach ((array) $j['banned_ips'] as $ip): ?>
                                <form class="d-inline-flex align-items-center gap-1 me-2 mt-1" method="post" action="<?= e($this->url('security.fail2ban.unban')) ?>"><?= $this->csrfField() ?><input type="hidden" name="jail" value="<?= e($j['name']) ?>"><input type="hidden" name="ip" value="<?= e($ip) ?>"><code><?= e($ip) ?></code><?php if ($canManage): ?><button class="btn btn-sm btn-link p-0" title="Unban"><i class="bi bi-x-circle"></i></button><?php endif; ?></form>
                            <?php endforeach; ?>
                        </li>
                    <?php endforeach; ?>
                </ul>
            <?php endif; ?>
        </div>
    </div>
    <?php if ($canManage): ?>
        <div class="col-xl-5">
            <div class="card">
                <div class="card-header"><i class="bi bi-sliders me-1"></i>Protection settings</div>
                <div class="card-body">
                    <form method="post" action="<?= e($this->url('security.fail2ban.configure')) ?>" data-wcp-busy>
                        <?= $this->csrfField() ?>
                        <?php foreach ($jails as $k => $label): ?>
                            <div class="form-check"><input class="form-check-input" type="checkbox" name="jails[]" value="<?= e($k) ?>" id="j-<?= e($k) ?>"<?= in_array($k, $enabled, true) ? ' checked' : '' ?>><label class="form-check-label" for="j-<?= e($k) ?>"><?= e($label) ?> <code class="small"><?= e($k) ?></code></label></div>
                        <?php endforeach; ?>
                        <div class="row g-2 mt-2">
                            <div class="col-4"><label class="form-label small" for="f-bt">Ban (min)</label><input class="form-control form-control-sm" id="f-bt" type="number" name="ban_minutes" min="1" value="<?= e($bt[1] ?? '60') ?>"></div>
                            <div class="col-4"><label class="form-label small" for="f-ft">Window (min)</label><input class="form-control form-control-sm" id="f-ft" type="number" name="find_minutes" min="1" value="<?= e($ft[1] ?? '10') ?>"></div>
                            <div class="col-4"><label class="form-label small" for="f-mr">Attempts</label><input class="form-control form-control-sm" id="f-mr" type="number" name="max_retry" min="1" value="<?= e($mr[1] ?? '5') ?>"></div>
                        </div>
                        <label class="form-label small mt-2" for="f-ig">Never ban (IP addresses or networks)</label>
                        <input class="form-control form-control-sm mb-3" id="f-ig" name="ignore" value="<?= e(trim($ig[1] ?? '')) ?>" placeholder="203.0.113.4 198.51.100.0/24">
                        <button class="btn btn-sm btn-primary">Test and apply</button>
                    </form>
                </div>
            </div>
        </div>
    <?php endif; ?>
</div>
<?php endif; ?>
