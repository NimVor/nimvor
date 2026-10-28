<?php
/** @var \NimVor\View\View $this */
/** @var list<array<string, mixed>> $checks */
/** @var array<string, mixed>|null $logins */
/** @var bool|null $sshProtected */
/** @var list<array{id: string, title: string, help: string, on: bool, available: bool, detail: string}> $switches */
/** @var array{success: array<string, mixed>|null, failure: array<string, mixed>|null} $panelLogin */
use NimVor\Security\Permissions as P;

$order = ['fail' => 0, 'warn' => 1, 'pass' => 2, 'info' => 3];
usort($checks, static fn ($a, $b) => [$order[$a['status']] ?? 9, $a['title']] <=> [$order[$b['status']] ?? 9, $b['title']]);
$counts = array_count_values(array_map(static fn ($c) => (string) $c['status'], $checks));
// Rating: passed checks weighted by severity (a warning counts half);
// informational checks do not count.
$weight = ['high' => 3, 'critical' => 3, 'medium' => 2, 'low' => 1];
$total = 0.0;
$got = 0.0;
foreach ($checks as $c) {
    if ($c['status'] === 'info') {
        continue;
    }
    $w = $weight[$c['severity'] ?? ''] ?? 1;
    $total += $w;
    $got += $c['status'] === 'pass' ? $w : ($c['status'] === 'warn' ? $w / 2 : 0);
}
$score = $total > 0 ? (int) round($got / $total * 100) : null;
[$scoreClass, $scoreLabel] = $score === null ? ['nim-muted', ''] : ($score >= 80 ? ['nim-good', 'Secure'] : ($score >= 60 ? ['nim-warn', 'Fair'] : ['nim-bad', 'Insecure']));
$icon = ['pass' => 'check-circle-fill', 'warn' => 'exclamation-triangle-fill', 'fail' => 'x-circle-fill', 'info' => 'info-circle'];
$when = static function (?string $t): string {
    if ($t === null || $t === '') {
        return '—';
    }
    $ts = strtotime($t);
    return $ts === false ? $t : gmdate('Y-m-d H:i', $ts) . ' UTC';
};
$max = $logins !== null ? max(1, (int) array_sum((array) ($logins['week'] ?? []))) : 1;
$w5 = static fn (int $n): int => (int) (round($n / $max * 20) * 5);
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($agentError === null): ?>
<div class="row g-3 mb-3">
    <div class="col-xl-6">
        <div class="nim-panel h-100">
            <?php if ($logins === null || empty($logins['available'])): ?>
                <div class="fw-semibold mb-2">SSH logins</div>
                <p class="small nim-muted mb-0">SSH login history is not available (it is read from the systemd journal of the SSH server).</p>
            <?php else: ?>
                <div class="row g-3 align-items-center">
                    <div class="col-6 col-md-3"><div class="nim-stat is-good"><div class="nim-stat-num"><?= (int) $logins['today'][0] ?></div><div class="nim-stat-label">SSH logins today</div></div></div>
                    <div class="col-6 col-md-3"><div class="nim-stat is-bad"><div class="nim-stat-num"><?= (int) $logins['today'][1] ?></div><div class="nim-stat-label">Failed today</div></div></div>
                    <div class="col-md-6">
                        <div class="nim-daycounts" role="table" aria-label="SSH logins: successful and failed">
                            <?php foreach (['Today' => 'today', 'Yesterday' => 'yesterday', '7 days' => 'week'] as $label => $k): $ok = (int) $logins[$k][0]; $bad = (int) $logins[$k][1]; ?>
                                <div class="fw-semibold" role="rowheader"><?= e($label) ?></div>
                                <div class="nim-bar" aria-hidden="true"><span class="nim-bar-ok nim-w-<?= $w5($ok) ?>"></span><span class="nim-bar-bad nim-w-<?= $w5($bad) ?>"></span></div>
                                <div class="nim-good" role="cell" title="Successful"><?= $ok ?></div>
                                <div class="nim-bad" role="cell" title="Failed"><?= $bad ?></div>
                            <?php endforeach; ?>
                        </div>
                    </div>
                </div>
            <?php endif; ?>
            <?php if ($sshProtected === false): ?>
                <div class="d-flex gap-3 align-items-start mt-3 pt-3 border-top">
                    <i class="bi bi-info-circle-fill fs-3 nim-warn" aria-hidden="true"></i>
                    <div>
                        <div class="fw-semibold">SSH brute-force protection is off</div>
                        <div class="small nim-muted mb-2">Fail2ban bans addresses that keep failing to log in.</div>
                        <a class="btn btn-sm btn-primary" href="<?= e($this->url('security.fail2ban')) ?>">Set up protection</a>
                    </div>
                </div>
            <?php elseif ($sshProtected === true): ?>
                <div class="small mt-3 pt-3 border-top"><i class="bi bi-shield-check nim-good me-1"></i>SSH brute-force protection (fail2ban) is on. <a href="<?= e($this->url('security.fail2ban')) ?>">Banned addresses</a></div>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-xl-6">
        <div class="nim-panel h-100">
            <div class="fw-semibold mb-2">Last login information</div>
            <div class="row g-3">
                <?php $sshOk = $logins['last_success'] ?? null; $sshBad = $logins['last_failure'] ?? null; ?>
                <div class="col-sm-6">
                    <div class="nim-login-card">
                        <div class="d-flex"><span class="fw-semibold">SSH login</span><span class="ms-auto <?= $sshOk !== null ? 'nim-good' : 'nim-muted' ?>"><?= $sshOk !== null ? 'Success' : 'None in 7 days' ?></span></div>
                        <dl>
                            <dt>User</dt><dd><?= e((string) ($sshOk['user'] ?? '—')) ?><?= !empty($sshOk['method']) ? ' <span class="nim-muted">(' . e((string) $sshOk['method']) . ')</span>' : '' ?></dd>
                            <dt>IP</dt><dd><?= e((string) ($sshOk['ip'] ?? '—')) ?></dd>
                            <dt>Time</dt><dd><?= e($when($sshOk['time'] ?? null)) ?></dd>
                        </dl>
                        <?php if ($sshBad !== null): ?><div class="small mt-2 nim-bad">Last failure: <?= e((string) $sshBad['user']) ?> from <?= e((string) $sshBad['ip']) ?>, <?= e($when($sshBad['time'])) ?></div><?php endif; ?>
                    </div>
                </div>
                <div class="col-sm-6">
                    <?php $pOk = $panelLogin['success']; $pBad = $panelLogin['failure']; ?>
                    <div class="nim-login-card">
                        <div class="d-flex"><span class="fw-semibold">Panel login</span><span class="ms-auto <?= $pOk !== null ? 'nim-good' : 'nim-muted' ?>"><?= $pOk !== null ? 'Success' : 'None yet' ?></span></div>
                        <dl>
                            <dt>User</dt><dd><?= e((string) ($pOk['username'] ?? '—')) ?></dd>
                            <dt>IP</dt><dd><?= e((string) ($pOk['ip'] ?? '—')) ?></dd>
                            <dt>Time</dt><dd><?= e($when($pOk['created_at'] ?? null)) ?></dd>
                        </dl>
                        <?php if ($pBad !== null): ?><div class="small mt-2 nim-bad">Last failure: <?= e((string) $pBad['username']) ?> from <?= e((string) $pBad['ip']) ?>, <?= e($when($pBad['created_at'])) ?></div><?php endif; ?>
                        <?php if ($this->can(P::AUDIT_VIEW) && $this->hasRoute('audit')): ?><div class="small mt-2"><a href="<?= e($this->url('audit')) ?>">Audit log</a></div><?php endif; ?>
                    </div>
                </div>
            </div>
        </div>
    </div>
