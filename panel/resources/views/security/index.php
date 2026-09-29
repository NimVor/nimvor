<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $checks */
/** @var array<string, mixed>|null $logins */
/** @var bool|null $sshProtected */
/** @var array{success: array<string, mixed>|null, failure: array<string, mixed>|null} $panelLogin */
use WhaCP\Security\Permissions as P;

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
[$scoreClass, $scoreLabel] = $score === null ? ['wcp-muted', ''] : ($score >= 80 ? ['wcp-good', 'Secure'] : ($score >= 60 ? ['wcp-warn', 'Fair'] : ['wcp-bad', 'Insecure']));
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
        <div class="wcp-panel h-100">
            <?php if ($logins === null || empty($logins['available'])): ?>
                <div class="fw-semibold mb-2">SSH logins</div>
                <p class="small wcp-muted mb-0">SSH login history is not available (it is read from the systemd journal of the SSH server).</p>
            <?php else: ?>
                <div class="row g-3 align-items-center">
                    <div class="col-6 col-md-3"><div class="wcp-stat is-good"><div class="wcp-stat-num"><?= (int) $logins['today'][0] ?></div><div class="wcp-stat-label">SSH logins today</div></div></div>
                    <div class="col-6 col-md-3"><div class="wcp-stat is-bad"><div class="wcp-stat-num"><?= (int) $logins['today'][1] ?></div><div class="wcp-stat-label">Failed today</div></div></div>
                    <div class="col-md-6">
                        <div class="wcp-daycounts" role="table" aria-label="SSH logins: successful and failed">
                            <?php foreach (['Today' => 'today', 'Yesterday' => 'yesterday', '7 days' => 'week'] as $label => $k): $ok = (int) $logins[$k][0]; $bad = (int) $logins[$k][1]; ?>
                                <div class="fw-semibold" role="rowheader"><?= e($label) ?></div>
                                <div class="wcp-bar" aria-hidden="true"><span class="wcp-bar-ok wcp-w-<?= $w5($ok) ?>"></span><span class="wcp-bar-bad wcp-w-<?= $w5($bad) ?>"></span></div>
                                <div class="wcp-good" role="cell" title="Successful"><?= $ok ?></div>
                                <div class="wcp-bad" role="cell" title="Failed"><?= $bad ?></div>
                            <?php endforeach; ?>
                        </div>
                    </div>
                </div>
            <?php endif; ?>
            <?php if ($sshProtected === false): ?>
                <div class="d-flex gap-3 align-items-start mt-3 pt-3 border-top">
                    <i class="bi bi-info-circle-fill fs-3 wcp-warn" aria-hidden="true"></i>
                    <div>
                        <div class="fw-semibold">SSH brute-force protection is off</div>
                        <div class="small wcp-muted mb-2">Fail2ban bans addresses that keep failing to log in.</div>
                        <a class="btn btn-sm btn-primary" href="<?= e($this->url('security.fail2ban')) ?>">Set up protection</a>
                    </div>
                </div>
            <?php elseif ($sshProtected === true): ?>
                <div class="small mt-3 pt-3 border-top"><i class="bi bi-shield-check wcp-good me-1"></i>SSH brute-force protection (fail2ban) is on. <a href="<?= e($this->url('security.fail2ban')) ?>">Banned addresses</a></div>
            <?php endif; ?>
        </div>
    </div>
    <div class="col-xl-6">
        <div class="wcp-panel h-100">
            <div class="fw-semibold mb-2">Last login information</div>
            <div class="row g-3">
                <?php $sshOk = $logins['last_success'] ?? null; $sshBad = $logins['last_failure'] ?? null; ?>
                <div class="col-sm-6">
                    <div class="wcp-login-card">
                        <div class="d-flex"><span class="fw-semibold">SSH login</span><span class="ms-auto <?= $sshOk !== null ? 'wcp-good' : 'wcp-muted' ?>"><?= $sshOk !== null ? 'Success' : 'None in 7 days' ?></span></div>
                        <dl>
                            <dt>User</dt><dd><?= e((string) ($sshOk['user'] ?? '—')) ?><?= !empty($sshOk['method']) ? ' <span class="wcp-muted">(' . e((string) $sshOk['method']) . ')</span>' : '' ?></dd>
                            <dt>IP</dt><dd><?= e((string) ($sshOk['ip'] ?? '—')) ?></dd>
                            <dt>Time</dt><dd><?= e($when($sshOk['time'] ?? null)) ?></dd>
                        </dl>
                        <?php if ($sshBad !== null): ?><div class="small mt-2 wcp-bad">Last failure: <?= e((string) $sshBad['user']) ?> from <?= e((string) $sshBad['ip']) ?>, <?= e($when($sshBad['time'])) ?></div><?php endif; ?>
                    </div>
                </div>
                <div class="col-sm-6">
                    <?php $pOk = $panelLogin['success']; $pBad = $panelLogin['failure']; ?>
                    <div class="wcp-login-card">
                        <div class="d-flex"><span class="fw-semibold">Panel login</span><span class="ms-auto <?= $pOk !== null ? 'wcp-good' : 'wcp-muted' ?>"><?= $pOk !== null ? 'Success' : 'None yet' ?></span></div>
                        <dl>
                            <dt>User</dt><dd><?= e((string) ($pOk['username'] ?? '—')) ?></dd>
                            <dt>IP</dt><dd><?= e((string) ($pOk['ip'] ?? '—')) ?></dd>
                            <dt>Time</dt><dd><?= e($when($pOk['created_at'] ?? null)) ?></dd>
                        </dl>
                        <?php if ($pBad !== null): ?><div class="small mt-2 wcp-bad">Last failure: <?= e((string) $pBad['username']) ?> from <?= e((string) $pBad['ip']) ?>, <?= e($when($pBad['created_at'])) ?></div><?php endif; ?>
                        <?php if ($this->can(P::AUDIT_VIEW) && $this->hasRoute('audit')): ?><div class="small mt-2"><a href="<?= e($this->url('audit')) ?>">Audit log</a></div><?php endif; ?>
                    </div>
                </div>
            </div>
        </div>
    </div>
