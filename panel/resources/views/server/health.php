<?php
/**
 * Server → Health: every check by section, worst first, each with a link
 * to where it is fixed.
 *
 * @var \NimVor\View\View $this
 * @var array<string, list<array{status: string, label: string, detail: string, route?: string}>> $result
 * @var array{status: string, fail: int, warn: int, unknown: int, ok: int} $summary
 * @var string $checkedAt
 */
use NimVor\Services\HealthService;

$rank = ['fail' => 0, 'warn' => 1, 'unknown' => 2, 'ok' => 3];
$icon = ['fail' => 'bi-x-circle-fill text-danger', 'warn' => 'bi-exclamation-triangle-fill text-warning', 'unknown' => 'bi-question-circle text-body-secondary', 'ok' => 'bi-check-circle-fill text-success'];
$word = ['fail' => 'Problem', 'warn' => 'Attention', 'unknown' => 'Not checked', 'ok' => 'OK'];
$state = ['fail' => 'is-error', 'warn' => 'is-warn', 'unknown' => '', 'ok' => 'is-ok'];
$headline = [
    'fail' => $summary['fail'] . ' problem(s) need fixing',
    'warn' => $summary['warn'] . ' item(s) need attention',
    'unknown' => 'Some parts could not be checked',
    'ok' => 'Everything looks healthy',
][$summary['status']];
?>
<?php $this->start(); ?>Health<?php $this->end('title'); ?>
<?php $this->start(); ?>Is anything wrong? Every check in one place<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('server.health')) ?>"><i class="bi bi-arrow-clockwise me-1" aria-hidden="true"></i>Check again</a>
<?php $this->end('actions'); ?>

<div class="nim-panel mb-3" data-health="<?= e($summary['status']) ?>">
    <div class="nim-statusbar flex-wrap">
        <i class="bi <?= e($icon[$summary['status']]) ?> fs-4" aria-hidden="true"></i>
        <span class="fw-semibold"><?= e($headline) ?></span>
        <span class="small"><span class="nim-state is-error"><?= (int) $summary['fail'] ?> problem(s)</span> <span class="nim-state is-warn"><?= (int) $summary['warn'] ?> to look at</span> <span class="nim-state is-ok"><?= (int) $summary['ok'] ?> OK</span><?= $summary['unknown'] > 0 ? ' <span class="nim-state">' . (int) $summary['unknown'] . ' not checked</span>' : '' ?></span>
        <span class="small nim-muted ms-auto">Checked <?= e($checkedAt) ?> UTC</span>
    </div>
</div>

<div class="row g-3">
    <?php foreach (HealthService::SECTIONS as $id => [$label, $sIcon, $sRoute]):
        $checks = $result[$id] ?? [];
        usort($checks, static fn (array $a, array $b): int => $rank[$a['status']] <=> $rank[$b['status']]);
        $worst = $checks === [] ? 'ok' : $checks[0]['status']; ?>
        <div class="col-xl-6">
            <section class="nim-panel h-100" data-section="<?= e($id) ?>" data-status="<?= e($worst) ?>" aria-labelledby="h-<?= e($id) ?>">
                <div class="d-flex align-items-center gap-2 px-3 pt-3 pb-2">
                    <i class="bi bi-<?= e($sIcon) ?>" aria-hidden="true"></i>
                    <h2 class="h6 mb-0 me-auto" id="h-<?= e($id) ?>"><?= e($label) ?></h2>
                    <span class="nim-state <?= e($state[$worst]) ?> small"><?= e($word[$worst]) ?></span>
                </div>
                <ul class="list-unstyled mb-0 small">
                    <?php foreach ($checks as $c): $route = $c['route'] ?? null; ?>
                        <li class="d-flex align-items-start gap-2 px-3 py-2 border-top" data-check-status="<?= e($c['status']) ?>">
                            <i class="bi <?= e($icon[$c['status']] ?? $icon['unknown']) ?> mt-1" aria-hidden="true"></i>
                            <div class="flex-grow-1 min-w-0">
                                <div class="fw-medium"><?= e($c['label']) ?><span class="visually-hidden"> (<?= e($word[$c['status']] ?? $c['status']) ?>)</span></div>
                                <div class="nim-muted text-break"><?= e($c['detail']) ?></div>
                            </div>
                            <?php if ($c['status'] !== 'ok' && $route !== null && $this->hasRoute($route) && $this->routeAllowed($route)): ?>
                                <a class="btn btn-sm btn-outline-primary text-nowrap" href="<?= e($this->url($route)) ?>">Open</a>
                            <?php endif; ?>
                        </li>
                    <?php endforeach; ?>
                    <?php if ($checks === []): ?><li class="px-3 py-2 border-top nim-muted">Nothing to check here.</li><?php endif; ?>
                </ul>
            </section>
        </div>
    <?php endforeach; ?>
</div>