</div>

<?php if (($switches ?? []) !== []):
    $pages = ['malware' => 'security.malware', 'fail2ban' => 'security.fail2ban', 'compilers' => 'security.compilers', 'smtp' => 'security.smtp', 'egress' => 'security.egress', 'hulk' => 'security.hulk', 'forkbomb' => 'security.forkbomb', 'traceroute' => 'security.traceroute', 'modsec' => 'security.modsec'];
    $canSet = $this->can(P::SERVER_MANAGE); ?>
<div class="nim-panel mb-3" id="switches">
    <div class="fw-semibold mb-1">Security settings</div>
    <div class="small nim-muted mb-3">The recommended protections. The server setup turns them on; enable or disable any of them here.</div>
    <div class="nim-dtable table-responsive">
        <table class="table align-middle mb-0">
            <thead><tr><th>Setting</th><th>Status</th><th class="text-end">Action</th></tr></thead>
            <tbody>
            <?php foreach ($switches as $sw): $page = $pages[$sw['id']] ?? null; ?>
                <tr>
                    <td>
                        <div class="fw-medium"><?= e($sw['title']) ?></div>
                        <div class="small nim-muted"><?= e($sw['help']) ?></div>
                        <?php if ($sw['detail'] !== '' && !$sw['on']): ?><div class="small nim-warn"><?= e($sw['detail']) ?></div><?php endif; ?>
                    </td>
                    <td><span class="nim-state <?= $sw['on'] ? 'is-ok' : 'is-warn' ?>"><?= $sw['on'] ? 'Enabled' : 'Disabled' ?></span></td>
                    <td class="text-end text-nowrap">
                        <?php if ($canSet && ($sw['available'] || $sw['on'])): ?>
                            <form class="d-inline" method="post" action="<?= e($this->url('security.defaults')) ?>" data-nim-busy><?= $this->csrfField() ?>
                                <input type="hidden" name="item" value="<?= e($sw['id']) ?>">
                                <input type="hidden" name="op" value="<?= $sw['on'] ? 'disable' : 'enable' ?>">
                                <button class="btn btn-sm <?= $sw['on'] ? 'btn-outline-secondary' : 'btn-primary' ?>"><?= $sw['on'] ? 'Disable' : 'Enable' ?></button>
                            </form>
                        <?php elseif ($page !== null && $this->hasRoute($page)): ?>
                            <a class="btn btn-sm btn-primary" href="<?= e($this->url($page)) ?>">Set up</a>
                        <?php endif; ?>
                        <?php if ($page !== null && $this->hasRoute($page)): ?><a class="btn btn-sm btn-link" href="<?= e($this->url($page)) ?>">Settings</a><?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
