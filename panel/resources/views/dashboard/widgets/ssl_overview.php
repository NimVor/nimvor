<?php
/**
 * SSL certificates: counts, nearest expiry, renewal and uncovered websites.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$renewal = ['on' => ['Automatic renewal is on', 'text-success'], 'off' => ['Automatic renewal is off', 'text-danger'], 'missing' => ['certbot is not installed', 'text-warning']];
[$rt, $rc] = $d['renewal'] === null ? ['Renewal status unknown', 'text-body-secondary'] : $renewal[$d['renewal']];
?>
<div class="nim-figs">
    <div class="nim-fig"><div class="nim-fig-value"><?= (int) $d['total'] ?></div><div class="nim-fig-label">Certificates</div></div>
    <div class="nim-fig"><div class="nim-fig-value text-success"><?= (int) $d['valid'] ?></div><div class="nim-fig-label">Valid</div></div>
    <div class="nim-fig"><div class="nim-fig-value <?= (int) $d['expiring'] > 0 ? 'text-warning' : '' ?>"><?= (int) $d['expiring'] ?></div><div class="nim-fig-label">Expire in <?= (int) $d['warn_days'] ?> days</div></div>
    <div class="nim-fig"><div class="nim-fig-value <?= (int) $d['expired'] > 0 ? 'text-danger' : '' ?>"><?= (int) $d['expired'] ?></div><div class="nim-fig-label">Expired</div></div>
</div>
<div class="mb-2 <?= e($rc) ?>"><i class="bi bi-arrow-repeat me-1" aria-hidden="true"></i><?= e($rt) ?><?= $d['auto_ssl'] ? '; websites get a certificate automatically' : '' ?>.</div>
<?php if ($d['soonest'] !== []): ?>
    <table class="table table-sm mb-2"><tbody>
        <?php foreach ((array) $d['soonest'] as $c): ?>
            <tr>
                <td class="text-truncate nim-w-9"><?= e((string) $c['name']) ?></td>
                <td class="text-end text-nowrap <?= $c['days'] < 0 ? 'text-danger' : ($c['days'] <= $d['warn_days'] ? 'text-warning' : '') ?>"><?= $c['days'] < 0 ? 'expired ' . e((string) -$c['days']) . ' days ago' : e((string) $c['days']) . ' days' ?></td>
            </tr>
        <?php endforeach; ?>
    </tbody></table>
<?php endif; ?>
<?php if ((int) $d['uncovered_total'] > 0): ?>
    <div class="text-warning"><?= (int) $d['uncovered_total'] ?> website<?= (int) $d['uncovered_total'] === 1 ? '' : 's' ?> serve HTTPS without a certificate that covers the name: <span class="font-monospace"><?= e(implode(', ', array_slice((array) $d['uncovered'], 0, 3))) ?></span><?= (int) $d['uncovered_total'] > 3 ? ', …' : '' ?></div>
<?php endif; ?>
<?php if ((int) $d['without_https'] > 0): ?>
    <div class="text-body-secondary"><?= (int) $d['without_https'] ?> enabled website<?= (int) $d['without_https'] === 1 ? ' has' : 's have' ?> no HTTPS.</div>
<?php endif; ?>
