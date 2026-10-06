<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $packages */
/** @var array<string, mixed>|null $updates */
/** @var array<string, mixed>|null $health */
$ups = (array) ($updates['updates'] ?? []);
$problems = (array) ($health['problems'] ?? []);
// "installed" without "functional": present but not usable.
$stats = ['installed' => 0, 'pending' => 0, 'failed' => 0];
foreach ($packages as $p) {
    $stats[(string) ($p['state'] ?? '') === 'installed' ? 'failed' : ($p['installed'] ? 'installed' : 'pending')]++;
}
$kinds = ['failed_script' => 'install script failed', 'missing_dependency' => 'missing dependency', 'duplicate' => 'duplicate version', 'half_configured' => 'not fully installed'];
?>
<?php // The install queue polls on its own: forms here submit normally. ?>
<?php $this->start(); ?>off<?php $this->end('ajax'); ?>
<?php $this->start(); ?>Packages<?php $this->end('title'); ?>
<?php $this->start(); ?>Components and system updates<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<form method="post" action="<?= e($this->url('packages.refresh')) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat"></i> Check for updates</button></form>
<button type="button" class="btn btn-sm btn-primary" data-wcp-pkg-show><i class="bi bi-list-task me-1"></i>Installation queue <span class="badge text-bg-light" data-wcp-pkg-count>0</span></button>
<?php $this->end('actions'); ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
<?php if ($problems !== []): ?>
<div class="card mb-3 border-warning">
    <div class="card-header d-flex align-items-center gap-2"><i class="bi bi-bandaid"></i>Package problems
        <span class="small text-body-secondary">left by an earlier installation or update that did not finish</span>
        <div class="ms-auto d-flex gap-2">
            <?php if (!empty($health['repairable'])): ?>
                <form method="post" action="<?= e($this->url('packages.repair')) ?>" data-wcp-confirm="Repair now? Packages whose install script failed are reinstalled and missing dependencies installed. Nothing is removed."><?= $this->csrfField() ?><button class="btn btn-sm btn-primary">Repair</button></form>
            <?php endif; ?>
            <?php if (!empty($health['duplicates'])): ?>
                <form method="post" action="<?= e($this->url('packages.repair')) ?>" data-wcp-confirm="Remove the older versions of duplicate packages (dnf remove --duplicates)? This removes packages left by an interrupted update; the newer versions stay installed."><?= $this->csrfField() ?><input type="hidden" name="remove_duplicates" value="1"><button class="btn btn-sm btn-outline-danger">Remove duplicates</button></form>
            <?php endif; ?>
        </div>
    </div>
    <div class="table-responsive wcp-scroll-400"><table class="table table-sm mb-0">
        <thead><tr><th>Package</th><th>Problem</th><th>Details</th></tr></thead>
        <tbody>
        <?php foreach ($problems as $p): ?>
            <tr><td><?= e((string) ($p['package'] ?? '')) ?></td><td><?= e($kinds[$p['kind'] ?? ''] ?? (string) ($p['kind'] ?? '')) ?><?= !empty($p['tried']) ? ' <span class="badge text-bg-secondary">repair tried</span>' : '' ?></td><td class="small text-body-secondary"><?= e((string) ($p['detail'] ?? '')) ?></td></tr>
        <?php endforeach; ?>
        </tbody>
    </table></div>