</div>
<?php endif; ?>

<?php if ($checks !== []): ?>
<div class="nim-panel">
    <div class="d-flex flex-wrap align-items-center gap-3 mb-3">
        <div class="nim-rating">Safety rating: <span class="<?= e($scoreClass) ?>"><?= $score === null ? '—' : $score . ' / 100' ?></span> <span class="small fw-normal <?= e($scoreClass) ?>"><?= e($scoreLabel) ?></span></div>
        <div class="small nim-muted d-flex flex-wrap gap-3">
            <span><i class="bi bi-x-circle-fill nim-bad"></i> <?= (int) ($counts['fail'] ?? 0) ?> to fix</span>
            <span><i class="bi bi-exclamation-triangle-fill nim-warn"></i> <?= (int) ($counts['warn'] ?? 0) ?> to review</span>
            <span><i class="bi bi-check-circle-fill nim-good"></i> <?= (int) ($counts['pass'] ?? 0) ?> passed</span>
        </div>
    </div>
    <div class="nim-checks">
        <?php foreach ($checks as $c): $st = in_array($c['status'], ['pass', 'warn', 'fail', 'info'], true) ? $c['status'] : 'info'; ?>
            <div class="nim-check is-<?= e($st) ?>">
                <div class="nim-check-title"><?= e($c['title']) ?> <span class="visually-hidden"><?= e($c['status']) ?></span></div>
                <div class="nim-check-text"><i class="bi bi-<?= e($icon[$st]) ?>" aria-hidden="true"></i><span><?= e($c['status'] !== 'pass' && !empty($c['remedy']) ? $c['remedy'] : $c['detail']) ?></span></div>
                <?php if ($c['status'] !== 'pass' && !empty($c['remedy'])): ?><div class="nim-check-more"><?= e($c['detail']) ?></div><?php endif; ?>
                <div class="nim-check-more">Severity: <?= e($c['severity']) ?></div>
                <?php if (!empty($c['action']) && $this->can(\NimVor\Security\Permissions::SERVER_MANAGE)): ?>
                    <form class="mt-2 d-flex flex-wrap align-items-center gap-2" method="post" action="<?= e($this->url($c['action']['route'])) ?>" data-nim-busy data-nim-confirm="<?= e($c['action']['confirm'] ?? $c['action']['label'] . '?') ?>"><?= $this->csrfField() ?>
                        <?php foreach ((array) ($c['action']['params'] ?? []) as $pk => $pv): ?><input type="hidden" name="<?= e((string) $pk) ?>" value="<?= e((string) $pv) ?>"><?php endforeach; ?>
                        <button class="btn btn-sm <?= !empty($c['action']['undo']) ? 'btn-outline-secondary' : 'btn-primary' ?>"><?= e($c['action']['label']) ?></button>
                        <?php if (!empty($c['link']) && $this->hasRoute($c['link'])): ?><a class="small" href="<?= e($this->url($c['link'])) ?>">Open <i class="bi bi-arrow-right" aria-hidden="true"></i></a><?php endif; ?>
                    </form>
                <?php elseif (!empty($c['link']) && $c['status'] !== 'pass' && $this->hasRoute($c['link'])): ?>
                    <a class="small d-inline-block mt-2" href="<?= e($this->url($c['link'])) ?>">Open <i class="bi bi-arrow-right" aria-hidden="true"></i></a>
                <?php endif; ?>
            </div>
        <?php endforeach; ?>
    </div>
</div>
<?php endif; ?>
<?php endif; ?>
