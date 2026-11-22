<?php
/**
 * Security overview. A source that could not be read says N/A; it is never drawn as "fine".
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$state = static function (?string $v, array $labels): array {
    // [text, css class]
    if ($v === null) {
        return ['N/A', 'text-body-secondary'];
    }
    return $labels[$v] ?? [$v, ''];
};
$onOff = ['on' => ['Active', 'text-success'], 'off' => ['Off', 'text-danger'], 'missing' => ['Not installed', 'text-warning'], 'stale' => ['No updates running', 'text-warning']];
$num = static fn (?int $n): string => $n === null ? 'N/A' : number_format($n);
$rows = [
    ['Firewall', $state($d['firewall'], $onOff)],
    ['fail2ban', $state($d['fail2ban'], $onOff)],
    ['Malware scanner', $state($d['malware'], $onOff)],
    ['ModSecurity', $state($d['modsec'], $onOff)],
    ['Brute force protection', $d['hulk'] ? ['Active', 'text-success'] : ['Off', 'text-danger']],
];
$f = $d['findings'];
?>
<div class="row g-3">
    <div class="col-12 col-md-5">
        <table class="table table-sm mb-0"><tbody>
            <?php foreach ($rows as [$label, [$text, $cls]]): ?>
                <tr><td><?= e($label) ?></td><td class="text-end <?= e($cls) ?>"><?= e($text) ?></td></tr>
            <?php endforeach; ?>
        </tbody></table>
    </div>
    <div class="col-12 col-md-7">
        <div class="nim-figs">
            <div class="nim-fig"><div class="nim-fig-value <?= ($d['ssh_failed_today'] ?? 0) > 0 ? 'text-warning' : '' ?>"><?= e($num($d['ssh_failed_today'])) ?></div><div class="nim-fig-label">Failed SSH today</div></div>
            <div class="nim-fig"><div class="nim-fig-value"><?= e($num($d['ssh_failed_week'])) ?></div><div class="nim-fig-label">Failed SSH, 7 days</div></div>
            <div class="nim-fig"><div class="nim-fig-value <?= $d['panel_failed_24h'] > 0 ? 'text-warning' : '' ?>"><?= (int) $d['panel_failed_24h'] ?></div><div class="nim-fig-label">Failed panel sign-ins, 24 h</div></div>
            <div class="nim-fig"><div class="nim-fig-value"><?= e($num($d['banned'])) ?></div><div class="nim-fig-label">Banned addresses<?= $d['jails'] !== null ? ' (' . (int) $d['jails'] . ' jails)' : '' ?></div></div>
            <div class="nim-fig"><div class="nim-fig-value <?= $f !== null && $f['high'] > 0 ? 'text-danger' : '' ?>"><?= $f === null ? 'N/A' : (int) $f['fail'] + (int) $f['warn'] ?></div><div class="nim-fig-label">Audit findings<?= $f !== null && $f['high'] > 0 ? ' (' . (int) $f['high'] . ' high)' : '' ?></div></div>
        </div>
        <?php if (is_array($d['ssh_last_failure'] ?? null) && !empty($d['ssh_last_failure']['ip'])): ?>
            <div class="text-body-secondary">Last failed SSH sign-in from <span class="font-monospace"><?= e((string) $d['ssh_last_failure']['ip']) ?></span>.</div>
        <?php endif; ?>
    </div>
</div>