</div>

<?php if ($checks !== []): ?>
<div class="wcp-panel">
    <div class="d-flex flex-wrap align-items-center gap-3 mb-3">
        <div class="wcp-rating">Safety rating: <span class="<?= e($scoreClass) ?>"><?= $score === null ? '—' : $score . ' / 100' ?></span> <span class="small fw-normal <?= e($scoreClass) ?>"><?= e($scoreLabel) ?></span></div>
        <div class="small wcp-muted d-flex flex-wrap gap-3">
            <span><i class="bi bi-x-circle-fill wcp-bad"></i> <?= (int) ($counts['fail'] ?? 0) ?> to fix</span>
            <span><i class="bi bi-exclamation-triangle-fill wcp-warn"></i> <?= (int) ($counts['warn'] ?? 0) ?> to review</span>
            <span><i class="bi bi-check-circle-fill wcp-good"></i> <?= (int) ($counts['pass'] ?? 0) ?> passed</span>
        </div>
    </div>
    <div class="wcp-checks">
        <?php foreach ($checks as $c): $st = in_array($c['status'], ['pass', 'warn', 'fail', 'info'], true) ? $c['status'] : 'info'; ?>
            <div class="wcp-check is-<?= e($st) ?>">
                <div class="wcp-check-title"><?= e($c['title']) ?> <span class="visually-hidden"><?= e($c['status']) ?></span></div>
                <div class="wcp-check-text"><i class="bi bi-<?= e($icon[$st]) ?>" aria-hidden="true"></i><span><?= e($c['status'] !== 'pass' && !empty($c['remedy']) ? $c['remedy'] : $c['detail']) ?></span></div>
                <?php if ($c['status'] !== 'pass' && !empty($c['remedy'])): ?><div class="wcp-check-more"><?= e($c['detail']) ?></div><?php endif; ?>
                <div class="wcp-check-more">Severity: <?= e($c['severity']) ?></div>
                <?php if (!empty($c['action']) && $this->can(\WhaCP\Security\Permissions::SERVER_MANAGE)): ?>
                    <form class="mt-2" method="post" action="<?= e($this->url($c['action']['route'])) ?>" data-wcp-busy data-wcp-confirm="<?= e($c['action']['label']) ?>?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger"><?= e($c['action']['label']) ?></button></form>
                <?php elseif (!empty($c['link']) && $c['status'] !== 'pass' && $this->hasRoute($c['link'])): ?>
                    <a class="small d-inline-block mt-2" href="<?= e($this->url($c['link'])) ?>">Open <i class="bi bi-arrow-right" aria-hidden="true"></i></a>
                <?php endif; ?>
            </div>
        <?php endforeach; ?>
    </div>
</div>
<?php endif; ?>
<?php endif; ?>