</div>
<?php endif; ?>
<div class="row g-2 mb-3 wcp-pkg-stats">
    <div class="col-6 col-md-4 col-xl"><div class="wcp-stat <?= $updates !== null && (int) $updates['count'] > 0 ? 'is-warn' : 'is-good' ?>"><div class="wcp-stat-num"><?= $updates !== null ? (int) $updates['count'] : '–' ?></div><div class="wcp-stat-label">Updates pending</div></div></div>
    <div class="col-6 col-md-4 col-xl"><div class="wcp-stat <?= $updates !== null && (int) $updates['security'] > 0 ? 'is-bad' : 'is-good' ?>"><div class="wcp-stat-num"><?= $updates !== null ? (int) $updates['security'] : '–' ?></div><div class="wcp-stat-label">Security updates</div></div></div>
    <div class="col-12 col-md-4 col-xl"><div class="wcp-stat is-plain<?= !empty($updates['index_stale']) ? ' is-warn' : '' ?>"><div class="wcp-stat-num wcp-stat-date"><?= !empty($updates['index_refreshed']) ? e(substr((string) $updates['index_refreshed'], 0, 10)) : '–' ?></div><div class="wcp-stat-label">Package index<?= !empty($updates['index_stale']) ? ' (out of date)' : '' ?></div></div></div>
    <div class="col-6 col-md-3 col-xl"><div class="wcp-stat is-good"><div class="wcp-stat-num" data-wcp-pkg-stat="installed"><?= $stats['installed'] ?></div><div class="wcp-stat-label">Components installed</div></div></div>
    <div class="col-6 col-md-3 col-xl"><div class="wcp-stat is-plain"><div class="wcp-stat-num" data-wcp-pkg-stat="pending"><?= $stats['pending'] ?></div><div class="wcp-stat-label">Not installed</div></div></div>
    <div class="col-6 col-md-3 col-xl"><div class="wcp-stat <?= $stats['failed'] > 0 ? 'is-bad' : 'is-plain' ?>"><div class="wcp-stat-num" data-wcp-pkg-stat="failed"><?= $stats['failed'] ?></div><div class="wcp-stat-label">Failed / not usable</div></div></div>
    <div class="col-6 col-md-3 col-xl"><div class="wcp-stat is-plain"><div class="wcp-stat-num" data-wcp-pkg-stat="queue">0</div><div class="wcp-stat-label">In the queue</div></div></div>
</div>

<div class="wcp-subtabs" role="tablist">
    <button class="active" type="button" role="tab" data-bs-toggle="tab" data-bs-target="#updates" aria-controls="updates" aria-selected="true"><i class="bi bi-arrow-up-circle me-1"></i>Updates <span class="badge text-bg-light border"><?= count($ups) ?></span></button>
    <button type="button" role="tab" data-bs-toggle="tab" data-bs-target="#components" aria-controls="components" aria-selected="false"><i class="bi bi-box-seam me-1"></i>Components <span class="badge text-bg-light border"><?= count($packages) ?></span></button>
</div>
<div class="tab-content">
<div class="tab-pane fade show active" id="updates" role="tabpanel">
<div class="card">
    <div class="card-header d-flex align-items-center gap-2"><i class="bi bi-arrow-up-circle"></i>Updates
        <?php if (!empty($updates['index_stale'])): ?> <span class="badge text-bg-warning" title="Update information is only as current as the package index">index out of date: check for updates</span><?php endif; ?>
        <?php if ($ups !== []): ?>
            <form class="ms-auto" method="post" action="<?= e($this->url('packages.upgrade')) ?>" data-wcp-confirm="Install <?= count($ups) ?> update(s) now? Services may restart briefly. Local configuration files are kept."><?= $this->csrfField() ?><button class="btn btn-sm btn-primary">Install all updates</button></form>
        <?php endif; ?>
    </div>
    <?php if ($ups === []): ?>
        <div class="card-body text-body-secondary">The system is up to date (as of the last package index refresh).</div>
    <?php else: ?>
        <div class="table-responsive wcp-scroll-400"><table class="table table-sm mb-0">
            <thead><tr><th>Package</th><th>Installed</th><th>Available</th></tr></thead>
            <tbody>
            <?php foreach ($ups as $u): ?>
                <tr><td><?= e($u['name']) ?><?= !empty($u['security']) ? ' <span class="badge text-bg-danger">security</span>' : '' ?></td><td class="small text-body-secondary"><?= e($u['current']) ?></td><td class="small"><?= e($u['new']) ?></td></tr>
            <?php endforeach; ?>
            </tbody>
        </table></div>
    <?php endif; ?>
