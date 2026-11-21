<?php
/**
 * Traffic: network use over 24 hours and the websites that sent the most this month.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$rate = static fn (float $b): string => format_bytes($b) . '/s';
?>
<div class="fw-semibold mb-1">Network, last <?= $d['net'] === null ? '24' : e((string) $d['net']['hours']) ?> hours</div>
<?php if ($d['net'] === null): ?>
    <div class="text-body-secondary mb-3">Not enough measurements yet (the monitor takes one every five minutes).</div>
<?php else: $n = $d['net']; ?>
    <table class="table table-sm mb-1">
        <thead><tr><th></th><th class="text-end">Transferred</th><th class="text-end">Average</th><th class="text-end">Peak</th></tr></thead>
        <tbody>
            <tr><td>In</td><td class="text-end"><?= e(format_bytes($n['rx_bytes'])) ?></td><td class="text-end"><?= e($rate($n['rx_avg'])) ?></td><td class="text-end"><?= e($rate($n['rx_peak'])) ?></td></tr>
            <tr><td>Out</td><td class="text-end"><?= e(format_bytes($n['tx_bytes'])) ?></td><td class="text-end"><?= e($rate($n['tx_avg'])) ?></td><td class="text-end"><?= e($rate($n['tx_peak'])) ?></td></tr>
        </tbody>
    </table>
    <div class="text-body-secondary mb-3">Estimated from the five-minute samples; short bursts between samples are not seen.</div>
<?php endif; ?>
<div class="fw-semibold mb-1">Websites, sent this month <span class="fw-normal text-body-secondary">(<?= e((string) $d['month']) ?>)</span></div>
<?php if ($d['sites'] === []): ?>
    <div class="text-body-secondary"><?= (int) $d['sites_measured'] > 0 ? 'No website has sent anything this month yet.' : 'Not measured yet: bandwidth is counted from each website\'s access log by the monitor.' ?></div>
<?php else: ?>
    <table class="table table-sm mb-1"><tbody>
        <?php foreach ((array) $d['sites'] as $s): ?>
            <tr><td class="text-truncate nim-w-9"><?= e((string) $s['domain']) ?></td><td class="text-end text-nowrap"><?= e(format_bytes($s['bytes'])) ?></td></tr>
        <?php endforeach; ?>
    </tbody></table>
    <div class="text-body-secondary">All <?= (int) $d['sites_measured'] ?> measured website<?= (int) $d['sites_measured'] === 1 ? '' : 's' ?>: <?= e(format_bytes($d['sites_total'])) ?>. Requests per second and visitors are not collected.</div>
<?php endif; ?>
