<?php
/**
 * Certificate state as a badge and one line.
 *
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $c
 * @var int $warnDays
 */
$days = (int) ($c['days_remaining'] ?? 0);
[$cls, $label] = match (true) {
    !empty($c['error']) => ['is-error', 'Unreadable'],
    !empty($c['expired']) => ['is-error', 'Expired'],
    $days <= $warnDays => ['is-warn', $days . ' days left'],
    default => ['is-ok', $days . ' days left'],
};
$issuer = (string) ($c['issuer'] ?? '');
if (preg_match('/O=([^,]+)/', $issuer, $m)) {
    $issuer = $m[1];
} elseif (preg_match('/CN=([^,]+)/', $issuer, $m)) {
    $issuer = $m[1];
}
?>
<span class="wcp-state <?= $cls ?>"><?= e($label) ?></span>
<?php if (empty($c['error'])): ?><span class="small wcp-muted"><?= !empty($c['self_signed']) ? 'self-signed' : e($issuer) ?> · until <?= e(substr((string) ($c['not_after'] ?? ''), 0, 10)) ?></span><?php else: ?><span class="small wcp-muted"><?= e((string) $c['error']) ?></span><?php endif; ?>
