<?php
/**
 * The hover card of the Firewall, fail2ban and Malware scanner tiles, from the Security overview's data. A source that could not be read says N/A.
 *
 * @var \NimVor\View\View $this
 * @var string $key firewall, fail2ban or clamav
 * @var array<string, mixed> $d security()
 */
$num = static fn (?int $v): string => $v === null ? 'N/A' : number_format($v);
$onOff = ['on' => ['Active', 'text-success'], 'off' => ['Off', 'text-danger'], 'missing' => ['Not installed', 'text-warning'], 'stale' => ['Running, but signatures are not updating', 'text-warning']];
$state = static function (?string $v) use ($onOff): array {
    return $v === null ? ['N/A', 'text-body-secondary'] : ($onOff[$v] ?? [$v, '']);
};
$stateKey = $key === 'clamav' ? 'malware' : $key;
[$text, $cls] = $state($d[$stateKey] ?? null);
?>
<div class="mb-2"><span class="<?= e($cls) ?>"><?= e($text) ?></span></div>
<table class="table table-sm nim-pop-table mb-0"><tbody>
<?php if ($key === 'firewall'): ?>
    <tr><td>Failed SSH sign-ins today</td><td class="text-end"><?= e($num($d['ssh_failed_today'] ?? null)) ?></td></tr>
    <tr><td>Failed SSH sign-ins, 7 days</td><td class="text-end"><?= e($num($d['ssh_failed_week'] ?? null)) ?></td></tr>
    <tr><td>Brute force protection</td><td class="text-end <?= !empty($d['hulk']) ? 'text-success' : 'text-danger' ?>"><?= !empty($d['hulk']) ? 'Active' : 'Off' ?></td></tr>
<?php elseif ($key === 'fail2ban'): ?>
    <tr><td>Banned addresses</td><td class="text-end"><?= e($num($d['banned'] ?? null)) ?></td></tr>
    <tr><td>Jails</td><td class="text-end"><?= e($num($d['jails'] ?? null)) ?></td></tr>
    <tr><td>Failed SSH sign-ins today</td><td class="text-end"><?= e($num($d['ssh_failed_today'] ?? null)) ?></td></tr>
    <?php if (is_array($d['ssh_last_failure'] ?? null) && !empty($d['ssh_last_failure']['ip'])): ?><tr><td>Last failed SSH sign-in</td><td class="text-end font-monospace"><?= e((string) $d['ssh_last_failure']['ip']) ?></td></tr><?php endif; ?>
<?php else: ?>
    <tr><td>Signatures</td><td class="text-end"><?= e((string) ($d['malware_version'] ?? '') !== '' ? (string) $d['malware_version'] : 'N/A') ?></td></tr>
    <tr><td>ModSecurity</td><td class="text-end"><?= e($state($d['modsec'] ?? null)[0]) ?></td></tr>
<?php endif; ?>
</tbody></table>
