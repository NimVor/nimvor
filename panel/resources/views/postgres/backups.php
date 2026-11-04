<?php
/**
 * Backups of one PostgreSQL database: take one now, restore or delete an archive, and automatic backups.
 *
 * @var \NimVor\View\View $this
 * @var array<string, mixed> $db
 * @var list<array{file: string, size: int, created: string, sha256: string}> $backups
 * @var \Throwable|null $error
 * @var array<string, mixed>|null $schedule
 * @var bool $canManage
 */
$id = (int) $db['id'];
?>
<?php $this->start(); ?><?= e((string) $db['name']) ?> · Backups<?php $this->end('title'); ?>
<?php $this->start(); ?>PostgreSQL backups<?php $this->end('subtitle'); ?>

<p class="mb-2 d-flex gap-3"><a href="<?= e($this->url('postgres')) ?>" class="small"><i class="bi bi-arrow-left"></i> PostgreSQL</a></p>

<?php if ($error !== null): ?>
    <div class="alert alert-warning small">The backups could not be listed (<?= e($error->getMessage()) ?>).</div>
<?php endif; ?>

<div class="card mb-3">
    <div class="card-header d-flex align-items-center gap-2 py-2">
        <span class="fw-medium">Backups of <span class="font-monospace"><?= e((string) $db['name']) ?></span></span>
        <span class="me-auto"></span>
        <?php if ($canManage): ?>
            <form method="post" action="<?= e($this->url('postgres.backup_create')) ?>" data-nim-busy>
                <?= $this->csrfField() ?><input type="hidden" name="db" value="<?= $id ?>">
                <button class="btn btn-sm btn-primary"><i class="bi bi-cloud-arrow-down me-1"></i>Back up now</button>
            </form>
        <?php endif; ?>
    </div>
    <div class="table-responsive">
        <table class="table table-sm align-middle mb-0 small">
            <thead><tr><th>Taken</th><th>File</th><th class="text-end">Size</th><th>Checksum</th><th></th></tr></thead>
            <tbody>
            <?php if (!$backups): ?>
                <tr><td colspan="5" class="p-3 text-body-secondary">No backups yet.</td></tr>
            <?php endif; ?>
            <?php foreach ($backups as $b): ?>
                <tr>
                    <td class="text-nowrap"><?= e(str_replace('T', ' ', substr($b['created'], 0, 19))) ?> UTC</td>
                    <td class="font-monospace text-break"><?= e($b['file']) ?></td>
                    <td class="text-end text-nowrap"><?= e(format_bytes($b['size'])) ?></td>
                    <td class="font-monospace text-body-secondary" title="SHA-256 <?= e($b['sha256']) ?>"><?= e(substr($b['sha256'], 0, 12)) ?></td>
                    <td class="text-end text-nowrap">
                        <?php if ($canManage): ?>
                            <form method="post" action="<?= e($this->url('postgres.backup_restore')) ?>" class="d-inline" data-nim-busy data-nim-confirm="Restore <?= e((string) $db['name']) ?> from this backup? Its current contents are replaced (a safety backup of the current state is taken first).">
                                <?= $this->csrfField() ?><input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="file" value="<?= e($b['file']) ?>">
                                <button class="btn btn-sm btn-outline-warning">Restore</button>
                            </form>
                            <form method="post" action="<?= e($this->url('postgres.backup_delete')) ?>" class="d-inline" data-nim-busy data-nim-confirm="Delete this backup?">
                                <?= $this->csrfField() ?><input type="hidden" name="db" value="<?= $id ?>"><input type="hidden" name="file" value="<?= e($b['file']) ?>">
                                <button class="btn btn-sm btn-outline-danger">Delete</button>
                            </form>
                        <?php endif; ?>
                    </td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    </div>
    <div class="card-footer small text-body-secondary">A restore replaces the database's tables with the backup's. Access given to other users is kept. The archives are stored on the server in the backup folder, readable by administrators only.</div>
</div>

<?php if ($canManage): ?>
<div class="card">
    <div class="card-header py-2 fw-medium">Automatic backups</div>
    <div class="card-body">
        <form method="post" action="<?= e($this->url('postgres.backup_schedule')) ?>" class="row g-2 align-items-end">
            <?= $this->csrfField() ?><input type="hidden" name="db" value="<?= $id ?>">
            <div class="col-auto"><label class="form-label small mb-1" for="pgb-freq">How often</label>
                <select class="form-select form-select-sm" id="pgb-freq" name="frequency">
                    <option value="daily"<?= ($schedule['frequency'] ?? 'daily') === 'daily' ? ' selected' : '' ?>>Every day</option>
                    <option value="weekly"<?= ($schedule['frequency'] ?? '') === 'weekly' ? ' selected' : '' ?>>Every Sunday</option>
                </select></div>
            <div class="col-auto"><label class="form-label small mb-1" for="pgb-hour">At (UTC)</label>
                <select class="form-select form-select-sm" id="pgb-hour" name="hour"><?php for ($h = 0; $h < 24; $h++): ?><option value="<?= $h ?>"<?= (int) ($schedule['hour'] ?? 3) === $h ? ' selected' : '' ?>><?= sprintf('%02d:00', $h) ?></option><?php endfor; ?></select></div>
            <div class="col-auto"><label class="form-label small mb-1" for="pgb-keep">Keep the newest</label>
                <input type="number" class="form-control form-control-sm nim-keep-input" id="pgb-keep" name="keep" min="1" max="90" value="<?= (int) ($schedule['keep'] ?? 7) ?>"></div>
            <div class="col-auto"><button class="btn btn-sm btn-primary">Save</button></div>
        </form>
        <?php if ($schedule !== null): ?>
            <div class="d-flex align-items-center gap-2 mt-3 small text-body-secondary">
                <span>On: <?= e((string) $schedule['frequency']) ?> at <?= sprintf('%02d:00', (int) $schedule['hour']) ?> UTC, keeping <?= (int) $schedule['keep'] ?>.<?= $schedule['last_run_at'] ? ' Last run ' . e(str_replace('T', ' ', substr((string) $schedule['last_run_at'], 0, 16))) . ' UTC (' . e((string) $schedule['last_status']) . ').' : ' Not run yet.' ?></span>
                <form method="post" action="<?= e($this->url('postgres.backup_schedule_delete')) ?>" data-nim-confirm="Turn off automatic backups?">
                    <?= $this->csrfField() ?><input type="hidden" name="db" value="<?= $id ?>"><button class="btn btn-sm btn-outline-secondary">Turn off</button>
                </form>
            </div>
        <?php endif; ?>
        <p class="small text-body-secondary mt-3 mb-0">Older backups beyond the number to keep are removed before the next one is taken, so up to one extra archive is kept until the next run.</p>
    </div>
</div>
<?php endif; ?>
