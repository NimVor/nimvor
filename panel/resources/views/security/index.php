<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $checks */
$icon = ['pass' => 'check-circle-fill text-success', 'warn' => 'exclamation-triangle-fill text-warning', 'fail' => 'x-octagon-fill text-danger', 'info' => 'info-circle text-body-secondary'];
$order = ['fail' => 0, 'warn' => 1, 'info' => 2, 'pass' => 3];
usort($checks, static fn ($a, $b) => [$order[$a['status']] ?? 9, $a['title']] <=> [$order[$b['status']] ?? 9, $b['title']]);
$counts = array_count_values(array_map(static fn ($c) => (string) $c['status'], $checks));
?>
<?= $this->partial('security/_header', get_defined_vars()) ?>
<?php if ($checks !== []): ?>
    <div class="d-flex gap-3 mb-3 small">
        <span><i class="bi bi-x-octagon-fill text-danger"></i> <?= (int) ($counts['fail'] ?? 0) ?> to fix</span>
        <span><i class="bi bi-exclamation-triangle-fill text-warning"></i> <?= (int) ($counts['warn'] ?? 0) ?> to review</span>
        <span><i class="bi bi-check-circle-fill text-success"></i> <?= (int) ($counts['pass'] ?? 0) ?> passed</span>
    </div>
    <div class="list-group">
        <?php foreach ($checks as $c): ?>
            <div class="list-group-item d-flex gap-3 align-items-start">
                <i class="bi bi-<?= e($icon[$c['status']] ?? 'circle') ?> fs-5 mt-1" aria-hidden="true"></i>
                <div>
                    <div class="fw-semibold"><?= e($c['title']) ?> <span class="badge text-bg-light border text-body-secondary fw-normal"><?= e($c['severity']) ?></span> <span class="visually-hidden"><?= e($c['status']) ?></span></div>
                    <div class="small"><?= e($c['detail']) ?></div>
                    <?php if (!empty($c['remedy']) && $c['status'] !== 'pass'): ?><div class="small text-body-secondary mt-1"><i class="bi bi-arrow-return-right me-1"></i><?= e($c['remedy']) ?></div><?php endif; ?>
                </div>
            </div>
        <?php endforeach; ?>
    </div>
<?php endif; ?>
