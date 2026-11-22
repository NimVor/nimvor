<?php
/**
 * A ticket's status (and priority when it is not the normal one) as small badges.
 *
 * @var \NimVorSite\Core\View $v
 * @var string $status
 * @var string|null $priority
 */
$class = ['open' => 'badge-new', 'answered' => 'badge-ok', 'on_hold' => 'badge-warn', 'resolved' => 'badge-ok', 'closed' => 'badge-off'][$status] ?? 'badge-off';
?>
<span class="badge <?= e($class) ?>"><?= e(str_replace('_', ' ', $status)) ?></span><?php if (($priority ?? 'normal') !== 'normal'): ?> <span class="badge <?= ($priority ?? '') === 'urgent' || ($priority ?? '') === 'high' ? 'badge-warn' : 'badge-off' ?>"><?= e((string) $priority) ?></span><?php endif; ?>
