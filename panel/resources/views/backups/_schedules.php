<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $schedules */
/** @var string|null $deleteRoute  null: read-only */
/** @var array<string, int> $routeParams */
use WhaCP\Services\BackupScheduler;

$describe = static function (array $s): string {
    $when = sprintf('%02d:00', (int) $s['hour']);
    return $s['frequency'] === 'weekly' ? 'Every ' . BackupScheduler::WEEKDAYS[(int) $s['weekday']] . ' at ' . $when : 'Every day at ' . $when;
};
?>
<?php if ($schedules === []): ?>
    <p class="text-body-secondary small">No schedule.</p>
<?php else: ?>
    <ul class="list-group list-group-flush mb-3">
        <?php foreach ($schedules as $s): ?>
            <li class="list-group-item px-0 d-flex align-items-start gap-2">
                <div class="small">
                    <div class="fw-semibold"><?= e($describe($s)) ?></div>
                    <div class="text-body-secondary">
                        <?php if ($s['kind'] === 'site'): ?><?= (int) $s['include_files'] ? 'files' : '' ?><?= (int) $s['include_files'] && (int) $s['include_databases'] ? ' + ' : '' ?><?= (int) $s['include_databases'] ? 'databases' : '' ?> · <?php endif; ?>
                        keep <?= (int) $s['retention'] ?> · <?= (int) $s['encrypt'] ? 'encrypted' : 'not encrypted' ?><?= $s['storage_name'] !== null ? ' · copy to ' . e($s['storage_name']) : '' ?>
                    </div>
                    <div class="text-body-secondary">Last run: <?= $s['last_run_at'] === null ? 'never' : e(substr((string) $s['last_run_at'], 0, 16)) . ' (' . e($s['last_status']) . ')' ?></div>
                </div>
                <?php if ($deleteRoute !== null): ?>
                <form class="ms-auto" method="post" action="<?= e($this->url($deleteRoute, $routeParams + ['schedule' => (int) $s['id']])) ?>" data-wcp-confirm="Remove this schedule? Existing backups are kept."><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Remove"><i class="bi bi-trash"></i></button></form>
                <?php endif; ?>
            </li>
        <?php endforeach; ?>
    </ul>
<?php endif; ?>
