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
            <div class="card-header"><i class="bi bi-box-seam me-1"></i>Components</div>
            <ul class="list-group list-group-flush">
                <?php foreach ($packages as $p): ?>
                    <li class="list-group-item d-flex align-items-center gap-2">
                        <div><strong><?= e($p['name']) ?></strong><div class="small text-body-secondary"><?= e($p['description']) ?></div></div>
                        <div class="ms-auto text-end">
                            <?php if ($p['installed']): ?>
                                <span class="small text-body-secondary"><?= e($p['version']) ?></span>
                            <?php else: ?>
                                <form method="post" action="<?= e($this->url('packages.install')) ?>" data-wcp-busy><?= $this->csrfField() ?><input type="hidden" name="name" value="<?= e($p['name']) ?>"><button class="btn btn-sm btn-outline-primary">Install</button></form>
                            <?php endif; ?>
                        </div>
                    </li>
                <?php endforeach; ?>
            </ul>
            <div class="card-footer small text-body-secondary">PHP versions are installed under <a href="<?= e($this->url('php')) ?>">PHP</a>.</div>
        </div>
    </div>
</div>
