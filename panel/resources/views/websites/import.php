<?php
/** @var \NimVor\View\View $this */
/** @var array<string, mixed> $found */
/** @var array<string, mixed>|null $plan */
/** @var array<string, mixed>|null $preview */
$blockers = (array) ($preview['blockers'] ?? []);
$ok = $preview !== null && $blockers === [];
$row = (array) ($plan['row'] ?? []);
?>
<?php $this->start(); ?>Import <?= e($found['domain']) ?><?php $this->end('title'); ?>
<?php $this->start(); ?>Move this website under NimVor management<?php $this->end('subtitle'); ?>


<div class="alert alert-info small"><i class="bi bi-info-circle me-1"></i>
    NimVor does not edit configuration it did not create. The import <strong>copies</strong> the website into a new NimVor website, then switches Nginx over in one validated step. The old configuration file and the old files are kept; the import can be undone from the website's overview.
</div>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
<?php if ($planErrors !== null): ?>
    <div class="alert alert-danger"><?php foreach ($planErrors as $k => $m): ?><div><?= e($m) ?></div><?php endforeach; ?></div>
<?php endif; ?>

<?php if ($preview !== null): ?>
<div class="row g-3">
    <div class="col-xl-6">
        <div class="card mb-3">
            <div class="card-header"><i class="bi bi-list-check me-1"></i>Plan</div>
            <ol class="list-group list-group-flush list-group-numbered small">
                <li class="list-group-item">Create account <code><?= e($row['system_user'] ?? '') ?></code> and website folder <code><?= e($row['root'] ?? '') ?></code> (<?= e(\NimVor\Services\WebsiteService::KINDS[$row['kind']] ?? $row['kind']) ?><?= ($row['php_version'] ?? '') !== '' ? ', PHP ' . e($row['php_version']) : '' ?>).</li>
                <li class="list-group-item">Copy <?= (int) $preview['files'] ?> files (<?= e(format_bytes((int) $preview['bytes'])) ?>) from <code><?= e($preview['source']) ?></code> to <code><?= e($preview['target']) ?></code>.
                    <?php if (!empty($preview['skipped_links'])): ?><div class="text-warning">Links pointing outside the website are not copied: <?= e(implode(', ', array_slice((array) $preview['skipped_links'], 0, 5))) ?></div><?php endif; ?></li>
                <?php foreach ((array) $preview['steps'] as $st): ?>
                    <li class="list-group-item">
                        <?php if ($st['action'] === 'unlink'): ?>Disable the old configuration by removing the link <code><?= e($st['path']) ?></code> (the file <code><?= e($st['file']) ?></code> is kept).
                        <?php elseif ($st['action'] === 'move'): ?>Disable <code><?= e($st['file']) ?></code> by moving it to <code><?= e($st['stored']) ?></code>.
                        <?php else: ?>Ignore <code><?= e($st['file']) ?></code> (already disabled).<?php endif; ?>
                    </li>
                <?php endforeach; ?>
                <li class="list-group-item">Enable the NimVor configuration below, test it with <code>nginx -t</code> and reload. If anything fails, the old configuration is put back.</li>
                <?php if (!empty($preview['database'])): ?><li class="list-group-item">Keep using the database <code><?= e($preview['database']) ?></code> (found in <code><?= e($preview['database_source']) ?></code>); it is not changed and will be listed under Databases.</li><?php endif; ?>
            </ol>
        </div>
        <?php foreach ((array) $preview['warnings'] as $w): ?><div class="alert alert-warning small"><?= e($w) ?></div><?php endforeach; ?>
        <?php if ($blockers !== []): ?>
            <div class="alert alert-danger"><div class="fw-semibold mb-1">The import cannot run yet:</div><ul class="mb-0 small"><?php foreach ($blockers as $b): ?><li><?= e($b) ?></li><?php endforeach; ?></ul></div>
        <?php endif; ?>
        <div class="card">
            <div class="card-header"><i class="bi bi-sliders me-1"></i>Options</div>
            <div class="card-body">
                <form method="get" class="row g-2 align-items-end mb-3">
                    <div class="col-sm-6"><label class="form-label small" for="i-user">Account name</label><input class="form-control form-control-sm" id="i-user" name="system_user" value="<?= e($row['system_user'] ?? '') ?>"></div>
                    <div class="col-sm-4"><label class="form-label small" for="i-php">PHP version</label><input class="form-control form-control-sm" id="i-php" name="php_version" value="<?= e($row['php_version'] ?? '') ?>"></div>
                    <div class="col-sm-2"><button class="btn btn-sm btn-outline-secondary w-100">Update plan</button></div>
                </form>
                <form method="post" action="<?= e($this->url('websites.import.run', ['id' => $found['id']])) ?>" data-nim-busy>
                    <?= $this->csrfField() ?>
                    <input type="hidden" name="system_user" value="<?= e($row['system_user'] ?? '') ?>">
                    <input type="hidden" name="php_version" value="<?= e($row['php_version'] ?? '') ?>">
                    <?php if ($owners !== []): ?>
                        <label class="form-label small" for="i-owner">Owner</label>
                        <select class="form-select form-select-sm mb-2" id="i-owner" name="owner_id"><option value="">No owner (administrators)</option><?php foreach ($owners as $o): ?><option value="<?= (int) $o['id'] ?>"><?= e($o['username']) ?></option><?php endforeach; ?></select>
                    <?php endif; ?>
                    <div class="form-check mb-3"><input class="form-check-input" type="checkbox" name="confirm" value="1" id="i-confirm" required<?= $ok ? '' : ' disabled' ?>><label class="form-check-label" for="i-confirm">I reviewed the plan and the new configuration.</label></div>
                    <button class="btn btn-primary"<?= $ok ? '' : ' disabled' ?>><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Import website</button>
                </form>
            </div>
        </div>
    </div>
    <div class="col-xl-6">
        <div class="card mb-3"><div class="card-header">New configuration (generated by NimVor)</div><pre class="nim-pre mb-0 small nim-scroll-400"><?= e((string) $preview['new_config']) ?></pre></div>
        <div class="card"><div class="card-header">Current configuration (kept, disabled)</div><pre class="nim-pre mb-0 small nim-scroll-400"><?= e((string) $preview['old_config']) ?></pre></div>
    </div>
</div>
<?php endif; ?>
