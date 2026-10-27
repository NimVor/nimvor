<?php
/**
 * PHP Manager: choose the PHP versions and extensions, then review.
 *
 * @var \NimVor\View\View $this
 * @var list<string> $versions
 * @var list<string> $available
 * @var array{installed: list<string>, extensions: array<string, list<string>>, usage: array<string, int>} $current
 * @var list<array{name: string, description: string}> $catalog
 * @var array{versions: list<string>, extensions: list<string>} $selection
 * @var array<string, mixed> $saved
 * @var array<string, string> $eol
 */
?>
<?php $this->start(); ?>PHP Manager<?php $this->end('title'); ?>
<?php $this->start(); ?>Choose the PHP versions and extensions this server should have, review the changes, apply<?php $this->end('subtitle'); ?>
<?php if (isset($agentError)): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError]) ?>
<?php return; endif; ?>
<?php $this->start(); ?>
    <?php if (!empty($saved['versions'])): ?><a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('php.manager.export')) ?>" data-nim-noajax><i class="bi bi-download me-1" aria-hidden="true"></i>Export profile</a><?php endif; ?>
    <button class="btn btn-sm btn-outline-secondary" type="button" data-bs-toggle="modal" data-bs-target="#php-import"><i class="bi bi-upload me-1" aria-hidden="true"></i>Import profile</button>
    <a class="btn btn-sm btn-outline-secondary" href="<?= e($this->url('php')) ?>"><i class="bi bi-filetype-php me-1" aria-hidden="true"></i>PHP versions</a>
<?php $this->end('actions'); ?>

<div class="nim-panel mb-3">
    <div class="nim-statusbar">
        <span class="fw-medium">Installed</span>
        <span><?= $current['installed'] === [] ? '<span class="nim-muted">No PHP version with PHP-FPM yet</span>' : e('PHP ' . implode(', ', $current['installed'])) ?></span>
        <?php if (!empty($saved['applied_at'])): ?><span class="small nim-muted">Profile last applied <?= e((string) $saved['applied_at']) ?> UTC<?php if (!empty($saved['job_id'])): ?> · <button type="button" class="btn btn-link btn-sm p-0 align-baseline" data-nim-job-open="<?= e((string) $saved['job_id']) ?>">its log</button><?php endif; ?></span>
        <?php else: ?><span class="small nim-muted">No profile applied yet: the selection below is what the server has.</span><?php endif; ?>
    </div>
</div>

<form method="post" action="<?= e($this->url('php.manager.review')) ?>" data-nim-noajax>
    <?= $this->csrfField() ?>
    <div class="nim-subtabs mb-3" role="tablist">
        <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#pm-versions" aria-controls="pm-versions" aria-selected="true">1. PHP versions</button>
        <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#pm-extensions" aria-controls="pm-extensions" aria-selected="false">2. Extensions</button>
    </div>
    <div class="tab-content">
        <div class="tab-pane fade show active" id="pm-versions" role="tabpanel">
            <div class="nim-panel">
                <div class="nim-dtable table-responsive">
                    <table class="table align-middle mb-0">
                        <thead><tr><th class="nim-w-check"></th><th>Version</th><th>On this server</th><th>Websites</th><th>Security fixes</th></tr></thead>
                        <tbody>
                        <?php foreach ($versions as $v):
                            $inst = in_array($v, $current['installed'], true);
                            $offered = in_array($v, $available, true);
                            $end = $eol[$v] ?? null;
                            $ended = $end !== null && $end < gmdate('Y-m-d');
                            $used = $current['usage'][$v] ?? 0;
                            ?>
                            <tr data-version="<?= e($v) ?>">
                                <td><input class="form-check-input" type="checkbox" name="versions[]" value="<?= e($v) ?>" id="pv-<?= e($v) ?>" aria-label="PHP <?= e($v) ?>"<?= in_array($v, $selection['versions'], true) ? ' checked' : '' ?><?= !$inst && !$offered ? ' disabled' : '' ?>></td>
                                <td><label class="fw-semibold" for="pv-<?= e($v) ?>">PHP <?= e($v) ?></label></td>
                                <td class="small"><?= $inst ? '<span class="nim-state is-ok">Installed</span>' : ($offered ? '<span class="nim-muted">Can be installed</span>' : '<span class="nim-muted">Not offered by the repositories</span>') ?></td>
                                <td class="small"><?= $used > 0 ? e((string) $used) . ' (cannot be removed)' : '<span class="nim-muted">—</span>' ?></td>
                                <td class="small"><?php if ($end === null): ?><span class="nim-muted">—</span><?php elseif ($ended): ?><span class="nim-state is-error">Ended <?= e($end) ?></span><?php else: ?><span class="nim-state is-ok">Until <?= e($end) ?></span><?php endif; ?></td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
                <div class="small nim-muted p-3">Unticking an installed version removes it, unless websites or NimVor's panel use it (those are kept). More versions: add the PHP repository on the <a href="<?= e($this->url('php')) ?>">PHP page</a>.</div>
            </div>
        </div>
        <div class="tab-pane fade" id="pm-extensions" role="tabpanel">
            <div class="nim-panel">
                <div class="nim-dtable table-responsive">
                    <table class="table align-middle mb-0">
                        <thead><tr><th class="nim-w-check"></th><th>Extension</th><th>Installed and on for</th></tr></thead>
                        <tbody>
                        <?php foreach ($catalog as $x): $n = (string) $x['name'];
                            $on = array_keys(array_filter($current['extensions'], static fn (array $l): bool => in_array($n, $l, true))); ?>
                            <tr data-extension="<?= e($n) ?>">
                                <td><input class="form-check-input" type="checkbox" name="extensions[]" value="<?= e($n) ?>" id="px-<?= e($n) ?>" aria-label="<?= e($n) ?>"<?= in_array($n, $selection['extensions'], true) ? ' checked' : '' ?>></td>
                                <td><label for="px-<?= e($n) ?>"><span class="fw-semibold font-monospace"><?= e($n) ?></span> <span class="small nim-muted"><?= e((string) $x['description']) ?></span></label></td>
                                <td class="small"><?= $on === [] ? '<span class="nim-muted">—</span>' : e('PHP ' . implode(', ', array_map('strval', $on))) ?></td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
                <div class="small nim-muted p-3">The ticked extensions are installed and switched on for every ticked version; unticked ones are removed (or switched off when they are part of PHP's base packages). Extensions NimVor's panel needs are always kept.</div>
            </div>
        </div>
    </div>
    <div class="d-flex align-items-center gap-3 mt-3">
        <button class="btn btn-primary"><i class="bi bi-list-check me-1" aria-hidden="true"></i>Review changes</button>
        <span class="small nim-muted">Nothing changes until you apply the review.</span>
    </div>
</form>

<?= $this->modalStart('php-import', 'Import a PHP profile', false, 'modal-md') ?>
<form method="post" enctype="multipart/form-data" action="<?= e($this->url('php.manager.import')) ?>" data-nim-noajax>
    <?= $this->csrfField() ?>
    <p class="small nim-muted">A profile exported from another NimVor server (nimvor-php-profile.json). You see the review first; nothing is applied until you confirm.</p>
    <input class="form-control form-control-sm mb-3" type="file" name="profile" accept=".json,application/json" required>
    <div class="d-flex justify-content-end gap-2">
        <button type="button" class="btn btn-sm btn-outline-secondary" data-bs-dismiss="modal">Cancel</button>
        <button class="btn btn-sm btn-primary">Review</button>
    </div>
</form>
<?= $this->modalEnd() ?>
