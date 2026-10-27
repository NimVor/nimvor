<?php
/**
 * Server → Runtimes: the Node.js and Python runtimes on the server.
 *
 * @var \WhaCP\View\View $this
 * @var array{arch: string, node: list<array<string, mixed>>, python: list<array<string, mixed>>, system: list<array<string, mixed>>}|null $status
 */
use WhaCP\Security\Permissions as P;

$status ??= null;
$canManage = $this->can(P::SERVER_MANAGE);
?>
<?php $this->start(); ?>Runtimes<?php $this->end('title'); ?>
<?php $this->start(); ?>Node.js and Python versions for website workers and apps<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if ($canManage && $status !== null): ?>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#add-node"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Install Node.js</button>
    <?php endif; ?>
<?php $this->end('actions'); ?>

<?php if ($status === null): ?>
    <?= $this->partial('partials/agent_error', ['error' => $agentError ?? null]) ?>
    <?php return; ?>
<?php endif; ?>

<?php
$rtRow = function (array $rt, bool $managed) use ($canManage): string {
    ob_start();
    ?>
    <tr>
        <td class="fw-medium"><?= e(ucfirst((string) ($rt['kind'] ?? ''))) ?></td>
        <td class="font-monospace small"><?= e((string) ($rt['version'] ?? '')) ?></td>
        <td class="small text-body-secondary font-monospace text-break"><?= e((string) ($rt['binary'] ?? '')) ?></td>
        <td class="text-end">
            <?php if ($managed && $canManage): ?>
                <form method="post" action="<?= e($this->url('runtimes.remove')) ?>" data-wcp-confirm="Remove <?= e((string) ($rt['kind'] ?? '') . ' ' . (string) ($rt['version'] ?? '')) ?>? Workers using it will fail to start until changed.">
                    <?= $this->csrfField() ?>
                    <input type="hidden" name="kind" value="<?= e((string) ($rt['kind'] ?? '')) ?>">
                    <input type="hidden" name="version" value="<?= e((string) ($rt['version'] ?? '')) ?>">
                    <button class="btn btn-sm btn-outline-danger" title="Remove"><i class="bi bi-trash"></i></button>
                </form>
            <?php elseif (!$managed): ?>
                <span class="wcp-state small">system</span>
            <?php endif; ?>
        </td>
    </tr>
    <?php
    return (string) ob_get_clean();
};
$managed = array_merge($status['node'], $status['python']);
?>

<div class="card mb-3">
    <div class="card-header">Installed runtimes</div>
    <?php if ($managed === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-box-seam" aria-hidden="true"></i><div class="fw-medium">No managed runtimes yet</div><div class="small">Install a Node.js version to run Node apps and workers on it. Node.js downloads come from nodejs.org and are verified before use.</div></div>
    <?php else: ?>
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Runtime</th><th>Version</th><th>Path</th><th class="text-end">Action</th></tr></thead>
                <tbody><?php foreach ($managed as $rt) { echo $rtRow($rt, true); } ?></tbody>
            </table>
        </div>
    <?php endif; ?>
</div>

<div class="card">
    <div class="card-header">On the system</div>
    <?php if ($status['system'] === []): ?>
        <div class="card-body small text-body-secondary">No system Node.js or Python interpreters were found.</div>
    <?php else: ?>
        <div class="wcp-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Runtime</th><th>Version</th><th>Path</th><th class="text-end"></th></tr></thead>
                <tbody><?php foreach ($status['system'] as $rt) { echo $rtRow($rt, false); } ?></tbody>
            </table>
        </div>
        <div class="card-footer small text-body-secondary">Install more Python versions (python3.x) under <a href="<?= e($this->url('packages')) ?>">Packages</a>. They are then available to workers that run <code>python3.x</code> directly.</div>
    <?php endif; ?>
</div>

<?php if ($canManage): ?>
<?= $this->modalStart('add-node', 'Install Node.js') ?>
    <form method="post" action="<?= e($this->url('runtimes.install')) ?>" data-wcp-busy>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <label class="form-label" for="rt-ver">Version</label>
            <input class="form-control" id="rt-ver" name="version" placeholder="20.17.0" pattern="\d{1,2}\.\d{1,3}\.\d{1,3}" required>
            <div class="form-text">An exact release from nodejs.org, e.g. 20.17.0 or 22.9.0. It is downloaded, verified against the release checksums and installed alongside any others<?= $status['arch'] !== '' ? ' (' . e($status['arch']) . ')' : '' ?>.</div>
            <button class="btn btn-primary btn-sm mt-3" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Install</button>
        </div>
    </form>
<?= $this->modalEnd() ?>
<?php endif; ?>