</div>
</div>
<div class="tab-pane fade" id="components" role="tabpanel">
<div class="card">
    <div class="card-header d-flex align-items-center gap-2"><i class="bi bi-box-seam"></i>Components
        <span class="ms-auto small text-body-secondary">Install several at once from the installation queue.</span>
    </div>
    <ul class="list-group list-group-flush" data-wcp-pkg-list data-url="<?= e($this->url('packages')) ?>" data-install-url="<?= e($this->url('packages.install')) ?>">
        <?php foreach ($packages as $p): ?>
            <li class="list-group-item d-flex align-items-center gap-2" data-wcp-pkg="<?= e($p['name']) ?>">
                <div><strong><?= e($p['name']) ?></strong><div class="small text-body-secondary"><?= e($p['description']) ?></div></div>
                <div class="ms-auto text-end" data-wcp-pkg-state>
                    <?php $state = (string) ($p['state'] ?? ''); ?>
                    <?php if ($p['installed'] && $state === 'installed'): ?>
                        <span class="badge text-bg-warning" title="<?= e((string) ($p['reason'] ?? '')) ?>"><i class="bi bi-exclamation-triangle"></i> Installed, not usable</span>
                        <div class="small text-body-secondary"><?= e($p['version']) ?></div>
                        <div class="small text-body-secondary text-wrap wcp-maxw-28"><?= e((string) ($p['reason'] ?? '')) ?></div>
                    <?php elseif ($p['installed']): ?>
                        <span class="badge text-bg-success"><i class="bi bi-check-circle"></i> <?= ($p['source'] ?? '') === 'other' ? 'Available' : 'Installed' ?></span>
                        <div class="small text-body-secondary"><?= ($p['source'] ?? '') === 'other' ? 'without the package (built or installed by hand)' : e($p['version']) ?></div>
                    <?php elseif ($state === 'installed'): ?>
                        <span class="badge text-bg-warning" title="<?= e((string) ($p['reason'] ?? '')) ?>"><i class="bi bi-exclamation-triangle"></i> Present, not usable</span>
                        <div class="small text-body-secondary text-wrap wcp-maxw-28"><?= e((string) ($p['reason'] ?? '')) ?></div>
                        <form method="post" action="<?= e($this->url('packages.install')) ?>" data-wcp-pkg-install><?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e($p['name']) ?>"><button class="btn btn-sm btn-outline-primary">Install package</button></form>
                    <?php else: ?>
                        <form method="post" action="<?= e($this->url('packages.install')) ?>" data-wcp-pkg-install><?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e($p['name']) ?>"><button class="btn btn-sm btn-outline-primary">Install</button></form>
                    <?php endif; ?>
                </div>
            </li>
        <?php endforeach; ?>
    </ul>
    <div class="card-footer small text-body-secondary">PHP versions are installed under <a href="<?= e($this->url('php')) ?>">PHP</a>.</div>
</div>
</div>
</div>

<div class="modal fade" id="wcp-pkg-queue" tabindex="-1" aria-labelledby="wcp-pkg-queue-title" aria-hidden="true">
    <div class="modal-dialog modal-xl modal-dialog-centered modal-dialog-scrollable">
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title h6" id="wcp-pkg-queue-title"><i class="bi bi-box-seam me-1"></i>Installation queue</h2>
                <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
            </div>
            <div class="modal-body">
                <p class="small text-body-secondary mb-2">Components are installed one after another. Add as many as you like; you can close this window and the installation continues on the server.</p>
                <div class="row g-3">
                    <div class="col-lg-5">
                        <div class="fw-semibold small mb-1">Queue</div>
                        <ul class="list-group mb-2 wcp-pkg-items" data-wcp-pkg-items aria-label="Queued components"></ul>
                        <p class="small text-body-secondary" data-wcp-pkg-empty>Nothing queued yet.</p>
                        <div class="border rounded p-2" data-wcp-pkg-addbox>
                            <div class="d-flex align-items-center mb-1"><span class="fw-semibold small">Add components</span>
                                <button type="button" class="btn btn-link btn-sm p-0 ms-auto" data-wcp-check-all="[data-wcp-pkg-add]">All</button></div>
                            <div class="wcp-pkg-add" data-wcp-pkg-add></div>
                            <p class="small text-body-secondary mb-0" data-wcp-pkg-add-none hidden>Every component is installed or queued.</p>
                            <button type="button" class="btn btn-sm btn-primary mt-2" data-wcp-pkg-add-btn disabled><i class="bi bi-plus-lg me-1"></i>Add to queue</button>
                        </div>
                    </div>
                    <div class="col-lg-7">
                        <div class="small fw-semibold mb-1" data-wcp-pkg-logname>Output</div>
                        <pre class="wcp-pre wcp-joblog wcp-pkg-out mb-0" data-wcp-pkg-log aria-live="polite">Select a component in the queue to see its output.</pre>
                    </div>
                </div>
            </div>
        </div>
    </div>
</div>
