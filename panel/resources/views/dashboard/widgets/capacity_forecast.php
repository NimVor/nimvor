<?php
/**
 * Capacity forecast. A date is only given when at least a week of measurements
 * follow a clear, steady trend; otherwise it says why there is none.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$pct = static fn (?float $v): string => $v === null ? 'N/A' : number_format($v, 1) . '%';
$num = static fn (?float $v): string => $v === null ? 'N/A' : number_format($v, 2);
$say = static function (array $f, string $what, string $unit = '%'): array {
    // [text, css class]
    return match ($f['state']) {
        'insufficient' => ['Not enough data yet: ' . (int) $f['have'] . ' of ' . (int) $f['need'] . ' days measured.', 'text-body-secondary'],
        'no_trend' => ['No clear trend (changes by about ' . number_format((float) $f['per_week'], 1) . $unit . ' a week, unevenly).', 'text-body-secondary'],
        'stable' => ['Not growing (' . number_format((float) $f['per_week'], 1) . $unit . ' a week).', 'text-success'],
        default => $f['beyond_horizon']
            ? ['Growing by ' . number_format((float) $f['per_week'], 1) . $unit . ' a week; ' . $what . ' is not expected within a year.', 'text-body-secondary']
            : ['Growing by ' . number_format((float) $f['per_week'], 1) . $unit . ' a week; ' . $what . ' in about ' . (int) $f['days'] . ' day' . ((int) $f['days'] === 1 ? '' : 's') . '.', (int) $f['days'] <= 30 ? 'text-danger' : ((int) $f['days'] <= 90 ? 'text-warning' : '')],
    };
};
[$dt, $dc] = $say($d['disk']['forecast'], 'the fullest disk is full');
[$mt, $mc] = $say($d['mem']['forecast'], 'memory reaches 95%');
$trend = static fn (?float $now, ?float $prev): string => $now === null || $prev === null || $prev <= 0 ? '' : ($now > $prev * 1.2 ? ' (up from ' . number_format($prev, 1) . ' the week before)' : ($now < $prev * 0.8 ? ' (down from ' . number_format($prev, 1) . ' the week before)' : ' (about the same as the week before)'));
?>
<div class="mb-3">
    <div class="fw-semibold">Disk <span class="fw-normal text-body-secondary">fullest filesystem, now <?= e($pct($d['disk']['now'])) ?></span></div>
    <div class="<?= e($dc) ?>"><?= e($dt) ?></div>
</div>
<div class="mb-3">
    <div class="fw-semibold">Memory <span class="fw-normal text-body-secondary">now <?= e($pct($d['mem']['now'])) ?>, 7-day average <?= e($pct($d['mem']['avg'])) ?>, peak <?= e($pct($d['mem']['peak'])) ?></span></div>
    <div class="<?= e($mc) ?>"><?= e($mt) ?></div>
</div>
<div>
    <div class="fw-semibold">CPU and load, last 7 days</div>
    <div>CPU average <?= e($pct($d['cpu']['avg'])) ?>, peak <?= e($pct($d['cpu']['peak'])) ?><?= e($trend($d['cpu']['avg'], $d['cpu']['prev_avg'])) ?></div>
    <div>Load average <?= e($num($d['load']['avg'])) ?>, peak <?= e($num($d['load']['peak'])) ?><?= e($trend($d['load']['avg'], $d['load']['prev_avg'])) ?></div>
</div>
<div class="text-body-secondary mt-2"><?= (int) $d['days'] ?> day<?= (int) $d['days'] === 1 ? '' : 's' ?> of measurements (kept 30 days). Forecasts follow a straight line through the daily values and are shown only when it fits well.</div>
