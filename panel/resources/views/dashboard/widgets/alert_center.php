<?php
/**
 * Alert center: open alerts by severity; acknowledge or close them here.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $d
 */
$canAct = $this->can(\NimVor\Security\Permissions::SERVER_MANAGE);
$levels = ['critical' => ['Critical', 'text-danger', 'danger'], 'warning' => ['Warning', 'text-warning', 'warning'], 'info' => ['Information', 'text-info', 'info']];
?>
<div class="nim-figs">
    <?php foreach ($levels as $key => [$label, $cls]): ?>
        <div class="nim-fig"><div class="nim-fig-value <?= (int) $d['counts'][$key] > 0 ? e($cls) : '' ?>"><?= (int) $d['counts'][$key] ?></div><div class="nim-fig-label"><?= e($label) ?></div></div>
    <?php endforeach; ?>
    <div class="nim-fig"><div class="nim-fig-value text-success"><?= (int) $d['resolved_24h'] ?></div><div class="nim-fig-label">Resolved, 24 h</div></div>
</div>
<?php if ($d['open'] === []): ?>
    <div class="text-success"><i class="bi bi-check-circle me-1" aria-hidden="true"></i>No open alerts.</div>
<?php else: ?>
    <ul class="list-unstyled mb-2">
        <?php foreach ((array) $d['open'] as $a): [$label, $cls, $badge] = $levels[$a['severity']] ?? $levels['info']; ?>
            <li class="d-flex align-items-start gap-2 py-1 border-bottom <?= $a['acknowledged'] ? 'text-body-secondary' : '' ?>">
                <span class="badge text-bg-<?= e($badge) ?>"><?= e($label) ?></span>
                <span class="me-auto"><?= e((string) $a['message']) ?>
                    <span class="text-body-secondary">since <?= e(str_replace('T', ' ', substr((string) $a['first_seen'], 0, 16))) ?> UTC<?= $a['acknowledged'] ? ', acknowledged' : '' ?></span></span>
                <?php if ($canAct): ?>
                    <span class="d-flex gap-1 flex-shrink-0">
                        <?php if (!$a['acknowledged']): ?>
                            <form method="post" action="<?= e($this->url('dashboard.alert.ack', ['id' => (int) $a['id']])) ?>" data-nim-noajax><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary py-0">Acknowledge</button></form>
                        <?php endif; ?>
                        <form method="post" action="<?= e($this->url('dashboard.alert.resolve', ['id' => (int) $a['id']])) ?>" data-nim-noajax data-nim-confirm="Close this alert? If the problem is still there it comes back as an acknowledged alert."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary py-0">Resolve</button></form>
                    </span>
                <?php endif; ?>
            </li>
        <?php endforeach; ?>
    </ul>
    <?php if ((int) $d['open_total'] > count((array) $d['open'])): ?><div class="text-body-secondary mb-2"><?= (int) $d['open_total'] - count((array) $d['open']) ?> more on the Monitoring page.</div><?php endif; ?>
<?php endif; ?>
<?php foreach ((array) $d['recent'] as $r): ?>
    <div class="text-body-secondary"><i class="bi bi-check2 me-1" aria-hidden="true"></i>Resolved: <?= e((string) $r['message']) ?></div>
<?php endforeach; ?>
<?php if ((int) $d['acknowledged'] > 0): ?><div class="text-body-secondary mt-1"><?= (int) $d['acknowledged'] ?> acknowledged alert<?= (int) $d['acknowledged'] === 1 ? ' is' : 's are' ?> not counted above and stay open until the problem is gone.</div><?php endif; ?>
