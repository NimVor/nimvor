<?php
/** @var \NimVor\View\View $this */
$icons = ['error' => 'x-octagon-fill text-danger', 'warning' => 'exclamation-triangle-fill text-warning', 'success' => 'check-circle-fill text-success', 'info' => 'info-circle-fill text-info'];
$labels = ['error' => 'Failures', 'warning' => 'Warnings', 'success' => 'Finished', 'info' => 'Information'];
$showAll = empty($filter['unread']);
$base = array_filter([
    'show' => $showAll ? 'all' : '',
    'severity' => (string) ($filter['severity'] ?? ''),
    'website' => isset($filter['website_id']) ? (string) $filter['website_id'] : '',
]);
?>
<?php $this->start(); ?>Notifications<?php $this->end('title'); ?>
<?php $this->start(); ?><?= (int) $unread ?> unread · results of background jobs and alerts, kept <?= \NimVor\Services\NotificationService::RETENTION_DAYS ?> days<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<?php if ($unread > 0): ?>
    <form method="post" action="<?= e($this->url('notifications.read')) ?>">
        <?= $this->csrfField() ?>
        <input type="hidden" name="all" value="1">
        <?php foreach ($base as $k => $v): ?><input type="hidden" name="<?= e($k) ?>" value="<?= e($v) ?>"><?php endforeach; ?>
        <button class="btn btn-sm btn-outline-secondary" type="submit"><i class="bi bi-check2-all me-1"></i>Mark all read</button>
    </form>
<?php endif; ?>
<?php $this->end('actions'); ?>

