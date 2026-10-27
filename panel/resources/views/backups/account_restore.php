<?php
/**
 * Restoring an account from an account backup file: what the file holds
 * and what a restore does here, then the import, the recreation and its
 * report.
 *
 * @var \WhaCP\View\View $this
 * @var string $path
 * @var array<string, mixed> $manifest
 * @var array<string, mixed> $plan
 * @var array<string, mixed>|null $restore
 * @var string $importState
 * @var list<array{what: string, name: string, password: string}> $secrets
 */
$meta = (array) ($manifest['meta'] ?? []);
$u = (array) ($meta['user'] ?? []);
$username = (string) ($manifest['username'] ?? ($u['username'] ?? ''));
$websites = (array) ($meta['websites'] ?? []);
$state = static fn (string $s): string => match ($s) {
    'create' => '<span class="wcp-state is-ok">will be created</span>',
    'existing' => '<span class="wcp-state is-warn">exists in the account: restored into it</span>',
    default => '<span class="wcp-state is-error">belongs to someone else: skipped</span>',
};
$secrets ??= [];
$importState ??= '';
$status = (string) ($restore['status'] ?? '');
// What a restore would do, until it is done.
$showPlan = $restore === null || in_array($status, ['importing', 'imported'], true);
?>
<?php $this->start(); ?>Restore the account <?= e($username) ?><?php $this->end('title'); ?>
<?php $this->start(); ?><?= e(basename($path)) ?><?php $this->end('subtitle'); ?>

<?php if ($secrets !== []): ?>
<div class="alert alert-warning">
    <div class="fw-semibold mb-1"><i class="bi bi-key me-1"></i>New passwords: copy them now, they are not shown again</div>
    <table class="table table-sm mb-0 small">
        <tbody>
        <?php foreach ($secrets as $s): ?>
            <tr><td><?= e($s['what']) ?></td><td class="font-monospace"><?= e($s['name']) ?></td><td class="font-monospace text-break" data-wcp-secret><?= e($s['password']) ?></td></tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</div>
<?php endif; ?>

