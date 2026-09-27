<?php
/** @var \WhaCP\View\View $this */
/** @var list<array<string, mixed>> $packages */
/** @var array<string, mixed>|null $updates */
$ups = (array) ($updates['updates'] ?? []);
?>
<?php $this->start(); ?>Packages<?php $this->end('title'); ?>
<?php $this->start(); ?>Components and system updates<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
<form method="post" action="<?= e($this->url('packages.refresh')) ?>"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary"><i class="bi bi-arrow-repeat"></i> Check for updates</button></form>
<?php $this->end('actions'); ?>
<?php if ($agentError !== null): ?><?= $this->partial('partials/agent_error', ['error' => $agentError]) ?><?php endif; ?>
<div class="row g-3">
    <div class="col-xl-7">
        <div class="card">
            <div class="card-header d-flex align-items-center gap-2"><i class="bi bi-arrow-up-circle"></i>Updates
                <?php if ($updates !== null): ?><span class="small text-body-secondary"><?= (int) $updates['count'] ?> pending, <?= (int) $updates['security'] ?> security</span><?php endif; ?>
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
    <div class="col-xl-5">
        <div class="card">
            <div class="card-header d-flex align-items-center gap-2"><i class="bi bi-box-seam"></i>Components
                <button type="button" class="btn btn-sm btn-outline-secondary ms-auto d-none" data-wcp-pkg-show><i class="bi bi-list-task"></i> Queue <span class="badge text-bg-secondary" data-wcp-pkg-count>0</span></button>
            </div>
            <ul class="list-group list-group-flush" data-wcp-pkg-list data-url="<?= e($this->url('packages')) ?>" data-install-url="<?= e($this->url('packages.install')) ?>">
                <?php foreach ($packages as $p): ?>
                    <li class="list-group-item d-flex align-items-center gap-2" data-wcp-pkg="<?= e($p['name']) ?>">
                        <div><strong><?= e($p['name']) ?></strong><div class="small text-body-secondary"><?= e($p['description']) ?></div></div>
                        <div class="ms-auto text-end" data-wcp-pkg-state>
                            <?php if ($p['installed']): ?>
                                <span class="badge text-bg-success"><i class="bi bi-check-circle"></i> Installed</span>
                                <div class="small text-body-secondary"><?= e($p['version']) ?></div>
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
    <div class="modal-dialog modal-lg modal-dialog-centered modal-dialog-scrollable">
        <div class="modal-content">
            <div class="modal-header">
                <h2 class="modal-title h6" id="wcp-pkg-queue-title"><i class="bi bi-box-seam me-1"></i>Installing components</h2>
                <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
            </div>
            <div class="modal-body">
                <p class="small text-body-secondary mb-2">Components are installed one after another. You can close this window: the installation continues on the server.</p>
                <ul class="list-group mb-3" data-wcp-pkg-items></ul>
                <div class="small fw-semibold mb-1" data-wcp-pkg-logname></div>
                <pre class="wcp-pre wcp-joblog mb-0" data-wcp-pkg-log aria-live="polite"></pre>
            </div>
        </div>
    </div>
</div>
