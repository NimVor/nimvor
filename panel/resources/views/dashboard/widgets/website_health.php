<?php
/**
 * Website health: the latest real HTTP check of each website. Unmeasured values
 * are shown as N/A, never as zero.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$ms = static fn (?int $v): string => $v === null ? 'N/A' : number_format($v) . ' ms';
$ago = static fn (?int $t): string => $t === null ? 'never' : gmdate('Y-m-d H:i', $t) . ' UTC';
$checkForm = '<form method="post" action="' . e($this->url('dashboard.websites.check')) . '" data-nim-noajax class="d-inline">' . $this->csrfField() . '<button class="btn btn-sm btn-outline-secondary">Check now</button></form>';
?>
<?php $off = (int) ($d['unmonitored'] ?? 0); $offNote = $off > 0 ? '<div class="text-body-secondary mb-2">' . $off . ' website' . ($off === 1 ? ' is' : 's are') . ' left out of the checks (Website → Advanced → Monitoring).</div>' : ''; ?>
<?php if (!empty($d['none'])): ?>
    <div class="text-body-secondary mb-2">
        <?php if ((int) $d['enabled'] === 0): ?><?= $off > 0 ? 'Every enabled website is left out of the checks (Website → Advanced → Monitoring).' : 'No enabled websites to check.' ?><?php else: ?>No check has run yet. The scheduler checks every <?= (int) $d['enabled'] ?> enabled website about every five minutes.<?php endif; ?>
    </div>
    <?php if ((int) $d['enabled'] > 0): ?><?= $checkForm ?><?php endif; ?>
<?php return; endif; ?>
<?php $c = (array) $d['counts']; ?>
<div class="nim-figs">
    <div class="nim-fig"><div class="nim-fig-value"><?= (int) $d['total'] ?></div><div class="nim-fig-label">Websites</div></div>
    <div class="nim-fig"><div class="nim-fig-value text-success"><?= (int) $c['up'] + (int) $c['auth'] ?></div><div class="nim-fig-label">Online</div></div>
    <div class="nim-fig"><div class="nim-fig-value <?= (int) $c['down'] > 0 ? 'text-danger' : '' ?>"><?= (int) $c['down'] ?></div><div class="nim-fig-label">Offline</div></div>
    <div class="nim-fig"><div class="nim-fig-value <?= (int) $c['server_error'] > 0 ? 'text-danger' : '' ?>"><?= (int) $c['server_error'] ?></div><div class="nim-fig-label">5xx errors</div></div>
    <div class="nim-fig"><div class="nim-fig-value <?= (int) $c['client_error'] > 0 ? 'text-warning' : '' ?>"><?= (int) $c['client_error'] ?></div><div class="nim-fig-label">4xx errors</div></div>
    <div class="nim-fig"><div class="nim-fig-value"><?= $d['uptime_24h'] === null ? 'N/A' : e(number_format((float) $d['uptime_24h'], 2)) . '%' ?></div><div class="nim-fig-label">Uptime 24 h</div></div>
</div>
<?= $offNote ?>
<?php if ((int) $c['auth'] > 0): ?><div class="text-body-secondary mb-2"><?= (int) $c['auth'] ?> website<?= (int) $c['auth'] === 1 ? ' asks' : 's ask' ?> for a login or forbid the check (401/403); they are counted as online.</div><?php endif; ?>
<?php if ($d['problems'] !== []): ?>
    <div class="mb-3">
        <div class="fw-semibold text-danger mb-1">Failing now</div>
        <ul class="list-unstyled mb-0">
            <?php foreach ((array) $d['problems'] as $p): ?>
                <li><span class="font-monospace"><?= e((string) $p['domain']) ?></span>
                    <span class="text-body-secondary">— <?= (int) $p['status'] > 0 ? 'HTTP ' . (int) $p['status'] : e(match ((string) $p['error']) { 'timeout' => 'no answer in time', 'refused' => 'connection refused', 'tls' => 'secure connection failed', default => 'no answer' }) ?><?= $p['since'] !== null ? ', since ' . e(gmdate('H:i', (int) $p['since'])) . ' UTC' : '' ?></span></li>
            <?php endforeach; ?>
        </ul>
    </div>
<?php endif; ?>
<div class="row g-3">
    <div class="col-12 col-md-7">
        <div class="fw-semibold mb-1">Slowest websites <span class="text-body-secondary fw-normal">(average <?= e($ms($d['average_ms'])) ?>)</span></div>
        <?php if ($d['slowest'] === []): ?><div class="text-body-secondary">No timings yet.</div><?php else: ?>
            <table class="table table-sm mb-0"><tbody>
            <?php foreach ((array) $d['slowest'] as $s): ?>
                <tr><td class="text-truncate nim-w-9"><?= e((string) $s['domain']) ?></td><td class="text-end text-nowrap <?= (int) $s['ms'] > 2000 ? 'text-warning' : '' ?>"><?= e($ms((int) $s['ms'])) ?></td></tr>
            <?php endforeach; ?>
            </tbody></table>
        <?php endif; ?>
    </div>
    <div class="col-12 col-md-5 text-body-secondary">
        <div>Last success: <?= e($ago($d['last_ok'])) ?></div>
        <div>Last failure: <?= e($ago($d['last_down'])) ?></div>
        <div>Checked: <?= (int) $d['checked'] ?> of <?= (int) $d['enabled'] ?> enabled</div>
        <div class="mt-2"><?= $checkForm ?></div>
    </div>
</div>