<div class="row g-3">
    <div class="col-xl-7">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-person-vcard me-1"></i>In the file</div>
            <div class="card-body">
                <dl class="wcp-dl mb-0">
                    <dt>Account</dt><dd><?= e($username) ?> <span class="wcp-muted">(<?= e((string) ($u['role'] ?? 'customer')) ?><?= !empty($u['plan']) ? ', plan ' . e((string) $u['plan']) : '' ?><?= !empty($u['parent']) ? ', reseller ' . e((string) $u['parent']) : '' ?>)</span>
                        <?php if ($showPlan): ?><div><?= $plan['account'] === 'create' ? '<span class="wcp-state is-ok">will be created</span>' : ($plan['account'] === 'existing' ? '<span class="wcp-state is-warn">exists: its websites are restored into it</span>' : '<span class="wcp-state is-error">a staff user has this name: cannot be restored</span>') ?></div><?php endif; ?></dd>
                    <dt>Made</dt><dd><?= e(substr((string) ($manifest['created_at'] ?? ''), 0, 16)) ?><?= !empty($manifest['label']) ? ' · ' . e((string) $manifest['label']) : '' ?></dd>
                    <dt>Backup key</dt><dd><code><?= e((string) ($manifest['key_id'] ?? '')) ?></code></dd>
                    <?php if (!$plan['plan_found']): ?><dt>Plan</dt><dd><span class="wcp-state is-warn">"<?= e((string) $u['plan']) ?>" does not exist here: the account gets no plan</span></dd><?php endif; ?>
                </dl>
            </div>
        </div>
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-globe2 me-1"></i>Websites <span class="badge text-bg-light border"><?= count($websites) ?></span></div>
            <div class="table-responsive"><table class="table table-sm align-middle mb-0">
                <thead><tr><th>Website</th><th>Type</th><th>Databases</th><?php if ($showPlan): ?><th>Here</th><?php endif; ?></tr></thead>
                <tbody>
                <?php foreach ($websites as $w): ?>
                    <tr><td class="fw-semibold"><?= e((string) $w['domain']) ?></td>
                        <td class="small"><?= e((string) ($w['type'] ?? 'main')) ?><?= ($w['kind'] ?? '') === 'php' ? ' · PHP ' . e((string) $w['php_version']) : ' · ' . e((string) ($w['kind'] ?? '')) ?></td>
                        <td class="small"><?= e(implode(', ', (array) ($w['databases'] ?? [])) ?: '—') ?></td>
                        <?php if ($showPlan): ?><td class="small"><?= $state((string) ($plan['websites'][$w['domain']] ?? 'create')) ?></td><?php endif; ?></tr>
                <?php endforeach; ?>
                </tbody>
            </table></div>
        </div>
        <div class="card">
            <div class="card-header"><i class="bi bi-list-check me-1"></i>Also in the file</div>
            <div class="card-body small">
                <div><?= count((array) ($meta['databases'] ?? [])) ?> database(s)<?php foreach ($showPlan ? (array) ($plan['databases'] ?? []) : [] as $name => $s): if ($s === 'existing'): ?> · <span class="wcp-state is-warn"><?= e((string) $name) ?> exists</span><?php endif; endforeach; ?>,
                    <?= count((array) ($meta['database_users'] ?? [])) ?> database user(s) (with new passwords),
                    <?= count((array) ($meta['dns_zones'] ?? [])) ?> DNS zone(s), <?= count((array) ($meta['cron_jobs'] ?? [])) ?> cron job(s).</div>
                <div class="wcp-muted mt-1">Not in account backups: email (mailboxes and messages), SSL certificates (AutoSSL issues them again once the domains point here) and FTP accounts. The account gets a new panel password.</div>
            </div>
        </div>
    </div>
    <div class="col-xl-5">
        <?php if ($restore === null): ?>
            <form class="card" method="post" action="<?= e($this->url('backups.accounts.restore')) ?>" data-wcp-busy>
                <?= $this->csrfField() ?>
                <input type="hidden" name="path" value="<?= e($path) ?>">
                <div class="card-header"><i class="bi bi-arrow-counterclockwise me-1"></i>Restore</div>
                <div class="card-body">
                    <ol class="small ps-3">
                        <li>The file is checked (backup key, authenticated description, every part's checksum) and its website backups are put in place.</li>
                        <li>Then you recreate the account: its websites, databases, users, DNS zones and cron jobs, and each website's files and databases are restored.</li>
                    </ol>
                    <label class="form-label small" for="ar-confirm">Type <strong><?= e($username) ?></strong> to confirm</label>
                    <input class="form-control" id="ar-confirm" name="confirm" autocomplete="off" required<?= $plan['account'] === 'conflict' ? ' disabled' : '' ?>>
                </div>
                <div class="card-footer d-flex justify-content-end gap-2">
                    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('backups')) ?>#accounts">Cancel</a>
                    <button class="btn btn-sm btn-primary"<?= $plan['account'] === 'conflict' ? ' disabled' : '' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Check and import</button>
                </div>
            </form>
        <?php else: ?>
            <div class="card">
                <div class="card-header"><i class="bi bi-arrow-counterclockwise me-1"></i>Restore</div>
                <div class="card-body">
                    <?php if ($status === 'importing'): ?>
                        <p class="mb-2"><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Checking the file and importing its website backups…</p>
                        <button type="button" class="btn btn-sm btn-outline-secondary" data-wcp-job-open="<?= e((string) $restore['job_id']) ?>">Show the log</button>
                        <span data-wcp-reload-when-done="<?= e((string) $restore['job_id']) ?>"></span>
                    <?php elseif ($status === 'imported'): ?>
                        <p class="mb-2"><span class="wcp-state is-ok">File checked and imported</span></p>
                        <form method="post" action="<?= e($this->url('backups.accounts.recreate', ['id' => (int) $restore['id']])) ?>" data-wcp-busy data-wcp-confirm="Recreate the account <?= e($username) ?> and restore its websites now?">
                            <?= $this->csrfField() ?>
                            <button class="btn btn-primary"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Recreate the account and restore its websites</button>
                        </form>
                    <?php elseif ($status === 'restoring'): ?>
                        <p class="mb-0"><span class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>Recreating the account…</p>
                    <?php endif; ?>
                    <?php if (in_array($status, ['done', 'failed'], true)): ?>
                        <p class="mb-2"><?= $status === 'done' ? '<span class="wcp-state is-ok">Done</span>' : '<span class="wcp-state is-error">Failed</span>' ?> <span class="small wcp-muted"><?= e(substr((string) $restore['updated_at'], 0, 16)) ?></span></p>
                        <ul class="list-unstyled small mb-0 wcp-restore-report">
                            <?php foreach ((array) $restore['report'] as $line): $lv = (string) ($line['level'] ?? 'info'); ?>
                                <li class="mb-1"><i class="bi <?= ['ok' => 'bi-check-circle text-success', 'warning' => 'bi-exclamation-triangle text-warning', 'error' => 'bi-x-circle text-danger'][$lv] ?? 'bi-info-circle text-body-secondary' ?> me-1"></i><?= e((string) ($line['text'] ?? '')) ?></li>
                            <?php endforeach; ?>
                        </ul>
                        <?php if ($status === 'done'): ?>
                            <div class="mt-3 d-flex flex-wrap gap-2"><a class="btn btn-sm btn-outline-primary" href="<?= e($this->url('users')) ?>">Users</a><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('websites')) ?>">Websites</a></div>
                            <p class="small wcp-muted mt-2 mb-0">The websites' files and databases are restored by jobs (see the notifications). Point the domains to this server; AutoSSL then issues their certificates.</p>
                        <?php endif; ?>
                    <?php endif; ?>
                </div>
            </div>
        <?php endif; ?>
    </div>
</div>
