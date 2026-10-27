<?php
/** @var \WhaCP\View\View $this */
/** @var array<string, mixed> $site */
/** @var list<array<string, mixed>> $workers */
/** @var array<string, array<string, mixed>> $status */
/** @var array<string, mixed>|null $edit */
use WhaCP\Services\WorkerService;

$id = (int) $site['id'];
$errors ??= [];
$form = $old !== [] ? $old : ($edit ?? []);
$envText = $form['env'] ?? '';
if (is_array($envText)) {
    $envText = implode("\n", array_map(static fn ($k, $v) => $k . '=' . $v, array_keys($envText), $envText));
}
$fmtMem = static fn (?int $b): string => $b === null ? '' : number_format($b / 1048576, 1) . ' MB';
?>
<?= $this->partial('sites/header', ['site' => $site, 'tab' => 'workers']) ?>
<?php $open = $edit || $errors !== []; ?>
<div class="card" id="workers">
    <div class="card-header wcp-toolbar">
        <h2 class="wcp-toolbar-title"><i class="bi bi-gear-wide-connected me-1"></i>Background workers <span class="badge rounded-pill text-bg-light border fw-normal"><?= count($workers) ?></span></h2>
        <button class="btn btn-sm btn-primary" type="button" data-bs-toggle="modal" data-bs-target="#new-worker"><i class="bi bi-plus-lg me-1"></i>Add worker</button>
    </div>
    <?= $this->modalStart('new-worker', $edit ? 'Edit worker' : 'Add worker', $open) ?>
        <div class="p-3">
                <form method="post" action="<?= e($this->url('sites.workers.store', ['id' => $id])) ?>" data-wcp-busy>
                    <?= $this->csrfField() ?>
                    <?php if ($edit): ?><input type="hidden" name="worker_id" value="<?= (int) $edit['id'] ?>"><?php endif; ?>
                    <?= $this->partial('partials/field', ['name' => 'name', 'label' => 'Name', 'value' => (string) ($form['name'] ?? ''), 'attrs' => ['required' => 'required', 'placeholder' => 'Queue worker']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'command', 'label' => 'Command', 'value' => (string) ($form['command'] ?? ''), 'attrs' => ['required' => 'required', 'placeholder' => 'php artisan queue:work --sleep=3 --tries=3']]) ?>
                    <?= $this->partial('partials/field', ['name' => 'directory', 'label' => 'Working folder', 'value' => (string) ($form['directory'] ?? ''), 'help' => 'Relative to the website root; empty for the root itself.', 'attrs' => ['placeholder' => 'app']]) ?>
                    <label class="form-label" for="w-env">Environment variables</label>
                    <textarea class="form-control font-monospace mb-1<?= isset($errors['env']) ? ' is-invalid' : '' ?>" id="w-env" name="env" rows="3" placeholder="NODE_ENV=production&#10;PORT=3000"><?= e((string) $envText) ?></textarea>
                    <?php if (isset($errors['env'])): ?><div class="invalid-feedback"><?= e($errors['env']) ?></div><?php endif; ?>
                    <div class="form-text mb-3">One KEY=value per line. Stored in a root-only file, readable by the process.</div>
                    <div class="row g-2">
                        <div class="col-4">
                            <label class="form-label" for="w-restart">Restart</label>
                            <select class="form-select" id="w-restart" name="restart">
                                <?php foreach (WorkerService::RESTART as $k => $label): ?><option value="<?= e($k) ?>"<?= ($form['restart'] ?? 'always') === $k ? ' selected' : '' ?>><?= e($label) ?></option><?php endforeach; ?>
                            </select>
                        </div>
                        <div class="col-4"><?= $this->partial('partials/field', ['name' => 'memory_mb', 'label' => 'Memory (MB, each)', 'type' => 'number', 'value' => (string) ($form['memory_mb'] ?? '0'), 'attrs' => ['min' => '0', 'max' => '65536']]) ?></div>
                        <div class="col-4"><?= $this->partial('partials/field', ['name' => 'procs', 'label' => 'Copies', 'type' => 'number', 'value' => (string) ($form['procs'] ?? '1'), 'help' => 'Identical processes to run.', 'attrs' => ['min' => '1', 'max' => (string) WorkerService::MAX_PROCS]]) ?></div>
                    </div>
                    <?php $runtimes ??= []; ?>
                    <div class="mt-2">
                        <label class="form-label" for="w-runtime">Runtime</label>
                        <select class="form-select" id="w-runtime" name="runtime">
                            <option value="system"<?= ($form['runtime'] ?? '') === '' ? ' selected' : '' ?>>System default</option>
                            <?php foreach ($runtimes as $rt): ?><option value="<?= e($rt) ?>"<?= ($form['runtime'] ?? '') === $rt ? ' selected' : '' ?>><?= e($rt) ?></option><?php endforeach; ?>
                        </select>
                        <div class="form-text">Managed Node.js/Python runtimes (Server → Runtimes) are put on the command's PATH. “System default” uses the server's own.</div>
                    </div>
                    <div class="form-check form-switch mb-3"><input type="hidden" name="enabled" value="0"><input class="form-check-input" type="checkbox" role="switch" id="w-en" name="enabled" value="1"<?= ($form['enabled'] ?? true) ? ' checked' : '' ?>><label class="form-check-label" for="w-en">Running</label></div>
                    <button class="btn btn-primary btn-sm" type="submit"><span class="spinner-border spinner-border-sm d-none me-1" aria-hidden="true"></span><?= $edit ? 'Save and restart' : 'Create worker' ?></button>
                    <?php if ($edit): ?><a class="btn btn-link btn-sm" href="<?= e($this->url('sites.workers', ['id' => $id])) ?>">Cancel</a><?php endif; ?>
                </form>
        </div>
    <?= $this->modalEnd() ?>
    <?php if ($workers === []): ?>
        <div class="wcp-empty-state"><i class="bi bi-gear-wide-connected" aria-hidden="true"></i><div class="fw-medium">No workers yet</div><div class="small">Keep a command running: queue consumers (<code>php artisan queue:work</code>), Node.js or Python servers and other long-running processes.</div></div>
    <?php else: ?>
                <div class="table-responsive">
                    <table class="table table-sm align-middle mb-0">
                        <thead><tr><th>Name</th><th>Command</th><th>State</th><th class="text-end">Actions</th></tr></thead>
                        <tbody>
                        <?php foreach ($workers as $w): $st = $status[$w['unit']] ?? null; ?>
                            <tr>
                                <td><?= e($w['name']) ?><div class="small text-body-secondary"><code><?= e($w['unit']) ?></code></div></td>
                                <td><code class="small text-break"><?= e($w['command']) ?></code><div class="small text-body-secondary">in <?= e($w['directory'] === '' ? '(website root)' : $w['directory']) ?> · restart: <?= e(WorkerService::RESTART[$w['restart']] ?? $w['restart']) ?><?= (int) $w['memory_mb'] > 0 ? ' · max ' . (int) $w['memory_mb'] . ' MB each' : '' ?><?= (int) ($w['procs'] ?? 1) > 1 ? ' · ' . (int) $w['procs'] . ' copies' : '' ?><?= ($w['runtime'] ?? '') !== '' ? ' · ' . e((string) $w['runtime']) : '' ?></div></td>
                                <td class="text-nowrap">
                                    <?php if ($st === null): ?><span class="text-body-secondary small">unknown</span>
                                    <?php else: ?><?= $this->statusBadge((string) $st['active_state']) ?><div class="small text-body-secondary"><?php if ((int) ($st['procs_total'] ?? 1) > 1): ?><?= (int) ($st['procs_running'] ?? 0) ?>/<?= (int) $st['procs_total'] ?> up · <?php endif; ?><?= e($fmtMem(isset($st['memory_bytes']) ? (int) $st['memory_bytes'] : null)) ?></div><?php endif; ?>
                                </td>
                                <td class="text-end text-nowrap">
                                    <?php if ($this->can(\WhaCP\Security\Permissions::LOGS_VIEW)): ?>
                                        <a class="btn btn-sm btn-outline-secondary" title="Log" href="<?= e($this->url('sites.logs', ['id' => $id], ['source' => 'journal:' . $w['unit']])) ?>"><i class="bi bi-journal-text"></i></a>
                                    <?php endif; ?>
                                    <?php if ($w['enabled']): ?>
                                        <form class="d-inline" method="post" action="<?= e($this->url('sites.workers.restart', ['id' => $id, 'worker' => (int) $w['id']])) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary" title="Restart"><i class="bi bi-arrow-clockwise"></i></button></form>
                                    <?php endif; ?>
                                    <a class="btn btn-sm btn-outline-secondary" title="Edit" href="<?= e($this->url('sites.workers', ['id' => $id], ['edit' => (int) $w['id']])) ?>"><i class="bi bi-pencil"></i></a>
                                    <form class="d-inline" method="post" action="<?= e($this->url('sites.workers.toggle', ['id' => $id, 'worker' => (int) $w['id']])) ?>" data-wcp-busy><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-secondary" title="<?= $w['enabled'] ? 'Stop' : 'Start' ?>"><i class="bi bi-<?= $w['enabled'] ? 'stop' : 'play' ?>"></i></button></form>
                                    <form class="d-inline" method="post" action="<?= e($this->url('sites.workers.delete', ['id' => $id, 'worker' => (int) $w['id']])) ?>" data-wcp-confirm="Stop and remove the worker “<?= e($w['name']) ?>”?"><?= $this->csrfField() ?><button class="btn btn-sm btn-outline-danger" title="Remove"><i class="bi bi-trash"></i></button></form>
                                </td>
                            </tr>
                        <?php endforeach; ?>
                        </tbody>
                    </table>
                </div>
    <?php endif; ?>
    <div class="card-footer small text-body-secondary">Workers run as <code><?= e($site['system_user']) ?></code> under systemd with a private /tmp and no privilege escalation. They stop while the website is disabled.</div>
</div>
