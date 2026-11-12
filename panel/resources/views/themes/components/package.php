<?php
/** @var \NimVor\View\View $this */
/** @var string $plan the hosting package's name */
/** @var list<array<string, mixed>> $rows plan usage rows from the account (key, label, used, limit) */
/** What the hosting package includes: its name and each limit (a limit of 0 means unlimited). */
$limits = array_values(array_filter($rows, static fn (array $u): bool => !in_array($u['key'], ['disk', 'bandwidth'], true) || $u['limit'] > 0));
?>
<dl class="nt-dl">
    <dt>Package</dt><dd class="fw-medium"><?= $plan !== '' ? e($plan) : 'Custom' ?></dd>
    <?php foreach ($limits as $u): ?>
        <dt><?= e($u['label']) ?></dt><dd><?= $u['limit'] > 0 ? (int) $u['limit'] : 'Unlimited' ?></dd>
    <?php endforeach; ?>
</dl>
