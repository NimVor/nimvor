<?php
/**
 * Shared renderer for a list of site security/health checks.
 *
 * @var \WhaCP\View\View $this
 * @var array<string, mixed> $site
 * @var list<array{level: string, title: string, detail: string, fix: string, where: string}> $checks
 */
$icon = ['ok' => 'bi-check-circle-fill text-success', 'warn' => 'bi-exclamation-triangle-fill text-warning', 'bad' => 'bi-x-octagon-fill text-danger'];
$problems = array_values(array_filter($checks, static fn (array $c): bool => $c['level'] !== 'ok'));
?>
<div class="wcp-infobar mb-3">
    <i class="bi <?= $problems === [] ? 'bi-shield-check text-success' : 'bi-shield-exclamation text-warning' ?>" aria-hidden="true"></i>
    <span><?= $problems === []
        ? 'Everything here looks good.'
        : count($problems) . ' item(s) need attention — each one explains how to fix it below.' ?></span>
</div>

<div class="card">
    <ul class="list-group list-group-flush">
        <?php foreach ($checks as $c): ?>
            <li class="list-group-item d-flex align-items-start gap-3 py-3">
                <i class="bi <?= $icon[$c['level']] ?? 'bi-dash-circle' ?> fs-5 mt-1" aria-hidden="true"></i>
                <div class="flex-grow-1 min-w-0">
                    <div class="fw-medium"><?= e($c['title']) ?></div>
                    <?php if ($c['detail'] !== ''): ?><div class="small text-body-secondary"><?= e($c['detail']) ?></div><?php endif; ?>
                    <?php if ($c['fix'] !== ''): ?>
                        <div class="small mt-1"><i class="bi bi-wrench-adjustable me-1" aria-hidden="true"></i><?= e($c['fix']) ?></div>
                    <?php endif; ?>
                </div>
                <?php if ($c['level'] !== 'ok'): ?>
                    <span class="badge rounded-pill <?= $c['where'] === 'you' ? 'text-bg-primary' : 'text-bg-secondary' ?> align-self-center text-nowrap">
                        <?= $c['where'] === 'you' ? 'You can fix this' : 'Ask your provider' ?>
                    </span>
                <?php endif; ?>
            </li>
        <?php endforeach; ?>
    </ul>
</div>