<div class="card mb-3">
    <div class="card-header d-flex flex-wrap gap-2 align-items-center">
        <ul class="nav nav-pills nav-sm me-auto">
            <li class="nav-item"><a class="nav-link py-1<?= $showAll ? '' : ' active' ?>" href="<?= e($this->url('notifications', [], array_diff_key($base, ['show' => 1]))) ?>">Unread</a></li>
            <li class="nav-item"><a class="nav-link py-1<?= $showAll ? ' active' : '' ?>" href="<?= e($this->url('notifications', [], ['show' => 'all'] + $base)) ?>">All</a></li>
        </ul>
        <form class="d-flex flex-wrap gap-2" method="get" action="<?= e($this->url('notifications')) ?>">
            <?php if ($showAll): ?><input type="hidden" name="show" value="all"><?php endif; ?>
            <select class="form-select form-select-sm w-auto" name="severity" aria-label="Kind" data-nim-autosubmit>
                <option value="">Any kind</option>
                <?php foreach ($labels as $sev => $label): ?>
                    <option value="<?= e($sev) ?>"<?= ($filter['severity'] ?? '') === $sev ? ' selected' : '' ?>><?= e($label) ?></option>
                <?php endforeach; ?>
            </select>
            <?php if ($websites !== []): ?>
                <select class="form-select form-select-sm w-auto" name="website" aria-label="Website" data-nim-autosubmit>
                    <option value="">All websites</option>
                    <?php foreach ($websites as $w): ?>
                        <option value="<?= (int) $w['id'] ?>"<?= (int) ($filter['website_id'] ?? 0) === (int) $w['id'] ? ' selected' : '' ?>><?= e($w['domain']) ?></option>
                    <?php endforeach; ?>
                </select>
            <?php endif; ?>
            <noscript><button class="btn btn-sm btn-outline-secondary" type="submit">Filter</button></noscript>
        </form>
    </div>
    <?php if ($rows === []): ?>
        <div class="card-body text-center py-5 text-body-secondary nim-empty">
            <i class="bi bi-bell display-6"></i>
            <p class="mt-3 mb-0"><?= $showAll ? 'No notifications match.' : 'You are all caught up.' ?></p>
        </div>
    <?php else: ?>
        <ul class="list-group list-group-flush" data-nim-notifications>
            <?php foreach ($rows as $n): ?>
                <li class="list-group-item d-flex gap-3 align-items-start<?= $n['is_read'] ? '' : ' nim-unread' ?>">
                    <i class="bi bi-<?= e($icons[$n['severity']] ?? $icons['info']) ?> fs-5 mt-1" aria-label="<?= e($labels[$n['severity']] ?? 'Information') ?>"></i>
                    <div class="flex-grow-1 min-w-0">
                        <div class="d-flex flex-wrap gap-2 align-items-baseline">
                            <strong class="nim-wrap"><?= e($n['title']) ?></strong>
                            <?php if (!$n['is_read']): ?><span class="badge text-bg-primary">new</span><?php endif; ?>
                        </div>
                        <div class="small text-body-secondary">
                            <?= e(gmdate('Y-m-d H:i', (int) strtotime((string) $n['created_at']))) ?> UTC
                            <?php if ($n['domain'] !== null): ?> · <?= e($n['domain']) ?><?php endif; ?>
                            <?php if (($n['server_name'] ?? '') !== '' && (int) $n['server_id'] !== 1): ?> · <?= e($n['server_name']) ?><?php endif; ?>
                        </div>
                        <?php if ($n['body'] !== ''): ?>
                            <div class="small mt-1 nim-wrap"><?= nl2br(e($n['body'])) ?></div>
                        <?php endif; ?>
                    </div>
                    <div class="d-flex flex-wrap gap-1 justify-content-end">
                        <?php if ($n['job_id'] !== ''): ?>
                            <button class="btn btn-sm btn-outline-secondary" type="button" data-nim-job-open="<?= e($n['job_id']) ?>"><i class="bi bi-terminal me-1"></i>Log</button>
                        <?php endif; ?>
                        <?php if (str_starts_with((string) $n['link'], '/') && !str_starts_with((string) $n['link'], '//')): ?>
                            <a class="btn btn-sm btn-outline-secondary" href="<?= e($n['link']) ?>">Open</a>
                        <?php endif; ?>
                        <?php if (!$n['is_read']): ?>
                            <form method="post" action="<?= e($this->url('notifications.read')) ?>">
                                <?= $this->csrfField() ?>
                                <input type="hidden" name="ids[]" value="<?= (int) $n['id'] ?>">
                                <?php foreach ($base as $k => $v): ?><input type="hidden" name="<?= e($k) ?>" value="<?= e($v) ?>"><?php endforeach; ?>
                                <button class="btn btn-sm btn-outline-secondary" type="submit" aria-label="Mark read"><i class="bi bi-check2"></i></button>
                            </form>
                        <?php endif; ?>
                    </div>
                </li>
            <?php endforeach; ?>
        </ul>
    <?php endif; ?>
    <?php if ($pages > 1): ?>
        <div class="card-footer">
            <nav aria-label="Notification pages"><ul class="pagination pagination-sm mb-0">
                <li class="page-item<?= $page <= 1 ? ' disabled' : '' ?>"><a class="page-link" href="<?= e($this->url('notifications', [], $base + ['page' => $page - 1])) ?>">Previous</a></li>
                <li class="page-item disabled"><span class="page-link">Page <?= (int) $page ?> of <?= (int) $pages ?></span></li>
                <li class="page-item<?= $page >= $pages ? ' disabled' : '' ?>"><a class="page-link" href="<?= e($this->url('notifications', [], $base + ['page' => $page + 1])) ?>">Next</a></li>
            </ul></nav>
        </div>
    <?php endif; ?>
</div>

<div class="card">
    <div class="card-body">
        <form method="post" action="<?= e($this->url('notifications.email')) ?>" class="d-flex flex-wrap gap-3 align-items-center">
            <?= $this->csrfField() ?>
            <input type="hidden" name="notify_email" value="0">
            <div class="form-check form-switch mb-0">
                <input class="form-check-input" type="checkbox" role="switch" id="notify_email" name="notify_email" value="1"<?= $emailOn ? ' checked' : '' ?>>
                <label class="form-check-label" for="notify_email">Email me about failures and alerts I can see (<?= e($user['email']) ?>)</label>
            </div>
            <button class="btn btn-sm btn-outline-secondary" type="submit">Save</button>
        </form>
    </div>
</div>
