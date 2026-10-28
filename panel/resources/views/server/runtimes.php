<?php
/**
 * Server → Runtimes: the Node.js and Python runtimes on the server.
 *
 * @var \NimVor\View\View $this
 * @var array{arch: string, node: list<array<string, mixed>>, python: list<array<string, mixed>>, system: list<array<string, mixed>>}|null $status
 * @var array{node: list<array<string, mixed>>, python: list<array<string, mixed>>, node_error: string, python_error: string}|null $available
 */
use NimVor\Security\Permissions as P;

$status ??= null;
$available ??= null;
$canManage = $this->can(P::SERVER_MANAGE);
?>
<?php $this->start(); ?>Runtimes<?php $this->end('title'); ?>
<?php $this->start(); ?>Node.js and Python versions for website workers and apps<?php $this->end('subtitle'); ?>
<?php $this->start(); ?>
    <?php if ($canManage && $status !== null): ?>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#add-node"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Install Node.js</button>
        <button class="btn btn-sm btn-outline-primary" type="button" data-bs-toggle="modal" data-bs-target="#add-python"><i class="bi bi-plus-lg me-1" aria-hidden="true"></i>Install Python</button>
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
                <form method="post" action="<?= e($this->url('runtimes.remove')) ?>" data-nim-confirm="Remove <?= e((string) ($rt['kind'] ?? '') . ' ' . (string) ($rt['version'] ?? '')) ?>? Workers using it will fail to start until changed.">
                    <?= $this->csrfField() ?>
                    <input type="hidden" name="kind" value="<?= e((string) ($rt['kind'] ?? '')) ?>">
                    <input type="hidden" name="version" value="<?= e((string) ($rt['version'] ?? '')) ?>">
                    <button class="btn btn-sm btn-outline-danger" title="Remove"><i class="bi bi-trash"></i></button>
                </form>
            <?php elseif (!$managed): ?>
                <span class="nim-state small">system</span>
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
        <div class="nim-empty-state"><i class="bi bi-box-seam" aria-hidden="true"></i><div class="fw-medium">No managed runtimes yet</div><div class="small">Install Node.js or Python versions to run apps and workers on them. Downloads are verified against published checksums before use.</div></div>
    <?php else: ?>
        <div class="nim-dtable table-responsive">
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
        <div class="nim-dtable table-responsive">
            <table class="table align-middle mb-0">
                <thead><tr><th>Runtime</th><th>Version</th><th>Path</th><th class="text-end"></th></tr></thead>
                <tbody><?php foreach ($status['system'] as $rt) { echo $rtRow($rt, false); } ?></tbody>
            </table>
        </div>
        <div class="card-footer small text-body-secondary">Use <strong>Install Python</strong> to add stable Python versions alongside the system ones.</div>
    <?php endif; ?>
</div>

<?php
$versionModal = function (string $id, string $kind, string $label, string $source) use ($available, $status): void {
    $list = (array) ($available[$kind] ?? []);
    $err = (string) ($available[$kind . '_error'] ?? '');
    echo $this->modalStart($id, 'Install ' . $label);
    ?>
    <form method="post" action="<?= e($this->url('runtimes.install')) ?>" data-nim-busy>
        <div class="p-3">
            <?= $this->csrfField() ?>
            <input type="hidden" name="kind" value="<?= e($kind) ?>">
            <?php if ($available === null || $err !== '' || $list === []): ?>
                <div class="alert alert-warning small">The list of <?= e($label) ?> versions could not be loaded<?= $err !== '' ? ': ' . e($err) : '' ?>. Check the server's internet access and reload the page.</div>
            <?php else: ?>
                <div class="fw-medium mb-2">Stable <?= e($label) ?> versions</div>
                <?php foreach ($list as $i => $v): $ver = (string) ($v['version'] ?? ''); $done = !empty($v['installed']); ?>
                    <div class="form-check mb-1">
                        <input class="form-check-input" type="checkbox" name="versions[]" value="<?= e($ver) ?>" id="<?= e($id . '-' . $i) ?>"<?= $done ? ' disabled checked' : '' ?>>
                        <label class="form-check-label" for="<?= e($id . '-' . $i) ?>">
                            <span class="font-monospace"><?= e($ver) ?></span>
                            <?php if ((string) ($v['label'] ?? '') !== ''): ?><span class="badge text-bg-secondary ms-1"><?= e((string) $v['label']) ?></span><?php endif; ?>
                            <?php if ($done): ?><span class="text-body-secondary small ms-1">installed</span><?php endif; ?>
                        </label>
                    </div>
                <?php endforeach; ?>
                <div class="form-text mt-2">Tick the versions to install. Each is downloaded from <?= e($source) ?>, verified against its published checksum and installed alongside any others<?= $kind === 'node' && ($status['arch'] ?? '') !== '' ? ' (' . e((string) $status['arch']) . ')' : '' ?>.</div>
                <button class="btn btn-primary btn-sm mt-3" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span>Install selected</button>
            <?php endif; ?>
        </div>
    </form>
    <?php
    echo $this->modalEnd();
};
?>
<?php if ($canManage): ?>
    <?php $versionModal('add-node', 'node', 'Node.js', 'nodejs.org'); ?>
    <?php $versionModal('add-python', 'python', 'Python', 'the python-build-standalone project'); ?>
<?php endif; ?>
